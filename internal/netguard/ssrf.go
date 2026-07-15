// Package netguard 给 HTTP 客户端与外部资源下载提供 SSRF 防护 (P1.5 issue F)。
//
// Guard.Validate 在发起外网请求之前对 URL 做以下校验：
//   - scheme 必须是 http 或 https
//   - host 必须能解析 (ResolverFunc)
//   - 解析得到的每个 IP 必须"公开"——拒绝 loopback / link-local /
//     private / multicast / unspecified
//
// 已知限制：DNS rebinding 不在 v1 防护范围内 (Cl → 目标 IP 在
// Validate 通过后被 attacker 在 dial 前替换为私有 IP)。生产环境建议
// 配合外部代理或直接对 DialContext 做再次解析，本包通过可注入的
// Resolver 留出这点升级空间。
//
// 测试时通过 Guard.Resolver 替换 net.DefaultResolver，避免污染 DNS。
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
)

// ResolverFunc 接受 host，返回其 A/AAAA 记录 IP 列表。
type ResolverFunc func(ctx context.Context, host string) ([]net.IP, error)

// DefaultResolver 走 Go 的 stdlib net.DefaultResolver。
func DefaultResolver(ctx context.Context, host string) ([]net.IP, error) {
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	out := make([]net.IP, len(ips))
	for i, ip := range ips {
		out[i] = ip.IP
	}
	return out, nil
}

// Guard 在 SSRF 防护时被实例化；默认走 stdlib 解析函数。
//
// Resolver 控制 DNS 解析；Transport 控制 http.RoundTripper。
// 二者均可注入以满足单测需求 (SafeHTTPGet 测试在
// internal/netguard/safehttp_test.go 用 ipSwitch + fakeRoundTrip
// 不联网验证 redirect-pivot / body-cap 路径)。
type Guard struct {
	Resolver  ResolverFunc
	Transport http.RoundTripper
}

// NewGuard 返回一个使用 stdlib resolver 与 stdlib Transport 的 Guard。
func NewGuard() *Guard {
	return &Guard{Resolver: DefaultResolver, Transport: http.DefaultTransport}
}

// Validate 检查 rawURL 是否可以安全地 fetch：scheme 必须为 http(s)，
// 解析后的所有 IP 都必须满足 isPublic 规则。
func (g *Guard) Validate(ctx context.Context, rawURL string) error {
	if g == nil || g.Resolver == nil {
		return errors.New("netguard: Guard/Resolver not configured")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("netguard: parse url: %w", err)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("netguard: scheme %q not allowed", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("netguard: empty host")
	}
	ips, err := g.Resolver(ctx, host)
	if err != nil {
		return fmt.Errorf("netguard: resolve %s: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("netguard: %s resolved to no IPs", host)
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return fmt.Errorf("netguard: %s resolves to non-public IP %s", host, ip)
		}
	}
	return nil
}

// isPublicIP 拒绝 loopback / link-local / private / multicast / unspecified。
// Go 1.17+ 的 IsPrivate 覆盖 RFC 1918 + ULA (fc00::/7) 等。
func isPublicIP(ip net.IP) bool {
	return !ip.IsUnspecified() &&
		!ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() &&
		!ip.IsMulticast() &&
		!ip.IsPrivate()
}
