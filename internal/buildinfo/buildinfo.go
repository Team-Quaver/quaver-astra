// Package buildinfo 解析当前构建的展示版本号。优先级：
//
//  1. 构建期注入的值（发布 CI 用 -X main.version，取自 git tag）
//  2. 源码检出目录里的 git tag（本地 go build / go run）
//  3. Go build info 里的 VCS 修订（短 hash）
//  4. 兜底 "dev"
//
// 只有拿不到注入值时才会真的去执行 git，因此正式发行包的启动路径不会
// 触碰 git，也就不依赖用户机器是否装了 git 或是否保留 .git。
package buildinfo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// DevVersion 是拿不到任何版本信息时的占位。
const DevVersion = "dev"

// walkDepth 限制向上查找 .git 的层数，避免异常路径下扫遍整棵树。
const walkDepth = 12

var (
	once     sync.Once
	fallback string
)

// Resolve 返回展示用版本号。injected 是构建期注入的值（main.version）。
func Resolve(injected string) string {
	if v := normalize(injected); v != "" {
		return v
	}
	once.Do(func() { fallback = discover() })
	if v := normalize(fallback); v != "" {
		return v
	}
	return DevVersion
}

// normalize 清理版本串：去空白、剥掉单个前导 v/V，把 dev/空值视作“未知”。
func normalize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s == DevVersion {
		return ""
	}
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	return strings.TrimSpace(s)
}

// discover 在没有注入值时尽力从 git/BuildInfo 里还原版本。
func discover() string {
	if tag := gitTag(); tag != "" {
		return tag
	}
	return vcsRevision()
}

// gitTag 读取最近可达的版本 tag，口径与发布 CI 的 git describe 一致。
func gitTag() string {
	dir := repoDir()
	if dir == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "describe",
		"--tags", "--match", "v[0-9]*", "--match", "V[0-9]*", "--abbrev=0").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// repoDir 从工作目录（再退到可执行文件所在目录）向上寻找含 .git 的目录。
func repoDir() string {
	if cwd, err := os.Getwd(); err == nil {
		if dir := walkUp(cwd); dir != "" {
			return dir
		}
	}
	if exe, err := os.Executable(); err == nil {
		if dir := walkUp(filepath.Dir(exe)); dir != "" {
			return dir
		}
	}
	return ""
}

func walkUp(dir string) string {
	for i := 0; i < walkDepth; i++ {
		if dir == "" {
			return ""
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}

// vcsRevision 取 Go 在 git 仓库里构建时写入的修订号（短 hash）。
func vcsRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && s.Value != "" {
			if len(s.Value) > 12 {
				return s.Value[:12]
			}
			return s.Value
		}
	}
	return ""
}
