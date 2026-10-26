package main

import (
	"archive/zip"
	"debug/macho"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

func extractArchive(archive, dest string) error {
	var err error
	dest, err = filepath.EvalSymlinks(dest)
	if err != nil {
		return err
	}
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()
	if len(r.File) > 20000 {
		return fmt.Errorf("ZIP 文件条目过多")
	}
	seen := map[string]bool{}
	var expanded uint64
	var links []*zip.File
	for _, f := range r.File {
		name := strings.TrimSuffix(f.Name, "/")
		clean := filepath.Clean(name)
		if name == "" || clean != name || strings.ContainsAny(name, "\\\x00\r\n") || !within(dest, filepath.Join(dest, name)) || (name != "Portunus.app" && !strings.HasPrefix(name, "Portunus.app/")) || seen[name] {
			return fmt.Errorf("ZIP 路径无效: %q", name)
		}
		seen[name] = true
		if f.UncompressedSize64 > 2<<30 || expanded > (2<<30)-f.UncompressedSize64 {
			return fmt.Errorf("ZIP 解压大小超限")
		}
		expanded += f.UncompressedSize64
		target := filepath.Join(dest, name)
		if f.Mode()&os.ModeSymlink != 0 {
			links = append(links, f)
			continue
		}
		if f.FileInfo().IsDir() {
			if err = os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if !f.Mode().IsRegular() {
			return fmt.Errorf("ZIP 文件类型不支持")
		}
		if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		mode := os.FileMode(0644)
		if f.Mode()&0111 != 0 {
			mode = 0755
		}
		out, e := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if e != nil {
			return e
		}
		input, e := f.Open()
		if e != nil {
			out.Close()
			return e
		}
		_, e = io.Copy(out, io.LimitReader(input, int64(f.UncompressedSize64)+1))
		input.Close()
		closeErr := out.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
	}
	// 先写普通文件，最后创建链接，避免通过链接向解压目录外写文件。
	root := filepath.Join(dest, "Portunus.app")
	for _, f := range links {
		input, e := f.Open()
		if e != nil {
			return e
		}
		data, e := io.ReadAll(io.LimitReader(input, 4097))
		input.Close()
		if e != nil {
			return e
		}
		link := string(data)
		target := filepath.Join(dest, f.Name)
		if len(data) > 4096 || link == "" || filepath.IsAbs(link) || strings.ContainsAny(link, "\\\x00\r\n") || strings.Contains("/"+link+"/", "/../") || !within(root, filepath.Join(filepath.Dir(target), link)) {
			return fmt.Errorf("ZIP 符号链接越界")
		}
		if e = os.MkdirAll(filepath.Dir(target), 0755); e != nil {
			return e
		}
		if e = os.Symlink(link, target); e != nil {
			return e
		}
	}
	for _, f := range links {
		target, e := filepath.EvalSymlinks(filepath.Join(dest, f.Name))
		if e != nil || !within(root, target) {
			return fmt.Errorf("ZIP 链接目标无效")
		}
	}
	return nil
}

func command(name string, args ...string) error {
	output, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %.2000s", filepath.Base(name), err, output)
	}
	return nil
}

func plistValue(app, key string) (string, error) {
	out, err := exec.Command("/usr/libexec/PlistBuddy", "-c", "Print :"+key, filepath.Join(app, "Contents/Info.plist")).Output()
	return strings.TrimSpace(string(out)), err
}

func validateBundle(app, version string) error {
	id, err := plistValue(app, "CFBundleIdentifier")
	if err != nil || id != bundleID {
		return fmt.Errorf("应用标识不匹配")
	}
	v, err := plistValue(app, "CFBundleShortVersionString")
	if err != nil || v != version {
		return fmt.Errorf("应用版本不匹配")
	}
	executable, err := plistValue(app, "CFBundleExecutable")
	if err != nil || executable == "" || filepath.Base(executable) != executable {
		return fmt.Errorf("应用可执行文件无效")
	}
	path := filepath.Join(app, "Contents/MacOS", executable)
	f, err := macho.Open(path)
	if err != nil {
		return fmt.Errorf("应用可执行架构无效: %w", err)
	}
	defer f.Close()
	cpu := macho.CpuArm64
	if runtime.GOARCH == "amd64" {
		cpu = macho.CpuAmd64
	}
	if f.Cpu != cpu {
		return fmt.Errorf("应用 CPU 架构不匹配")
	}
	return nil
}
