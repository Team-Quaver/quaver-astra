package backend

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
