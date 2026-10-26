//go:build !darwin && !linux

package main

import (
	"fmt"
	"os/exec"
)

func acquireInstallLock(path string) (func(), error) { return nil, fmt.Errorf("不支持此平台") }

func processAlive(pid int) bool                            { return false }
func setupProcessGroup(cmd *exec.Cmd)                      {}
func stopProcessGroup(cmd *exec.Cmd, done <-chan struct{}) { cmd.Process.Kill() }
