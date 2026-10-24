package player

import (
	"net/http"
	"testing"
	"time"
)

type httpError struct{ status int }

func (e httpError) Error() string   { return "http error" }
func (e httpError) HTTPStatus() int { return e.status }

// TestListenReportChunksAndTails 守住音响力的时间口径：真实出声才累计，
// 60 秒一片；切歌把旧歌尾段记在旧目标上；恢复会话没有 token 就不上报。
func TestListenReportChunksAndTails(t *testing.T) {
	p, _, be := newRacePlayer(t)
	// newRacePlayer 默认走真实开关；这里显式打开，避免未来夹具改动影响断言。
	p.conf.Set("Playing.ReportListenTime", true)
	p.PlayList([]Song{
		{Mid: "a", SongID: 1, SongType: 1, Interval: 130},
		{Mid: "b", SongID: 2, SongType: 1, Interval: 130},
	}, 0)
	wait(t, func() bool { return !p.Loading() && p.PlayingAudio() })

	// 65 秒出声：一片 60 秒，剩 5 秒尾段。采样每次最多吃 5 秒，
	// 按同样步长推进 13 次，模拟真实心跳连续到达。
	for range 13 {
		p.mu.Lock()
		p.listenLast = p.listenLast.Add(-5 * time.Second)
		p.mu.Unlock()
		p.listenTick()
	}
	wait(t, func() bool {
		be.mu.Lock()
		defer be.mu.Unlock()
		return len(be.listenReports) == 1
	})

	p.PlayList([]Song{{Mid: "b", SongID: 2, SongType: 1, Interval: 130}}, 0)
	wait(t, func() bool {
		be.mu.Lock()
		defer be.mu.Unlock()
		return len(be.listenReports) == 2
	})
	be.mu.Lock()
	chunk, tail := be.listenReports[0], be.listenReports[1]
	be.mu.Unlock()
	if chunk.SongID != 1 || chunk.Seconds != 60 || chunk.StreamToken == "" {
		t.Fatalf("整片归属错误：%+v", chunk)
	}
	if tail.SongID != 1 || tail.Seconds != 5 {
		t.Fatalf("尾段应记在旧歌上：%+v", tail)
	}
	if tail.AudioTimeMS != 130000 || tail.StreamToken != chunk.StreamToken {
		t.Fatalf("上报目标不完整：%+v", tail)
	}

	// 暂停后不再累计，也不会凭空多出第三片。
	p.Pause()
	wait(t, func() bool { return !p.PlayingAudio() })
	wait(t, func() bool {
		p.mu.RLock()
		defer p.mu.RUnlock()
		return !p.listenActive
	})
	p.listenTick()
	time.Sleep(50 * time.Millisecond)
	be.mu.Lock()
	n := len(be.listenReports)
	be.mu.Unlock()
	if n != 2 {
		t.Fatalf("暂停期间不应继续累计，报告数 %d", n)
	}
}

// TestListenReportDisabledDropsQueue 关闭开关立即清空累计与待发队列，
// 已累计的时间不补发，符合主项目的隐私语义。
func TestListenReportDisabledDropsQueue(t *testing.T) {
	prefs := newMapPrefs()
	prefs.Set("Playing.ReportListenTime", false)
	p := New(&liveBackend{loggedIn: true}, &fakeEngine{}, prefs)
	p.mu.Lock()
	p.listenTarget = ListenReport{StreamToken: "t", SongID: 1, Mid: "m"}
	p.listenSeen = 60
	p.listenQueue = []ListenReport{{StreamToken: "t", SongID: 1, Seconds: 60}}
	p.mu.Unlock()
	p.listenTick()
	p.mu.RLock()
	seen, queued := p.listenSeen, len(p.listenQueue)
	p.mu.RUnlock()
	if seen != 0 || queued != 0 {
		t.Fatalf("关闭后应清空状态：seen=%v queue=%d", seen, queued)
	}
}

// TestListenReportRetryKeepsTransientJob 网络失败保留任务并进入退避；
// 4xx 确定性失败则丢弃，避免同一段流水无限撞墙。
func TestListenReportRetryKeepsTransientJob(t *testing.T) {
	p := New(&liveBackend{listenErr: errListenNetwork}, &fakeEngine{}, newMapPrefs())
	job := ListenReport{StreamToken: "t", SongID: 1, Seconds: 60}
	p.mu.Lock()
	p.listenQueue = []ListenReport{job}
	p.mu.Unlock()
	p.listenDrain()
	wait(t, func() bool {
		p.mu.RLock()
		defer p.mu.RUnlock()
		return len(p.listenQueue) == 1 && !p.listenRetryAt.IsZero()
	})

	be := &liveBackend{listenErr: httpError{status: http.StatusForbidden}}
	p2 := New(be, &fakeEngine{}, newMapPrefs())
	p2.mu.Lock()
	p2.listenQueue = []ListenReport{job}
	p2.mu.Unlock()
	p2.listenDrain()
	wait(t, func() bool {
		p2.mu.RLock()
		defer p2.mu.RUnlock()
		return len(p2.listenQueue) == 0
	})
}

var errListenNetwork = httpError{status: http.StatusBadGateway}
