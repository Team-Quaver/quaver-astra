package vault

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundtrip(t *testing.T) {
	dir := t.TempDir()
	v := Open(dir)
	cred := []byte(`{"musicid":123,"musickey":"abc","encrypt_uin":"u1"}`)
	if err := v.Save(cred); err != nil {
		t.Fatal(err)
	}
	got, err := v.Load()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(cred) {
		t.Fatalf("roundtrip mismatch: %s", got)
	}
	if !v.HasSaved() {
		t.Error("HasSaved should be true")
	}
}

func TestMissingFile(t *testing.T) {
	v := Open(t.TempDir())
	if _, err := v.Load(); err != ErrNoCredential {
		t.Fatalf("want ErrNoCredential, got %v", err)
	}
	if v.HasSaved() {
		t.Error("HasSaved should be false")
	}
}

func TestWrongKeyFailsClosed(t *testing.T) {
	dir := t.TempDir()
	v := Open(dir)
	if err := v.Save([]byte(`{"musicid":1}`)); err != nil {
		t.Fatal(err)
	}
	// 换掉密钥 = 相当于换机器/密钥丢失
	if err := os.Remove(filepath.Join(dir, keyFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Load(); err != ErrNoCredential {
		t.Fatalf("stale ciphertext must fail closed, got %v", err)
	}
}

func TestCorruptFailsClosed(t *testing.T) {
	dir := t.TempDir()
	v := Open(dir)
	if err := v.Save([]byte(`{"musicid":1}`)); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, credFile))
	raw[len(raw)-3] ^= 0xff // 破坏密文尾部
	if err := os.WriteFile(filepath.Join(dir, credFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Load(); err != ErrNoCredential {
		t.Fatalf("corrupt ciphertext must fail closed, got %v", err)
	}
}

func TestClear(t *testing.T) {
	dir := t.TempDir()
	v := Open(dir)
	if err := v.Save([]byte(`{"musicid":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := v.Clear(); err != nil {
		t.Fatal(err)
	}
	if v.HasSaved() {
		t.Error("HasSaved should be false after Clear")
	}
	// 重复 Clear 幂等
	if err := v.Clear(); err != nil {
		t.Fatal(err)
	}
	// Save(空) 等价 Clear
	if err := v.Save(nil); err != nil {
		t.Fatal(err)
	}
	if v.HasSaved() {
		t.Error("empty save should clear")
	}
}

func TestKeyPersists(t *testing.T) {
	dir := t.TempDir()
	Open(dir).Save([]byte(`{"musicid":1}`))
	// 同目录重新打开（模拟重启）能解出来
	got, err := Open(dir).Load()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"musicid":1}` {
		t.Fatalf("got %s", got)
	}
	info, err := os.Stat(filepath.Join(dir, keyFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("key perm = %v, want 0600", info.Mode().Perm())
	}
}
