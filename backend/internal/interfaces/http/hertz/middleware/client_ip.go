package middleware

import (
	"net"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
)

// ClientIP 按可信代理规则确定来源地址：仅当 TCP 对端属于可信代理时读取 X-Forwarded-For，
// 并从右向左取第一个不可信跳点；链上出现畸形地址时回退到对端地址，不扩大信任。
func ClientIP(ctx *app.RequestContext, trusted []*net.IPNet) string {
	return resolveClientIP(peerIP(ctx), string(ctx.Request.Header.Peek("X-Forwarded-For")), trusted)
}

// resolveClientIP 是不依赖连接的纯函数版本，便于覆盖信任链边界。
func resolveClientIP(peer net.IP, forwarded string, trusted []*net.IPNet) string {
	if peer == nil {
		return ""
	}
	if !inTrusted(trusted, peer) {
		return peer.String()
	}
	chain, ok := parseForwarded(forwarded)
	if !ok || len(chain) == 0 {
		return peer.String()
	}
	candidate := peer
	for i := len(chain) - 1; i >= 0; i-- {
		if !inTrusted(trusted, chain[i]) {
			return chain[i].String()
		}
		candidate = chain[i]
	}
	return candidate.String()
}

func peerIP(ctx *app.RequestContext) net.IP {
	addr := ctx.RemoteAddr()
	if addr == nil {
		return nil
	}
	switch value := addr.(type) {
	case *net.TCPAddr:
		return value.IP
	default:
		host, _, err := net.SplitHostPort(addr.String())
		if err != nil {
			return nil
		}
		return net.ParseIP(host)
	}
}

// parseForwarded 解析 X-Forwarded-For；任一跳无法解析即视为畸形，整体不信任。
func parseForwarded(raw string) ([]net.IP, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, true
	}
	parts := strings.Split(raw, ",")
	chain := make([]net.IP, 0, len(parts))
	for _, part := range parts {
		ip := net.ParseIP(strings.TrimSpace(part))
		if ip == nil {
			return nil, false
		}
		chain = append(chain, ip)
	}
	return chain, true
}

func inTrusted(trusted []*net.IPNet, ip net.IP) bool {
	for _, network := range trusted {
		if network != nil && network.Contains(ip) {
			return true
		}
	}
	return false
}
