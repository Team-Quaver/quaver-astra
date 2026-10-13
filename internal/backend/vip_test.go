package backend

import (
	"testing"
	"time"
)

// TestVIPBenefits 锁住「个人中心」依赖的三条契约：档位顺序、失效档位不出现、
// 到期时间只认各档自己的 *_end。
func TestVIPBenefits(t *testing.T) {
	v := VIPInfo{
		Svip:    1,
		SvipEnd: "2026-12-31",
		Identity: VIPIdentity{
			Vip:        1,
			VipEnd:     "2026-11-20 15:30:00",
			HugeVip:    0,
			HugeVipEnd: "2030-01-01", // 档位无效，时间再晚也不许出现
			Level:      12,
		},
		Userinfo: VIPUserinfo{Expire: "2024-01-01"}, // 摘要字段，不参与权益判断
	}
	got := v.Benefits()
	if len(got) != 2 {
		t.Fatalf("应有两条生效权益，实得 %d：%+v", len(got), got)
	}
	if got[0].Label != "超级会员" || got[1].Label != "绿钻" {
		t.Errorf("档位顺序应为 超级会员 → 绿钻，实得 %q, %q", got[0].Label, got[1].Label)
	}
	// 纯日期按「有效到当日末尾」，与 Typhoeus 的 ParseVipTime 一致。
	want := time.Date(2026, 12, 31, 23, 59, 59, 0, time.FixedZone("CST+8", 8*60*60))
	if !got[0].HasEnd || !got[0].End.Equal(want) {
		t.Errorf("超级会员到期时刻 = %v (has=%v)，期望 %v", got[0].End, got[0].HasEnd, want)
	}
	if got[1].End.Format("2006-01-02 15:04") != "2026-11-20 15:30" {
		t.Errorf("绿钻到期时刻 = %v", got[1].End)
	}
}

func TestVIPBenefitsWithoutEnd(t *testing.T) {
	// 上游只给标志不给时间：如实报「没有到期时间」，不许借用别的档位或写成永久。
	v := VIPInfo{Svip: 1, Identity: VIPIdentity{Vip: 1, VipEnd: "bad-date"}}
	got := v.Benefits()
	if len(got) != 2 {
		t.Fatalf("两条权益都该出现（时间缺失不等于没有权益）：%+v", got)
	}
	for _, b := range got {
		if b.HasEnd {
			t.Errorf("%s 不该有到期时间：%v", b.Label, b.End)
		}
	}
}

func TestVIPBenefitsEmpty(t *testing.T) {
	if got := (VIPInfo{Identity: VIPIdentity{Level: 8}}).Benefits(); len(got) != 0 {
		t.Errorf("非会员不该摊出权益：%+v", got)
	}
}
