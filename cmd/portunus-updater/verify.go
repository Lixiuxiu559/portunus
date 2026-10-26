package main

import (
	"crypto/ed25519"
	"crypto/sha512"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

//go:embed public-key.pem
var publicKeyPEM []byte

const bundleID = "com.portunus.app"
const maxArchiveSize = 1 << 30

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type signatureInfo struct {
	Version   string `json:"version"`
	Arch      string `json:"arch"`
	Size      int64  `json:"size"`
	SHA512    string `json:"sha512"`
	Signature string `json:"signature"`
}

func signaturePayload(s signatureInfo) []byte {
	return []byte(fmt.Sprintf("portunus-mac-update-v1\n%s\n%s\n%s\n%d\n%s\n", bundleID, s.Version, s.Arch, s.Size, s.SHA512))
}

func newer(next, current string) bool {
	if !versionPattern.MatchString(next) || !versionPattern.MatchString(current) {
		return false
	}
	a, b := strings.Split(next, "."), strings.Split(current, ".")
	for i := range a {
		x, e1 := strconv.ParseUint(a[i], 10, 64)
		y, e2 := strconv.ParseUint(b[i], 10, 64)
		if e1 != nil || e2 != nil {
			return false
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func verifyArchive(archive, signature, version, current string, keyPEM []byte) (signatureInfo, error) {
	var info signatureInfo
	data, err := os.ReadFile(signature)
	if err != nil {
		return info, err
	}
	if len(data) > 16384 {
		return info, fmt.Errorf("签名文件过大")
	}
	if err = json.Unmarshal(data, &info); err != nil {
		return info, err
	}
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	if info.Version != version || !newer(version, current) || info.Arch != arch || info.Size <= 0 || info.Size > maxArchiveSize {
		return info, fmt.Errorf("更新版本、架构或大小不符合要求")
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return info, fmt.Errorf("更新公钥无效")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return info, err
	}
	pub, ok := key.(ed25519.PublicKey)
	if !ok {
		return info, fmt.Errorf("更新公钥类型错误")
	}
	sig, err := base64.StdEncoding.DecodeString(info.Signature)
	if err != nil || !ed25519.Verify(pub, signaturePayload(info), sig) {
		return info, fmt.Errorf("更新包签名校验失败")
	}
	file, err := os.Open(archive)
	if err != nil {
		return info, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return info, err
	}
	if !stat.Mode().IsRegular() || stat.Size() != info.Size {
		return info, fmt.Errorf("更新包大小不匹配")
	}
	digest := sha512.New()
	if _, err = io.Copy(digest, file); err != nil {
		return info, err
	}
	if base64.StdEncoding.EncodeToString(digest.Sum(nil)) != info.SHA512 {
		return info, fmt.Errorf("更新包内容校验失败")
	}
	return info, nil
}
