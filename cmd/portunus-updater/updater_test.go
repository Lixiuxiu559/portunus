package main

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestVerifySignedArchive(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "update.zip")
	signature := archive + ".sig"
	payload := []byte("trusted release payload")
	os.WriteFile(archive, payload, 0600)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	digest := sha512.Sum512(payload)
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	good := signatureInfo{"0.6.0", arch, int64(len(payload)), base64.StdEncoding.EncodeToString(digest[:]), ""}
	good.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, signaturePayload(good)))
	for _, tc := range []struct {
		name, version, current string
		mutate                 func(*signatureInfo)
		corrupt                bool
		want                   bool
	}{
		{"valid", "0.6.0", "0.5.0", nil, false, true},
		{"wrong version", "0.6.1", "0.5.0", nil, false, false},
		{"downgrade", "0.6.0", "0.7.0", nil, false, false},
		{"same version", "0.6.0", "0.6.0", nil, false, false},
		{"wrong arch", "0.6.0", "0.5.0", func(s *signatureInfo) { s.Arch = "other" }, false, false},
		{"forged metadata", "0.6.0", "0.5.0", func(s *signatureInfo) { s.SHA512 = "forged" }, false, false},
		{"wrong signature", "0.6.0", "0.5.0", func(s *signatureInfo) { s.Signature = base64.StdEncoding.EncodeToString(make([]byte, 64)) }, false, false},
		{"tampered bytes", "0.6.0", "0.5.0", nil, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := good
			if tc.mutate != nil {
				tc.mutate(&info)
			}
			data, _ := json.Marshal(info)
			os.WriteFile(signature, data, 0600)
			bytes := append([]byte(nil), payload...)
			if tc.corrupt {
				bytes[0] ^= 1
			}
			os.WriteFile(archive, bytes, 0600)
			_, err := verifyArchive(archive, signature, tc.version, tc.current, keyPEM)
			if (err == nil) != tc.want {
				t.Fatalf("verified=%v err=%v", err == nil, err)
			}
		})
	}
	os.Remove(signature)
	if _, err := verifyArchive(archive, signature, "0.6.0", "0.5.0", keyPEM); err == nil {
		t.Fatal("missing signature accepted")
	}
}

func makeZIP(t *testing.T, entries []struct {
	name, content string
	mode          os.FileMode
}) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "archive.zip")
	f, _ := os.Create(p)
	w := zip.NewWriter(f)
	for _, entry := range entries {
		h := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		h.SetMode(entry.mode)
		out, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		out.Write([]byte(entry.content))
	}
	w.Close()
	f.Close()
	return p
}

func TestExtractPathsAndFrameworkLinks(t *testing.T) {
	for _, name := range []string{"../outside", "/absolute", "Portunus.app/../outside", "Portunus.app/a\\b", "Portunus.app/a\nb"} {
		t.Run(fmt.Sprintf("reject-%q", name), func(t *testing.T) {
			p := makeZIP(t, []struct {
				name, content string
				mode          os.FileMode
			}{{name, "bad", 0644}})
			if err := extractArchive(p, t.TempDir()); err == nil {
				t.Fatal("unsafe path accepted")
			}
		})
	}
	for _, link := range []string{"../../../outside", "/tmp/outside", "A/../../outside"} {
		t.Run("link-"+link, func(t *testing.T) {
			p := makeZIP(t, []struct {
				name, content string
				mode          os.FileMode
			}{{"Portunus.app/link", link, os.ModeSymlink | 0777}})
			if err := extractArchive(p, t.TempDir()); err == nil {
				t.Fatal("unsafe symlink accepted")
			}
		})
	}
	p := makeZIP(t, []struct {
		name, content string
		mode          os.FileMode
	}{
		{"Portunus.app/Framework/Versions/A/bin", "executable", 0755},
		{"Portunus.app/Framework/Versions/Current", "A", os.ModeSymlink | 0777},
		{"Portunus.app/Framework/bin", "Versions/Current/bin", os.ModeSymlink | 0777},
	})
	dest := t.TempDir()
	if err := extractArchive(p, dest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "Portunus.app/Framework/bin"))
	if err != nil || string(data) != "executable" {
		t.Fatal("framework link damaged", err)
	}
}

func TestReplacementHealthAndRollback(t *testing.T) {
	for _, scenario := range []string{"success", "no confirmation", "wrong token", "launch fails", "rename fails"} {
		t.Run(scenario, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, ".portunus-update-test")
			os.Mkdir(root, 0700)
			target := filepath.Join(parent, "Portunus.app")
			staged := filepath.Join(root, "unpacked/Portunus.app")
			os.MkdirAll(target, 0755)
			os.WriteFile(filepath.Join(target, "version"), []byte("old"), 0644)
			if scenario != "rename fails" {
				os.MkdirAll(staged, 0755)
				os.WriteFile(filepath.Join(staged, "version"), []byte("new"), 0644)
			}
			job := updateJob{target, "0.5.0", "0.6.0", strings.Repeat("a", 64)}
			launches := 0
			stopped := false
			opts := installOptions{healthTimeout: time.Millisecond, launch: func(p string, j updateJob) (func(), error) {
				launches++
				content, _ := os.ReadFile(filepath.Join(p, "version"))
				if launches == 1 && scenario != "rename fails" && string(content) != "new" {
					t.Fatal("did not launch new application")
				}
				if launches == 2 && (string(content) != "old" || j.Token != "") {
					t.Fatal("rollback did not restore old application")
				}
				if scenario == "rename fails" && (string(content) != "old" || j.Token != "") {
					t.Fatal("failed rename did not restart old application")
				}
				if scenario == "success" {
					os.WriteFile(filepath.Join(root, "healthy"), []byte(job.Token), 0600)
				}
				if scenario == "wrong token" {
					os.WriteFile(filepath.Join(root, "healthy"), []byte("wrong"), 0600)
				}
				if scenario == "launch fails" && launches == 1 {
					return nil, fmt.Errorf("launch failed")
				}
				return func() { stopped = true }, nil
			}}
			err := replaceAndLaunch(root, job, opts)
			content, _ := os.ReadFile(filepath.Join(target, "version"))
			if scenario == "success" {
				if err != nil || string(content) != "new" {
					t.Fatal(err, string(content))
				}
				if _, err = os.Stat(filepath.Join(root, "Previous.app")); !os.IsNotExist(err) {
					t.Fatal("backup not cleaned")
				}
			} else {
				if err == nil || string(content) != "old" {
					t.Fatal("old application not preserved", err, string(content))
				}
				if scenario == "no confirmation" || scenario == "wrong token" {
					if !stopped || launches != 2 {
						t.Fatal("new process not stopped or old not restarted")
					}
				}
				if scenario == "rename fails" && launches != 1 {
					t.Fatal("old application not restarted after rename failure")
				}
			}
		})
	}
}
