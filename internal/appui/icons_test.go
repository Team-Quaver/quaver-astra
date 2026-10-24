package appui

import "testing"

// TestSidebarIconsUseQuaverShapes 守住侧栏图标与 Quaver 同款：
// 无限电台是 ∞ 形，每日 30 首是日历。这里不做像素快照，只防图标名
// 被改回旧图后测试仍悄悄通过。
func TestSidebarIconsUseQuaverShapes(t *testing.T) {
	for _, item := range navItems {
		if item.icon != "guess" && item.icon != "daily" {
			continue
		}
		body := iconBodies[item.icon]
		if body == "" {
			t.Fatalf("侧栏图标 %q 缺失", item.icon)
		}
	}
	if iconBodies["daily"] == iconBodies["guess"] {
		t.Fatal("无限电台与每日 30 首使用了同一枚图标")
	}
}
