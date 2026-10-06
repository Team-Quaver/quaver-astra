// Package vault 负责登录凭证的本地加密持久化（进程内版 SessionVault）。
//
// 与后端 external 模式配套：凭证由本包独占加密落盘（credential.enc），
// 后端经 QCRED1 管道协议与本包交接，明文凭证绝不落盘。
//
// 文件格式（AEAD 内建认证，encrypt-then-MAC）：
//
//	magic "QAST1"(5B) | nonce(12B) | ChaCha20-Poly1305(plaintext)
//
// 主密钥 32B 随机，存 <config>/session.key（0600）。密钥与密文同目录的
// 安全边界与 QML 版一致：防的是他人随意拷贝文件，不防本机 root；
// 后续可无缝升级到 OS 密钥环（只换 LoadKey 的实现）。
package vault

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	magic    = "QAST1"
	credFile = "credential.enc"
	keyFile  = "session.key"
	keyLen   = 32
	nonceLen = 12
)

// ErrNoCredential 表示没有可用凭证（文件缺失或校验失败，一律按未登录）。
var ErrNoCredential = errors.New("vault: 没有可用凭证")

// Vault 管理配置目录下的加密凭证。
type Vault struct {
	dir string
}

// Open 打开 vault（目录不存在会创建）。
func Open(dir string) *Vault {
	_ = os.MkdirAll(dir, 0o700)
	return &Vault{dir: dir}
}

func (v *Vault) credPath() string { return filepath.Join(v.dir, credFile) }
func (v *Vault) keyPath() string  { return filepath.Join(v.dir, keyFile) }

// loadKey 读主密钥；没有则生成并落盘（0600）。
func (v *Vault) loadKey() ([]byte, error) {
	if key, err := os.ReadFile(v.keyPath()); err == nil && len(key) == keyLen {
		return key, nil
	}
	key := make([]byte, keyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("生成主密钥失败: %w", err)
	}
	if err := os.WriteFile(v.keyPath(), key, 0o600); err != nil {
		return nil, fmt.Errorf("保存主密钥失败: %w", err)
	}
	return key, nil
}

// Save 加密并原子落盘一份凭证 JSON。
func (v *Vault) Save(credJSON []byte) error {
	if len(credJSON) == 0 {
		return v.Clear()
	}
	key, err := v.loadKey()
	if err != nil {
		return err
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return err
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	sealed := aead.Seal(nil, nonce, credJSON, []byte(magic))

	out := make([]byte, 0, len(magic)+nonceLen+len(sealed))
	out = append(out, magic...)
	out = append(out, nonce...)
	out = append(out, sealed...)

	tmp := v.credPath() + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, v.credPath())
}

// Load 读出凭证 JSON；缺失、密钥不匹配或密文损坏一律 ErrNoCredential（fail-closed）。
func (v *Vault) Load() ([]byte, error) {
	raw, err := os.ReadFile(v.credPath())
	if err != nil {
		return nil, ErrNoCredential
	}
	if len(raw) < len(magic)+nonceLen+16 || string(raw[:len(magic)]) != magic {
		return nil, ErrNoCredential
	}
	key, err := v.loadKey()
	if err != nil {
		return nil, ErrNoCredential
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, ErrNoCredential
	}
	nonce := raw[len(magic) : len(magic)+nonceLen]
	sealed := raw[len(magic)+nonceLen:]
	plain, err := aead.Open(nil, nonce, sealed, []byte(magic))
	if err != nil {
		return nil, ErrNoCredential
	}
	return plain, nil
}

// Clear 删除已保存的凭证（登出时由后端 QCRED1 null 触发）。
func (v *Vault) Clear() error {
	err := os.Remove(v.credPath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// HasSaved 报告是否存有凭证文件。
func (v *Vault) HasSaved() bool {
	_, err := os.Stat(v.credPath())
	return err == nil
}
