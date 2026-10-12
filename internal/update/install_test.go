package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestSwapExecutable(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "new.bin")
	dst := filepath.Join(dir, "app")
	if err := os.WriteFile(src, []byte("new-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old-content"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := swapExecutable(src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new-content" {
		t.Fatalf("content = %q", got)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("dst should remain executable, mode = %v", info.Mode())
	}
}

func TestExtractTarGzBinary(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "pkg.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	payload := []byte("#!/bin/sh\necho hi\n")
	if err := tw.WriteHeader(&tar.Header{Name: "pkg/quaver-astra", Mode: 0o755, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	_ = tw.WriteHeader(&tar.Header{Name: "pkg/README.md", Mode: 0o644, Size: 3, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("hi\n"))
	tw.Close()
	gz.Close()
	f.Close()

	bin, err := extractTarGzBinary(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(bin)
	got, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("bin content = %q", got)
	}
}

func TestApplyWindowsZip(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "pkg.zip")
	zf, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	addZipFile(t, zw, "quaver-astra-1.2.3-windows-amd64/quaver-astra.exe", "new-exe")
	addZipFile(t, zw, "quaver-astra-1.2.3-windows-amd64/mpv/mpv.exe", "mpv")
	addZipFile(t, zw, "quaver-astra-1.2.3-windows-amd64/README.md", "readme")
	zw.Close()
	zf.Close()

	// 模拟已有的便携目录。
	install := filepath.Join(dir, "install")
	if err := os.MkdirAll(filepath.Join(install, "mpv"), 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(install, "quaver-astra.exe")
	if err := os.WriteFile(exe, []byte("old-exe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(install, "mpv", "mpv.exe"), []byte("old-mpv"), 0o755); err != nil {
		t.Fatal(err)
	}

	a := Asset{Name: "Quaver-Astra-1.2.3-windows-amd64.zip"}
	if err := Apply(FormWindowsZip, exe, archive, a); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new-exe" {
		t.Fatalf("exe content = %q", got)
	}
	mpv, err := os.ReadFile(filepath.Join(install, "mpv", "mpv.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if string(mpv) != "mpv" {
		t.Fatalf("mpv content = %q", mpv)
	}
}

func TestApplyUnsupportedReturnsManual(t *testing.T) {
	err := Apply(FormSystemPackage, "/usr/bin/quaver-astra", "/tmp/x", Asset{Name: "x.deb"})
	if err != ErrManualInstall {
		t.Fatalf("err = %v, want ErrManualInstall", err)
	}
}

func TestZipSlipRejected(t *testing.T) {
	dest := t.TempDir()
	if _, err := zipSafeJoin(dest, "../evil"); err == nil {
		t.Fatal("expected zip slip to be rejected")
	}
	if _, err := zipSafeJoin(dest, "ok/file"); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func addZipFile(t *testing.T, zw *zip.Writer, name, content string) {
	t.Helper()
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
}
