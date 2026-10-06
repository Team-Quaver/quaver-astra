package backend

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Team-Quaver/quaver-astra/internal/vault"
)

// fakeCred 是能通过 HasLogin()（musicid>0 && musickey!=""）的最小凭证。
const fakeCred = `{"musicid":12345,"musickey":"Q_H_L_ fake-key","encrypt_uin":"oXXX","musickey_create_time":1,"key_expires_in":259200}`

func startWithVault(t *testing.T, credJSON string) (*Server, *vault.Vault, string) {
	t.Helper()
	dir := t.TempDir()
	v := vault.Open(dir)
	if credJSON != "" {
		if err := v.Save([]byte(credJSON)); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Start(dir, v)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)
	return s, v, dir
}

func getStatus(t *testing.T, base string) map[string]any {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(base + "/login/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var env struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("bad envelope: %s", raw)
	}
	if env.Code != 0 {
		t.Fatalf("code=%d body=%s", env.Code, raw)
	}
	return env.Data
}

func waitLoggedIn(t *testing.T, s *Server, want bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		st := getStatus(t, s.BaseURL())
		if st["logged_in"] == want {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("logged_in never became %v", want)
	return nil
}

// TestCredentialHandoffRestore 保存的凭证经 QCRED1 注入 → 服务端认成已登录。
func TestCredentialHandoffRestore(t *testing.T) {
	s, v, dir := startWithVault(t, fakeCred)
	st := waitLoggedIn(t, s, true)
	if st["credential_mode"] != "external" {
		t.Errorf("credential_mode = %v, want external", st["credential_mode"])
	}
	if !v.HasSaved() {
		t.Error("vault should still hold the credential")
	}
	// device.json 应落在我们的配置目录
	if _, err := os.Stat(filepath.Join(dir, "device.json")); err != nil {
		t.Errorf("device.json not in config dir: %v", err)
	}
}

// TestCredentialHandoffFresh 无保存凭证 → 未登录（external 空注入）。
func TestCredentialHandoffFresh(t *testing.T) {
	s, _, _ := startWithVault(t, "")
	waitLoggedIn(t, s, false)
}

// TestCredentialHandoffLogout 登出 → 后端回写 QCRED1 null → vault 文件被清。
// 上游注销会失败（假凭证），后端按"仅清除本地凭证"继续——正是要测的路径。
func TestCredentialHandoffLogout(t *testing.T) {
	s, v, _ := startWithVault(t, fakeCred)
	waitLoggedIn(t, s, true)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Post(s.BaseURL()+"/login/logout", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !v.HasSaved() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("vault file should be cleared after logout")
}
