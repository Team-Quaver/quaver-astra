package appui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Team-Quaver/quaver-astra/internal/backend"

	"github.com/egoist/mygo/ui"
)

func TestSearchHintRendersAndEnterChoosesHighlighted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"song":[],"songlist":[],"total_num":0,"nextpage":0}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	app, tester := testApp(t)
	app.API = backend.NewClient(srv.URL)
	app.searchOpen = true
	app.searchDraft = "周杰伦"
	app.searchHint = searchHintState{
		kw:     "周杰伦",
		items:  []string{"周杰伦 晴天", "周杰伦 发如雪"},
		done:   true,
		active: -1,
	}
	tester.Frame()

	if !tester.HasText("周杰伦 晴天") || !tester.HasText("周杰伦 发如雪") {
		t.Fatalf("搜索建议未渲染；文本：%v", tester.Texts())
	}

	tester.Key(0, ui.KeyDown)
	if app.searchHint.active != 0 {
		t.Fatalf("按下键后高亮 = %d，期望 0", app.searchHint.active)
	}
	tester.Key(0, ui.KeyEnter)

	if app.searchDraft != "周杰伦 晴天" {
		t.Errorf("回车后搜索词 = %q，期望高亮建议", app.searchDraft)
	}
	if app.searchOpen {
		t.Error("回车搜索后浮窗应关闭")
	}
	if got := app.Router.Query("kw"); got != "周杰伦 晴天" {
		t.Errorf("路由关键词 = %q", got)
	}
}
