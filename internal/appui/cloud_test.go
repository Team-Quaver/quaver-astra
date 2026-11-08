package appui

import (
	"encoding/json"
	"testing"

	"github.com/Team-Quaver/quaver-astra/internal/backend"
	"github.com/Team-Quaver/quaver-astra/internal/conf"
	"github.com/Team-Quaver/quaver-astra/internal/player"

	"github.com/egoist/mygo/ui"
)

// TestCloudSongConversion 锁住网盘条目 → 播放器曲目的映射：取流需要的
// file_id/原始文件名、歌词/封面需要的 key，以及从匹配到的 QQ 曲目继承
// 曲长、歌手与专辑。
func TestCloudSongConversion(t *testing.T) {
	it := backend.CloudItem{
		Key: "k", FileID: "f", FileName: "a b.flac",
		Title: "标题", Artist: "歌手", Album: "专辑",
		HasCover: true, Playable: true,
		Song: &backend.Song{
			Mid: "qm", Interval: 180, Type: 3, ID: json.Number("42"),
			Singer: []backend.SingerRef{{ID: 7, Mid: "sm", Name: "歌手"}},
			Album:  &backend.AlbumRef{Mid: "am", Pmid: "ap", Name: "专辑"},
		},
	}
	s := cloudSong(it)
	if s.Mid != "cloud:k" || s.CloudFileID != "f" || s.CloudKey != "k" || s.CloudName != "a b.flac" {
		t.Fatalf("cloud 字段错误: %+v", s)
	}
	if !s.CloudHasCover || s.Interval != 180 || s.SongID != 42 || s.SongType != 3 {
		t.Fatalf("曲目字段错误: %+v", s)
	}
	if s.Artists != "歌手" || s.Album != "专辑" || s.AlbumMid != "am" || s.AlbumPmid != "ap" {
		t.Fatalf("展示字段错误: %+v", s)
	}
	if len(s.Singers) != 1 || s.Singers[0].Mid != "sm" {
		t.Fatalf("歌手列表错误: %+v", s.Singers)
	}
}

// TestCloudSongFallsBackToFileName 上游没匹配到曲库时，标题回退到文件名，
// 歌手回退为未知，曲长保持 0（由引擎载入后回报）。
func TestCloudSongFallsBackToFileName(t *testing.T) {
	s := cloudSong(backend.CloudItem{Key: "k", FileID: "f", FileName: "untitled.flac"})
	if s.Title != "untitled.flac" || s.Artists != "未知歌手" {
		t.Fatalf("回退字段错误: %+v", s)
	}
}

// TestImportedCoverOverridesStaleHasCover 守住导入封面后的即时生效：
// 库列表刷新前 HasCover 仍是旧值，当前播放曲目也应改用网盘封面接口。
func TestImportedCoverOverridesStaleHasCover(t *testing.T) {
	app := &App{API: backend.NewClient("http://example.test")}
	song := player.Song{CloudFileID: "f", CloudKey: "k"}
	if got := app.songCoverURL(song, 300); got != "" {
		t.Fatalf("导入前封面地址 = %q，期望空（没有专辑封面）", got)
	}
	app.noteCloudCoverImported("k")
	if got, want := app.songCoverURL(song, 300), "http://example.test/cloud/cover/k"; got != want {
		t.Fatalf("导入后封面地址 = %q，期望 %q", got, want)
	}
	it := backend.CloudItem{Key: "k", FileID: "f"}
	if got, want := app.cloudCoverURL(it, 300), "http://example.test/cloud/cover/k"; got != want {
		t.Fatalf("列表封面地址 = %q，期望 %q", got, want)
	}
}

// TestCloudViewRendersSignedOut 网盘页在未登录时只展示提示，不触网。
func TestCloudViewRendersSignedOut(t *testing.T) {
	p := player.New(nil, nilEngine{}, &nullPrefs{})
	app := &App{
		PL:            p,
		Conf:          conf.Open(t.TempDir()),
		Router:        ui.NewRouter("/cloud"),
		Covers:        NewCoverCache(),
		playlist:      map[int64]*playlistState{},
		lastLyricLine: -2,
		cloud:         cloudState{loaded: true},
	}
	tester := ui.NewTester(app.View, testWinW, testWinH)
	mustFind(t, tester, "音乐网盘")
	mustFind(t, tester, "登录 QQ 音乐后才能读取网盘列表。")
}
