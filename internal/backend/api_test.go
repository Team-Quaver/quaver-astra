package backend

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestResolveStreamSendsDuration 锁住与 Typhoeus 新版 stream guard 的契约：
// 曲长提示必须随 resolve 一起到达，否则后端只能按 2 分钟兜底限速。
func TestResolveStreamSendsDuration(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stream/resolve" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"token":"t","path":"/stream/t","tier":"flac","tier_label":"FLAC","mime":"audio/flac","size":123}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if _, err := c.ResolveStream("mid", "", 1, "flac", true, nil, 245000, ""); err != nil {
		t.Fatal(err)
	}
	if got["duration_ms"] != float64(245000) {
		t.Fatalf("duration_ms = %#v, want 245000", got["duration_ms"])
	}
	if _, ok := got["cache_seed"]; ok {
		t.Fatalf("cache_seed should be omitted when empty, got %#v", got["cache_seed"])
	}
}

// TestResolveStreamSendsCacheSeed 锁住本地缓存前端半种的上行契约：
// 非空时必须原样随 resolve 交给后端（LocalCache 命中判断与落盘都靠它）。
func TestResolveStreamSendsCacheSeed(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"token":"t","path":"/stream/t","tier":"128","cached":true}}`))
	}))
	defer srv.Close()

	if _, err := NewClient(srv.URL).ResolveStream("mid", "", 1, "auto", true, nil, 0, "c2VlZC1zZWVkLXNlZWQtc2VlZC1zZWVkLXNlZWQ"); err != nil {
		t.Fatal(err)
	}
	if got["cache_seed"] != "c2VlZC1zZWVkLXNlZWQtc2VlZC1zZWVkLXNlZWQ" {
		t.Fatalf("cache_seed = %#v", got["cache_seed"])
	}
}

func TestReportListenTimeSendsSoundPowerBody(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sound-power/report" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode body: %v", err)
		}
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"ok":true,"duration":60}}`))
	}))
	defer srv.Close()

	res, err := NewClient(srv.URL).ReportListenTime(SoundPowerReport{
		StreamToken: "token", SongID: 735018740, Mid: "004Bjrow3AzR86",
		AudioTimeMS: 208000, PlayDurationSecond: 60, SongType: 1,
		SingerID: 50, CoSinger: "测试歌手",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Duration != 60 {
		t.Fatalf("result = %+v", res)
	}
	for k, want := range map[string]any{
		"stream_token": "token", "song_id": float64(735018740), "mid": "004Bjrow3AzR86",
		"audio_time_ms": float64(208000), "play_duration_seconds": float64(60),
		"song_type": float64(1), "singer_id": float64(50), "co_singer": "测试歌手",
	} {
		if got[k] != want {
			t.Errorf("%s = %#v, want %#v", k, got[k], want)
		}
	}
}

func TestEnvelopeDecode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"logged_in": true}})
		case "/err":
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": -401, "msg": "凭证无效"})
		case "/big":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"id": json.Number("8231558328")}})
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL)

	var out struct {
		LoggedIn bool `json:"logged_in"`
	}
	if err := c.getJSON("/ok", nil, &out); err != nil {
		t.Fatal(err)
	}
	if !out.LoggedIn {
		t.Error("logged_in should be true")
	}

	if _, err := c.do(http.MethodGet, "/err", nil); err == nil {
		t.Fatal("expected error")
	} else if !Unauthorized(err) {
		t.Errorf("should be unauthorized: %v", err)
	}

	var big struct {
		ID json.Number `json:"id"`
	}
	if err := c.getJSON("/big", nil, &big); err != nil {
		t.Fatal(err)
	}
	if v, err := big.ID.Int64(); err != nil || v != 8231558328 {
		t.Errorf("big id: %v %v", big.ID, err)
	}
}

func TestHotkeyParse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"vec_hotkey":[{"query":"茶汤 郁可唯"},{"query":"阿楚姑娘"}]}}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	hot, err := c.SearchHotkey()
	if err != nil {
		t.Fatal(err)
	}
	if len(hot) != 2 || hot[0] != "茶汤 郁可唯" {
		t.Errorf("hotkey: %v", hot)
	}
}

func TestLyricRequestsTranslationRomanizationAndQRC(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/song/m1/lyric" {
			t.Errorf("path = %q", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("trans") != "1" || q.Get("roma") != "1" || q.Get("qrc") != "1" {
			t.Errorf("歌词参数 = %v，期望 trans/roma/qrc 全为 1", q)
		}
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"lyric":"[0,1000]君(0,1000)","trans":"[0,1000]你","roma":"[0,1000]kimi"}}`))
	}))
	defer srv.Close()
	l, err := NewClient(srv.URL).Lyric("m1", true, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if l.Trans != "[0,1000]你" || l.Roma != "[0,1000]kimi" {
		t.Fatalf("双辅助行解析错误：%+v", l)
	}
}

func TestDiscoveryHomeResponses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/recommend/ai-songlist":
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"songlists":[{"id":1,"title":"AI"}],"total":9,"hasmore":true}}`))
		case "/top/category":
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"groups":[{"id":1,"name":"精选","toplists":[{"id":4,"title":"热歌榜","update_tips":"每日更新"}]}]}}`))
		case "/top/4/detail":
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"info":{"id":4,"title":"热歌榜","songnum":1},"songs":[{"mid":"m1","name":"歌"}],"total":1,"hasmore":false}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	ai, err := c.RecommendAISonglists(1, 8)
	if err != nil || len(ai.Songlists) != 1 || !ai.HasMore {
		t.Fatalf("AI 歌单响应错误：%+v %v", ai, err)
	}
	top, err := c.ToplistCategories()
	if err != nil || len(top.Groups) != 1 || top.Groups[0].Toplists[0].ID != 4 {
		t.Fatalf("榜单分类响应错误：%+v %v", top, err)
	}
	d, err := c.ToplistDetail(4, 1, 100)
	if err != nil || len(d.Songs) != 1 || d.Info.Title != "热歌榜" {
		t.Fatalf("榜单详情响应错误：%+v %v", d, err)
	}
}
