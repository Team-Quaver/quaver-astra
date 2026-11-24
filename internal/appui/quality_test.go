package appui

import (
	"reflect"
	"testing"

	"github.com/Team-Quaver/quaver-astra/internal/player"
)

func TestTierLabelsFiltersByAvailableTiers(t *testing.T) {
	tt := &player.TierTable{Tiers: []player.Tier{
		{ID: "128", Label: "标准音质", Rank: 10},
		{ID: "320", Label: "高品质 HQ", Rank: 20},
		{ID: "flac", Label: "无损 SQ", Rank: 40},
		{ID: "master", Label: "臻品母带", Rank: 60},
	}}

	ids, labels := tierLabels(tt, []string{"128", "flac"})
	if want := []string{"128", "flac"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	if want := []string{"128 标准音质", "flac 无损 SQ"}; !reflect.DeepEqual(labels, want) {
		t.Fatalf("labels = %v, want %v", labels, want)
	}

	// nil 表示详情未就位/请求失败：退回完整档位表。
	ids, _ = tierLabels(tt, nil)
	if want := []string{"128", "320", "flac", "master"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("fallback ids = %v, want %v", ids, want)
	}

	// 详情确认没有已知档位时为空。
	ids, _ = tierLabels(tt, []string{})
	if len(ids) != 0 {
		t.Fatalf("empty ids = %v, want none", ids)
	}

	// 档位表未加载时用内置的常见档位兜底。
	ids, labels = tierLabels(nil, nil)
	if want := []string{"flac", "320", "128"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("nil table ids = %v, want %v", ids, want)
	}
	if want := []string{"flac FLAC 无损", "320 320K MP3", "128 128K MP3"}; !reflect.DeepEqual(labels, want) {
		t.Fatalf("nil table labels = %v, want %v", labels, want)
	}
}

// TestPlayingQualityTierPrefersStream 锁住菜单高亮语义：降档后高亮实际播出
// 的档位，而不是用户请求/默认档位。
func TestPlayingQualityTierPrefersStream(t *testing.T) {
	st := &player.StreamInfo{Tier: "flac", Degraded: true}
	if got := playingQualityTier("master", st); got != "flac" {
		t.Fatalf("playingQualityTier = %q, want flac", got)
	}
	if got := playingQualityTier("master", nil); got != "master" {
		t.Fatalf("playingQualityTier without stream = %q, want master", got)
	}
	if got := playingQualityTier("320", &player.StreamInfo{}); got != "320" {
		t.Fatalf("playingQualityTier with empty tier = %q, want 320", got)
	}
}
