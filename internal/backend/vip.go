package backend

import (
	"time"

	"github.com/team-quaver/typhoeus-go/qqmusic/modules"
)

// VipBenefit 是一项生效中的会员权益：档位名 + 该档位自己的到期时刻。
type VipBenefit struct {
	Label string
	// End 是到期时刻（上游北京时间墙钟解析而来）。
	// HasEnd=false 表示上游只给了权益标志、没给可用时间：这不是「永久」，
	// 界面要如实写「未提供到期时间」，不许拿别的档位的时间凑数。
	End    time.Time
	HasEnd bool
}

// Benefits 按档位从高到低摊平生效中的权益，与 vipLabel 的优先级同源。
//
// 有效/失效的判定已经在服务器侧做完：Typhoeus 的 handleUserVip 先过
// VipInfo.Active(now) 再下发，标志位非 0 就是当前生效，客户端不再自己比时间。
//
// 到期时间只认各权益自己的 *_end 字段。契约里的 userinfo.expire 是会员页摘要，
// 服务器注释明确「不用于独立权益的到期判断」；三个档位的时间段同样不可互相借用
// （超级会员的 svip_end 与豪华绿钻的 huge_vip_end 在旧 CGI 里甚至互换了语义）。
//
// 时间串沿用 Typhoeus 的 ParseVipTime：只认北京时间的 2006-01-02[ 15:04[:05]]，
// 纯日期按「有效到当日 23:59:59」算，异常年份（<2000 / >2100）视为没有到期时间。
func (v VIPInfo) Benefits() []VipBenefit {
	src := []struct {
		label string
		flag  int
		end   string
	}{
		{"超级会员", v.Svip, v.SvipEnd},
		{"豪华绿钻", v.Identity.HugeVip, v.Identity.HugeVipEnd},
		{"绿钻", v.Identity.Vip, v.Identity.VipEnd},
	}
	out := make([]VipBenefit, 0, len(src))
	for _, s := range src {
		if s.flag == 0 {
			continue
		}
		b := VipBenefit{Label: s.label}
		if t, ok := modules.ParseVipTime(s.end, true); ok {
			b.End, b.HasEnd = t, true
		}
		out = append(out, b)
	}
	return out
}
