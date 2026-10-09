package appui

import (
	"sync"
	"time"

	"github.com/team-quaver/typhoeus-go/inhibit"
)

// sleepInhibitor 是 Typhoeus inhibit.Inhibitor 的最小接口，便于单测替换。
type sleepInhibitor interface {
	Acquire() (bool, error)
	Release()
}

// sleepBlocker 基于 Typhoeus-go/inhibit 复用三平台睡眠禁止实现：
// Linux xdg portal/logind、Windows PowerRequest、macOS IOKit。它只阻止系统
// 睡眠，不请求 DisplayRequired，因此熄屏与锁屏策略照常生效。
//
// opMu 串行化 Acquire/Release，避免快速的 true→false→true 把新请求误释放；
// mu 只保护短状态。失败后 30 秒再试，避免播放器 5Hz 心跳反复打系统总线。
type sleepBlocker struct {
	opMu      sync.Mutex
	mu        sync.Mutex
	inhibitor sleepInhibitor
	want      bool
	held      bool
	closed    bool
	retryAt   time.Time
}

const (
	sleepInhibitKey        = "Playing.InhibitSleep"
	sleepInhibitRetryDelay = 30 * time.Second
)

func newSleepBlocker(inhibitor sleepInhibitor) *sleepBlocker {
	return &sleepBlocker{inhibitor: inhibitor}
}

// newSystemSleepBlocker 使用随 Typhoeus-go 分发的系统睡眠禁止执行器。
func newSystemSleepBlocker() *sleepBlocker {
	return newSleepBlocker(inhibit.New())
}

// set 对齐期望态：true 至多持有一个睡眠禁止请求，false 立即释放。
func (b *sleepBlocker) set(want bool) {
	if b == nil {
		return
	}
	b.opMu.Lock()
	defer b.opMu.Unlock()

	b.mu.Lock()
	if b.closed && want {
		b.mu.Unlock()
		return
	}
	now := time.Now()
	if want == b.want && (want == false || b.held || now.Before(b.retryAt)) {
		b.mu.Unlock()
		return
	}
	b.want = want
	inhibitor := b.inhibitor
	b.mu.Unlock()

	if inhibitor == nil {
		return
	}
	if !want {
		inhibitor.Release()
		b.mu.Lock()
		b.held = false
		b.retryAt = time.Time{}
		b.mu.Unlock()
		return
	}

	_, err := inhibitor.Acquire()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.held = err == nil
	if err == nil {
		b.retryAt = time.Time{}
		return
	}
	// 保留 want=true：下次通知在退避窗口外重试（Typhoeus Acquire 幂等）。
	b.retryAt = now.Add(sleepInhibitRetryDelay)
}

func (b *sleepBlocker) close() {
	if b == nil {
		return
	}
	b.opMu.Lock()
	defer b.opMu.Unlock()
	b.mu.Lock()
	b.closed = true
	b.want = false
	b.held = false
	b.retryAt = time.Time{}
	inhibitor := b.inhibitor
	b.mu.Unlock()
	if inhibitor != nil {
		inhibitor.Release()
	}
}
