package netguard

import (
	"context"
	"net"
	"testing"
)

func mkGuard(ips []net.IP, err error) *Guard {
	return &Guard{
		Resolver: func(ctx context.Context, host string) ([]net.IP, error) {
			return ips, err
		},
	}
}

func TestSSRF_AllowsPublicHost(t *testing.T) {
	g := mkGuard([]net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("1.1.1.1")}, nil)
	if err := g.Validate(context.Background(), "http://8.8.8.8/cover.jpg"); err != nil {
		t.Errorf("public IP should pass, got %v", err)
	}
}

func TestSSRF_RejectsSchemes(t *testing.T) {
	g := mkGuard([]net.IP{net.ParseIP("8.8.8.8")}, nil)
	for _, u := range []string{
		"file:///etc/passwd",
		"ftp://example.com/x",
		"gopher://example.com",
		"javascript:alert(1)",
		"data:image/png;base64,AAAA",
	} {
		if err := g.Validate(context.Background(), u); err == nil {
			t.Errorf("scheme %q should be rejected", u)
		}
	}
}

func TestSSRF_RejectsPrivateNetworks(t *testing.T) {
	cases := []struct {
		name string
		ip   string
	}{
		{"loopback v4", "127.0.0.1"},
		{"loopback v6", "::1"},
		{"private 10/8", "10.0.0.1"},
		{"private 192.168/16", "192.168.1.1"},
		{"private 172.16/12", "172.16.0.1"},
		{"link-local", "169.254.169.254"},
		{"AWS metadata", "169.254.169.254"},
		{"unspecified v4", "0.0.0.0"},
		{"unspecified v6", "::"},
		{"private v6", "fc00::1"},
		{"link-local v6", "fe80::1"},
		{"multicast v4", "224.0.0.1"},
		{"v4-mapped v6 loopback", "::ffff:127.0.0.1"},
		{"v4-mapped v6 private", "::ffff:10.0.0.1"},
		{"v4-mapped v6 link-local", "::ffff:169.254.169.254"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := mkGuard([]net.IP{net.ParseIP(c.ip)}, nil)
			if err := g.Validate(context.Background(), "http://danger.test/"); err == nil {
				t.Errorf("IP %s should be rejected", c.ip)
			}
		})
	}
}

func TestSSRF_RejectsAnyNonPublicInMixedSet(t *testing.T) {
	// 攻击者可控 DNS 返回 1 个公网 + 1 个 127.0.0.1；必须拒绝。
	g := mkGuard([]net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("127.0.0.1")}, nil)
	if err := g.Validate(context.Background(), "http://mixed.test/"); err == nil {
		t.Error("mixed public/private set should be rejected")
	}
}

func TestSSRF_RejectsEmptyOrFailedDNS(t *testing.T) {
	if err := mkGuard(nil, nil).Validate(context.Background(), "http://x/"); err == nil {
		t.Error("empty IP set should be rejected")
	}
	if err := mkGuard(nil, &net.DNSError{Err: "no such host", Name: "x"}).Validate(context.Background(), "http://x/"); err == nil {
		t.Error("DNS failure should be rejected")
	}
}

func TestSSRF_RejectsEmptyHost(t *testing.T) {
	if err := mkGuard([]net.IP{net.ParseIP("8.8.8.8")}, nil).Validate(context.Background(), "http:///path"); err == nil {
		t.Error("empty host should be rejected")
	}
}

func TestSSRF_NilGuardPanicsViaCheck(t *testing.T) {
	var g *Guard
	if err := g.Validate(context.Background(), "http://x/"); err == nil {
		t.Error("nil guard should produce an error")
	}
}
