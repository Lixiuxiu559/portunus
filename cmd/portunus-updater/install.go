package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type updateJob struct {
	Target  string `json:"target"`
	Current string `json:"current"`
	Version string `json:"version"`
	Token   string `json:"token"`
}

func copyFile(from, to string, mode os.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func prepare(archive, signature, target, version, current string) (root string, err error) {
	if !filepath.IsAbs(target) || filepath.Ext(target) != ".app" {
		return "", fmt.Errorf("安装目标无效")
	}
	real, err := filepath.EvalSymlinks(target)
	if err != nil || real != filepath.Clean(target) {
		return "", fmt.Errorf("安装目标不可为符号链接")
	}
	if err = validateBundle(target, current); err != nil {
		return "", err
	}
	verified, err := verifyArchive(archive, signature, version, current, publicKeyPEM)
	if err != nil {
		return "", err
	}
	if existing := preparedUpdate(target, version, current, verified.SHA512); existing != "" {
		return existing, nil
	}
	root, err = os.MkdirTemp(filepath.Dir(target), ".portunus-update-")
	if err != nil {
		return "", fmt.Errorf("安装目录不可写，请将应用移到可写目录: %w", err)
	}
	defer func() {
		if err != nil {
			os.RemoveAll(root)
		}
	}()
	if err = copyFile(archive, filepath.Join(root, "update.zip"), 0600); err != nil {
		return root, err
	}
	if err = copyFile(signature, filepath.Join(root, "update.sig"), 0600); err != nil {
		return root, err
	}
	// 校验私有 staging 中的副本，避免下载缓存被改动后带入未验证内容。
	if _, err = verifyArchive(filepath.Join(root, "update.zip"), filepath.Join(root, "update.sig"), version, current, publicKeyPEM); err != nil {
		return root, err
	}
	extract := filepath.Join(root, "unpacked")
	if err = os.Mkdir(extract, 0700); err != nil {
		return root, err
	}
	if err = extractArchive(filepath.Join(root, "update.zip"), extract); err != nil {
		return root, err
	}
	app := filepath.Join(extract, "Portunus.app")
	if err = validateBundle(app, version); err != nil {
		return root, err
	}
	if err = command("/usr/bin/xattr", "-dr", "com.apple.quarantine", app); err != nil {
		return root, err
	}
	if err = command("/usr/bin/codesign", "--force", "--deep", "--sign", "-", "--timestamp=none", app); err != nil {
		return root, err
	}
	if err = command("/usr/bin/codesign", "--verify", "--deep", "--strict", app); err != nil {
		return root, err
	}
	exe, err := os.Executable()
	if err != nil {
		return root, err
	}
	if err = copyFile(exe, filepath.Join(root, "installer"), 0700); err != nil {
		return root, err
	}
	token := make([]byte, 32)
	if _, err = rand.Read(token); err != nil {
		return root, err
	}
	job := updateJob{target, current, version, hex.EncodeToString(token)}
	data, err := json.Marshal(job)
	if err != nil {
		return root, err
	}
	err = os.WriteFile(filepath.Join(root, "job.json"), data, 0600)
	return root, err
}

// 普通退出不安装；下次启动复用经过再次校验的准备包，避免每次启动都留下一个应用副本。
func preparedUpdate(target, version, current, digest string) string {
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), ".portunus-update-") {
			continue
		}
		root := filepath.Join(filepath.Dir(target), entry.Name())
		job, err := readJob(root)
		if err != nil || job.Target != target || job.Version != version || job.Current != current {
			continue
		}
		if _, err = os.Stat(filepath.Join(root, "result.json")); !os.IsNotExist(err) {
			continue
		}
		info, err := verifyArchive(filepath.Join(root, "update.zip"), filepath.Join(root, "update.sig"), version, current, publicKeyPEM)
		if err != nil || info.SHA512 != digest {
			continue
		}
		installer, err := os.Stat(filepath.Join(root, "installer"))
		if err != nil || !installer.Mode().IsRegular() || installer.Mode().Perm()&0100 == 0 {
			continue
		}
		app := filepath.Join(root, "unpacked/Portunus.app")
		if validateBundle(app, version) != nil || command("/usr/bin/codesign", "--verify", "--deep", "--strict", app) != nil {
			continue
		}
		return root
	}
	return ""
}

func readJob(root string) (updateJob, error) {
	var job updateJob
	real, err := filepath.EvalSymlinks(root)
	if err != nil || real != root || !filepath.IsAbs(root) || !strings.HasPrefix(filepath.Base(root), ".portunus-update-") {
		return job, fmt.Errorf("更新事务目录无效")
	}
	stat, err := os.Stat(root)
	if err != nil || stat.Mode().Perm() != 0700 {
		return job, fmt.Errorf("更新目录权限无效")
	}
	data, err := os.ReadFile(filepath.Join(root, "job.json"))
	if err != nil {
		return job, err
	}
	if err = json.Unmarshal(data, &job); err != nil {
		return job, err
	}
	if filepath.Dir(job.Target) != filepath.Dir(root) || filepath.Ext(job.Target) != ".app" || !newer(job.Version, job.Current) || len(job.Token) != 64 {
		return job, fmt.Errorf("更新事务无效")
	}
	return job, nil
}

