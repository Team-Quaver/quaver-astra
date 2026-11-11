//go:build linux

package smedia

import (
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestMPRISMetadataIncludesLyrics(t *testing.T) {
	m := &mpris{snap: Snapshot{
		HasTrack: true,
		TrackID:  "track-1",
		Title:    "晴天",
		Duration: 269.3,
		Lyrics:   "[00:01.00]故事的小黄花",
	}}

	md := m.metadata()
	got, ok := md["xesam:asText"]
	if !ok {
		t.Fatal("Metadata 缺少 xesam:asText")
	}
	if text, _ := got.Value().(string); text != m.snap.Lyrics {
		t.Fatalf("xesam:asText = %q，期望 %q", text, m.snap.Lyrics)
	}
}

func TestMPRISMetadataOmitsEmptyLyrics(t *testing.T) {
	m := &mpris{snap: Snapshot{HasTrack: true, TrackID: "track-1", Title: "晴天"}}
	if _, ok := m.metadata()["xesam:asText"]; ok {
		t.Fatal("无歌词时不应上总线 xesam:asText")
	}
}

func TestMPRISStateSignatureTracksLyrics(t *testing.T) {
	m := &mpris{snap: Snapshot{HasTrack: true, TrackID: "track-1", Lyrics: "a"}}
	before := m.stateSig()
	m.snap.Lyrics = "b"
	if after := m.stateSig(); after == before {
		t.Fatal("歌词变化没有进入状态签名，PropertiesChanged 会漏发")
	}
}

// TestMPRISTrackPathEscapesCloudMid 守住网盘曲目上报：Mid 里的冒号不是合法
// object path 字符，不转义会让整条 Metadata（含 xesam:asText 歌词）发送失败。
func TestMPRISTrackPathEscapesCloudMid(t *testing.T) {
	ids := []string{"cloud:" + strings.Repeat("a", 40), "cloud_a", "cloud:61", "cloud:a_b"}
	seen := make(map[dbus.ObjectPath]string, len(ids))
	for _, id := range ids {
		m := &mpris{snap: Snapshot{HasTrack: true, TrackID: id, Lyrics: "[00:01.00]歌词"}}
		path := m.trackPath()
		if !path.IsValid() {
			t.Fatalf("TrackID %q 生成了非法 object path %q", id, path)
		}
		if prev, ok := seen[path]; ok {
			t.Fatalf("TrackID %q 与 %q 撞路径 %q", id, prev, path)
		}
		seen[path] = id
		if _, ok := m.metadata()["xesam:asText"]; !ok {
			t.Fatalf("TrackID %q 的歌词没有进入 Metadata", id)
		}
	}
}
