package appui

import (
	"testing"

	"github.com/Team-Quaver/quaver-astra/internal/backend"
)

func TestVIPLabelUsesStableEntitlementContract(t *testing.T) {
	for _, tc := range []struct {
		name string
		vip  backend.VIPInfo
		want string
	}{
		{name: "none", want: ""},
		{name: "green", vip: backend.VIPInfo{Identity: backend.VIPIdentity{Vip: 1}}, want: "绿钻"},
		{name: "huge green", vip: backend.VIPInfo{Identity: backend.VIPIdentity{HugeVip: 1}}, want: "豪华绿钻"},
		{name: "super wins", vip: backend.VIPInfo{Svip: 1}, want: "超级会员"},
		{name: "level is not entitlement", vip: backend.VIPInfo{Identity: backend.VIPIdentity{Level: 9}}, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := vipLabel(tc.vip); got != tc.want {
				t.Fatalf("vipLabel() = %q, want %q", got, tc.want)
			}
		})
	}
}