type installOptions struct {
	exitTimeout   time.Duration
	healthTimeout time.Duration
	alive         func(int) bool
	launch        func(string, updateJob) (func(), error)
}

func install(root string, parent int, opts installOptions) error {
	job, err := readJob(root)
	if err != nil {
		return err
	}
	lock := filepath.Join(filepath.Dir(job.Target), "."+filepath.Base(job.Target)+".update-lock")
	releaseLock, err := acquireInstallLock(lock)
	if err != nil {
		return fmt.Errorf("已有更新事务或安装目录不可写: %w", err)
	}
	defer releaseLock()
	if parent <= 1 {
		return fmt.Errorf("父进程无效")
	}
	// 再次验证签名、应用身份和已准备目录；就绪之前任何失败均不退出旧应用。
	if _, err = verifyArchive(filepath.Join(root, "update.zip"), filepath.Join(root, "update.sig"), job.Version, job.Current, publicKeyPEM); err != nil {
		return err
	}
	staged := filepath.Join(root, "unpacked/Portunus.app")
	if err = validateBundle(staged, job.Version); err != nil {
		return err
	}
	if err = command("/usr/bin/codesign", "--verify", "--deep", "--strict", staged); err != nil {
		return err
	}
	if err = validateBundle(job.Target, job.Current); err != nil {
		return err
	}
	fmt.Println(`{"ready":true}`)
	deadline := time.Now().Add(opts.exitTimeout)
	for opts.alive(parent) {
		if time.Now().After(deadline) {
			return fmt.Errorf("旧应用退出超时，未替换")
		}
		time.Sleep(100 * time.Millisecond)
	}
	return replaceAndLaunch(root, job, opts)
}

func replaceAndLaunch(root string, job updateJob, opts installOptions) error {
	var err error
	staged := filepath.Join(root, "unpacked/Portunus.app")
	backup := filepath.Join(root, "Previous.app")
	if err = os.Rename(job.Target, backup); err != nil {
		_, restartErr := opts.launch(job.Target, updateJob{})
		return fmt.Errorf("备份失败: %v；旧版重新启动: %v", err, restartErr)
	}
	if err = os.Rename(staged, job.Target); err != nil {
		restoreErr := os.Rename(backup, job.Target)
		var restartErr error
		if restoreErr == nil {
			_, restartErr = opts.launch(job.Target, updateJob{})
		}
		return fmt.Errorf("替换失败: %v；恢复结果: %v；旧版重新启动: %v", err, restoreErr, restartErr)
	}
	stop, launchErr := opts.launch(job.Target, job)
	healthy := false
	if launchErr == nil {
		deadline := time.Now().Add(opts.healthTimeout)
		for time.Now().Before(deadline) {
			data, _ := os.ReadFile(filepath.Join(root, "healthy"))
			if string(data) == job.Token {
				healthy = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if healthy {
		if err = os.RemoveAll(backup); err != nil {
			return fmt.Errorf("新版已启动，清理备份失败: %w", err)
		}
		os.Remove(filepath.Join(root, "update.zip"))
		os.Remove(filepath.Join(root, "update.sig"))
		return os.WriteFile(filepath.Join(root, "result.json"), []byte(`{"status":"installed"}`), 0600)
	}
	if stop != nil {
		stop()
	}
	failed := filepath.Join(root, "Failed.app")
	if err = os.Rename(job.Target, failed); err != nil {
		return fmt.Errorf("新版启动失败，移出失败应用失败，备份位于 %s: %w", backup, err)
	}
	if err = os.Rename(backup, job.Target); err != nil {
		return fmt.Errorf("恢复旧版失败，备份位于 %s: %w", backup, err)
	}
	// 旧版不携带确认参数，防止失败事务被误标成功。
	_, restartErr := opts.launch(job.Target, updateJob{})
	return fmt.Errorf("新版未确认启动，已恢复旧版（启动错误: %v，恢复启动: %v）", launchErr, restartErr)
}

func launchApp(target string, job updateJob) (func(), error) {
	executable, err := plistValue(target, "CFBundleExecutable")
	if err != nil {
		return nil, err
	}
	args := []string{}
	if job.Token != "" {
		args = append(args, "--portunus-update-root="+os.Getenv("PORTUNUS_INSTALL_ROOT"), "--portunus-update-token="+job.Token)
	}
	cmd := exec.Command(filepath.Join(target, "Contents/MacOS", executable), args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	setupProcessGroup(cmd)
	// 只传递当前安装器的正常运行环境，不设置 Electron 的 RUN_AS_NODE。
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	return func() { stopProcessGroup(cmd, done) }, nil
}
