package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestLockReleasedAfterProcessKilled(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("unix helper lock")
	}
	if path := os.Getenv("PORTUNUS_LOCK_TEST_CHILD"); path != "" {
		release, err := acquireInstallLock(path)
		if err != nil {
			os.Exit(2)
		}
		defer release()
		os.WriteFile(path+".ready", []byte("ready"), 0600)
		time.Sleep(time.Minute)
		return
	}
	path := filepath.Join(t.TempDir(), "update.lock")
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockReleasedAfterProcessKilled$")
	cmd.Env = append(os.Environ(), "PORTUNUS_LOCK_TEST_CHILD="+path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path + ".ready"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child lock timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if release, err := acquireInstallLock(path); err == nil {
		release()
		t.Fatal("concurrent installer accepted")
	}
	cmd.Process.Kill()
	cmd.Wait()
	// 对其他账号而言，锁文件可能只有读取权限；不应要求写入权限。
	if err := os.Chmod(path, 0444); err != nil {
		t.Fatal(err)
	}
	release, err := acquireInstallLock(path)
	if err != nil {
		t.Fatal("killed helper left stale lock", err)
	}
	release()
}
