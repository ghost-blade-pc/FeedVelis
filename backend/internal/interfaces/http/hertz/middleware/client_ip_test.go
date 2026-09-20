package middleware

import (
	"net"
	"testing"
)

func mustCIDR(t *testing.T, raw string) *net.IPNet {
	t.Helper()
	_, network, err := net.ParseCIDR(raw)
	if err != nil {
		t.Fatal(err)
	}
	return network
}

func TestResolveClientIP(t *testing.T) {
	trusted := []*net.IPNet{mustCIDR(t, "172.18.0.0/16")}
	cases := []struct {
		name      string
		peer      string
		forwarded string
		want      string
	}{
		{"直连不可信时忽略转发头", "203.0.113.9", "198.51.100.1", "203.0.113.9"},
		{"反代直连且无转发头", "172.18.0.5", "", "172.18.0.5"},
		{"反代直连取转发链首跳", "172.18.0.5", "203.0.113.9", "203.0.113.9"},
		{"多层反代取第一个不可信跳点", "172.18.0.5", "203.0.113.9, 172.18.0.4", "203.0.113.9"},
		{"全链可信时取最左", "172.18.0.5", "172.18.0.9, 172.18.0.4", "172.18.0.9"},
		{"畸形转发头回退到对端", "172.18.0.5", "not-an-ip, 203.0.113.9", "172.18.0.5"},
		{"缺省信任链为空时不读转发头", "172.18.0.5", "203.0.113.9", "172.18.0.5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			networks := trusted
			if tc.name == "缺省信任链为空时不读转发头" {
				networks = nil
			}
			if got := resolveClientIP(net.ParseIP(tc.peer), tc.forwarded, networks); got != tc.want {
				t.Fatalf("ClientIP = %q，期望 %q", got, tc.want)
			}
		})
	}
	if got := resolveClientIP(nil, "", trusted); got != "" {
		t.Fatalf("无对端地址时 = %q", got)
	}
}
