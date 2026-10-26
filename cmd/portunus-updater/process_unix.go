//go:build darwin || linux

package main

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// 内核锁随进程退出自动释放，超时或强制终止不会遗留阻塞重试的目录锁。
// 不 unlink 锁文件，避免两个进程锁住不同 inode 后同时替换应用。
func acquireInstallLock(path string) (func(), error) {
	// flock 不要求可写 FD。空锁文件允许其他安装目录授权账号读取，
	// 避免一次更新后文件归属某个账号，阻断其他账号的后续更新。
	flags := syscall.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_CLOEXEC
	fd, err := syscall.Open(path, flags|syscall.O_CREAT|syscall.O_EXCL, 0644)
	created := err == nil
	if err == syscall.EEXIST {
		fd, err = syscall.Open(path, flags, 0)
	}
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if created {
		if err = file.Chmod(0644); err != nil {
			file.Close()
			return nil, err
		}
	}
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, err
	}
	return func() { syscall.Flock(fd, syscall.LOCK_UN); file.Close() }, nil
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
func setupProcessGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func stopProcessGroup(cmd *exec.Cmd, done <-chan struct{}) {
	syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
	// 后端与 renderer 也在独立进程组中；主进程已退出仍清理该组。
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
