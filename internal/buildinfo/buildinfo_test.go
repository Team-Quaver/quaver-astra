package buildinfo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"":                      "",
		"   ":                   "",
		"dev":                   "",
		" dev ":                 "",
		"v1.2.3":                "1.2.3",
		"V1.2.3":                "1.2.3",
		"1.2.3":                 "1.2.3",
		"  v1.2.3  ":            "1.2.3",
		"1.2.3-abcdef-snapshot": "1.2.3-abcdef-snapshot",
	}
	for in, want := range cases {
		if got := normalize(in); got != want {
			t.Errorf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolvePrefersInjected(t *testing.T) {
	cases := map[string]string{
		"v1.2.3":                "1.2.3",
		"1.2.3":                 "1.2.3",
		"1.2.3-abc123-snapshot": "1.2.3-abc123-snapshot",
	}
	for in, want := range cases {
		if got := Resolve(in); got != want {
			t.Errorf("Resolve(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestResolveFallbackNeverEmpty 断言注入值为 dev/空时也能给出非空版本
// （本包在仓库内测试，通常能从 git tag 或 VCS 修订里拿到值）。
func TestResolveFallbackNeverEmpty(t *testing.T) {
	for _, in := range []string{"", "dev"} {
		got := Resolve(in)
		if got == "" {
			t.Fatalf("Resolve(%q) returned empty", in)
		}
		if strings.HasPrefix(got, "v") || strings.HasPrefix(got, "V") {
			t.Errorf("Resolve(%q) = %q, want no leading v", in, got)
		}
	}
}

func TestWalkUpFindsGitRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := walkUp(nested); got != root {
		t.Fatalf("walkUp(%q) = %q, want %q", nested, got, root)
	}
}

// TestWalkUpDepthLimit 断言在没有 .git 的深目录里，向上查找会被层数上限
// 拦住（避免把宿主环境里无关的 .git——例如 /tmp/.git——误判成仓库根）。
func TestWalkUpDepthLimit(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < walkDepth+4; i++ {
		dir = filepath.Join(dir, "d")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := walkUp(dir); got != "" {
		t.Fatalf("walkUp(deep, no-git) = %q, want empty", got)
	}
}
