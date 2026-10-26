package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

func run() error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("此安装器仅支持 macOS")
	}
	prepareMode := flag.Bool("prepare", false, "准备更新")
	installRoot := flag.String("install", "", "安装事务目录")
	archive := flag.String("archive", "", "ZIP 路径")
	signature := flag.String("signature", "", "签名路径")
	target := flag.String("target", "", "现有应用路径")
	version := flag.String("version", "", "新版本")
	current := flag.String("current", "", "现有版本")
	parent := flag.Int("parent", 0, "父进程 PID")
	flag.Parse()
	if *prepareMode {
		root, err := prepare(*archive, *signature, *target, *version, *current)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"root": root})
	}
	if *installRoot == "" {
		return fmt.Errorf("缺少操作参数")
	}
	if _, err := readJob(*installRoot); err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(*installRoot, "install.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	// 子应用的输出与失败诊断都保留在本次事务目录。
	os.Stderr = log
	os.Setenv("PORTUNUS_INSTALL_ROOT", *installRoot)
	err = install(*installRoot, *parent, installOptions{30 * time.Second, 90 * time.Second, processAlive, launchApp})
	if err != nil {
		data, _ := json.Marshal(map[string]string{"status": "failed", "error": err.Error()})
		os.WriteFile(filepath.Join(*installRoot, "result.json"), data, 0600)
		fmt.Fprintln(log, err)
	}
	return err
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
