package player

import (
	"net/http"
	"testing"
	"time"
)

type httpError struct{ status int }

func (e httpError) Error() string   { return "http error" }
func (e httpError) HTTPStatus() int { return e.status }

// TestListenReportOncePerSong 守住音响力的时间口径：真实出声才累计，
// 整曲只在切歌时提交一次（分片上报会被上游记成多条听歌记录）；
// 暂停不结算，续播接着累计；恢复会话没有 token 就不上报。
func TestListenReportOncePerSong(t *testing.T) {
	p, _, be := newRacePlayer(t)
	// newRacePlayer 默认走真实开关；这里显式打开，避免未来夹具改动影响断言。
	p.conf.Set("Playing.ReportListenTime", true)
	p.PlayList([]Song{
		{Mid: "a", SongID: 1, SongType: 1, Interval: 130},
		{Mid: "b", SongID: 2, SongType: 1, Interval: 130},
	}, 0)
	wait(t, func() bool { return !p.Loading() && p.PlayingAudio() })

	// 65 秒出声：采样每次最多吃 5 秒，按同样步长推进 13 次，模拟真实心跳。
	for range 13 {
		p.mu.Lock()
		p.listenLast = p.listenLast.Add(-5 * time.Second)
		p.mu.Unlock()
		p.listenTick()
	}
	be.mu.Lock()
	n := len(be.listenReports)
	be.mu.Unlock()
	if n != 0 {
		t.Fatalf("整曲播放期间不应上报，已报 %d 条", n)
	}

	// 切歌：整曲 65 秒一次性归到旧歌上。
	p.PlayList([]Song{{Mid: "b", SongID: 2, SongType: 1, Interval: 130}}, 0)
	wait(t, func() bool {
		be.mu.Lock()
		defer be.mu.Unlock()
		return len(be.listenReports) == 1
	})
	be.mu.Lock()
	whole := be.listenReports[0]
	be.mu.Unlock()
	if whole.SongID != 1 || whole.Seconds != 65 || whole.StreamToken == "" {
		t.Fatalf("整曲应一次报完且归属旧歌：%+v", whole)
	}
	if whole.AudioTimeMS != 130000 {
		t.Fatalf("上报目标不完整：%+v", whole)
	}

	// 暂停后不再累计，也不会凭空多出第二条。
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
	n = len(be.listenReports)
	be.mu.Unlock()
	if n != 1 {
		t.Fatalf("暂停期间不应继续累计，报告数 %d", n)
	}
}

// TestListenReportPauseResumeKeepsOneRecord 暂停再续播仍是同一次收听：
// 累计不清零，切歌时只提交一条。
func TestListenReportPauseResumeKeepsOneRecord(t *testing.T) {
	p, _, be := newRacePlayer(t)
	p.conf.Set("Playing.ReportListenTime", true)
	p.PlayList([]Song{
		{Mid: "a", SongID: 1, SongType: 1, Interval: 300},
		{Mid: "b", SongID: 2, SongType: 1, Interval: 300},
	}, 0)
	wait(t, func() bool { return !p.Loading() && p.PlayingAudio() })

	for range 4 {
		p.mu.Lock()
		p.listenLast = p.listenLast.Add(-5 * time.Second)
		p.mu.Unlock()
		p.listenTick()
	}
	p.Pause()
	wait(t, func() bool { return !p.PlayingAudio() })
	p.Play()
	wait(t, func() bool { return p.PlayingAudio() })
	for range 2 {
		p.mu.Lock()
		p.listenLast = p.listenLast.Add(-5 * time.Second)
		p.mu.Unlock()
		p.listenTick()
	}

	p.PlayList([]Song{{Mid: "b", SongID: 2, SongType: 1, Interval: 300}}, 0)
	wait(t, func() bool {
		be.mu.Lock()
		defer be.mu.Unlock()
		return len(be.listenReports) == 1
	})
	be.mu.Lock()
	got := be.listenReports[0]
	be.mu.Unlock()
	if got.SongID != 1 || got.Seconds != 30 {
		t.Fatalf("暂停/续播应合成一条 30 秒记录：%+v", got)
	}
}

// TestListenReportQualitySwitchKeepsRecord 换音质/换后端带进度重新取流
// 不是新的一次收听：不能把同一首拆成两条记录。
func TestListenReportQualitySwitchKeepsRecord(t *testing.T) {
	p, _, be := newRacePlayer(t)
	p.conf.Set("Playing.ReportListenTime", true)
	p.PlayList([]Song{{Mid: "a", SongID: 1, SongType: 1, Interval: 300}}, 0)
	wait(t, func() bool { return !p.Loading() && p.PlayingAudio() })

	for range 4 {
		p.mu.Lock()
		p.listenLast = p.listenLast.Add(-5 * time.Second)
		p.mu.Unlock()
		p.listenTick()
	}
	p.SwitchQuality("flac")
	wait(t, func() bool {
		p.mu.RLock()
		defer p.mu.RUnlock()
		return !p.Loading() && p.listenTarget.SongID == 1
	})
	for range 2 {
		p.mu.Lock()
		p.listenLast = p.listenLast.Add(-5 * time.Second)
		p.mu.Unlock()
		p.listenTick()
	}
	be.mu.Lock()
	n := len(be.listenReports)
	be.mu.Unlock()
	if n != 0 {
		t.Fatalf("换档不应提前上报，已报 %d 条", n)
	}

	p.PlayList([]Song{{Mid: "b", SongID: 2, SongType: 1, Interval: 300}}, 0)
	wait(t, func() bool {
		be.mu.Lock()
		defer be.mu.Unlock()
		return len(be.listenReports) == 1
	})
	be.mu.Lock()
	got := be.listenReports[0]
	be.mu.Unlock()
	if got.SongID != 1 || got.Seconds != 30 {
		t.Fatalf("换档前后应合成一条 30 秒记录：%+v", got)
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

// TestListenTickSelfReschedules 守住采样心跳会自己续期：AfterFunc 只响一次，
// 若 tick 里不重新排定时器，整曲累计在第一个窗口后就不再增长——整曲一次
// 上报时这等于只记了 1 秒。用真实时间跑两个多窗口来验证。
func TestListenTickSelfReschedules(t *testing.T) {
	if testing.Short() {
		t.Skip("真实等待两个采样窗口")
	}
	p, _, _ := newRacePlayer(t)
	p.conf.Set("Playing.ReportListenTime", true)
	p.PlayList([]Song{{Mid: "a", SongID: 1, SongType: 1, Interval: 300}}, 0)
	wait(t, func() bool { return !p.Loading() && p.PlayingAudio() })

	deadline := time.Now().Add(6 * time.Second)
	for {
		p.mu.RLock()
		seen := p.listenSeen
		p.mu.RUnlock()
		if seen >= 2.2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("6s 内累计只到 %.2fs：采样心跳没有续期", seen)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
