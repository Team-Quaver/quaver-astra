package player

import "testing"

// rateEngine 给 fakeEngine 补上 Liino 的自适应采样率能力，用于验证
// Player 的可选接口转发；普通 fakeEngine 则代表不支持该能力的 mpv。
type rateEngine struct {
	*fakeEngine
	adaptive     bool
	switchDevice bool
}

func (e *rateEngine) SetAdaptiveRate(on bool)     { e.adaptive = on }
func (e *rateEngine) SetSwitchDeviceRate(on bool) { e.switchDevice = on }

func TestSetRatePolicyForwardsToCapableEngine(t *testing.T) {
	eng := &rateEngine{fakeEngine: &fakeEngine{}}
	p := New(stubBackend{}, eng, fakePrefs{})

	p.SetRatePolicy(true, true)
	if !eng.adaptive || !eng.switchDevice {
		t.Fatalf("rate policy not forwarded: adaptive=%v switch=%v", eng.adaptive, eng.switchDevice)
	}
}

func TestSetRatePolicyIgnoresUnsupportedEngine(t *testing.T) {
	p := New(stubBackend{}, &fakeEngine{}, fakePrefs{})
	p.SetRatePolicy(true, true)
}
