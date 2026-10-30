package groupassistant

import (
	"context"
	"github.com/chenhg5/cc-connect/core"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

func publicAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	for _, block := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001::/32", "2001:db8::/32", "2002::/16", "64:ff9b::/96", "64:ff9b:1::/48", "::/96", "fec0::/10"} {
		if netip.MustParsePrefix(block).Contains(ip) {
			return false
		}
	}
	return true
}

func publicHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy: nil, DisableKeepAlives: true, MaxResponseHeaderBytes: 32 << 10,
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second,
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, ErrDenied
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, ErrUnavailable
		}
		// Validate every answer, then pin the actual connection to that address.
		for _, ip := range ips {
			if !publicAddress(ip) {
				return nil, ErrDenied
			}
		}
		dialer := net.Dialer{Timeout: 15 * time.Second}
		for _, ip := range ips {
			connection, e := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if e == nil {
				return connection, nil
			}
		}
		return nil, ErrUnavailable
	}
	return &http.Client{Transport: transport, Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (c *ControlTools) validateURL(raw string) (*url.URL, error) {
	if len(raw) > 4096 || strings.ContainsAny(raw, "\r\n\x00") {
		return nil, ErrInvalid
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Opaque != "" || u.Fragment != "" || u.Hostname() == "" || u.Scheme != "http" && u.Scheme != "https" {
		return nil, ErrDenied
	}
	if u.Port() != "" && u.Port() != "80" && u.Port() != "443" {
		return nil, ErrDenied
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.Contains(host, "%") {
		return nil, ErrDenied
	}
	if ip, e := netip.ParseAddr(host); e == nil && !publicAddress(ip) {
		return nil, ErrDenied
	}
	allowed := false
	for _, domain := range c.cfg.AllowedHosts {
		allowed = allowed || domain == "*" || strings.EqualFold(domain, host)
	}
	if !allowed {
		return nil, ErrDenied
	}
	return u, nil
}

func (c *ControlTools) get(ctx context.Context, raw, target string, approval bool) (*http.Response, bool, error) {
	for redirects := 0; redirects <= 5; redirects++ {
		u, err := c.validateURL(raw)
		if err != nil {
			return nil, approval, err
		}
		if target == "" && !previewExtension(u.Path) {
			return nil, approval, ErrDenied
		}
		if target != "" && !ordinaryExtension(u.Path) && !approval {
			if err := c.requireApproval(ctx, c.T(core.MsgGroupDownloadAction, u.String(), target)); err != nil {
				return nil, false, err
			}
			approval = true
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, approval, ErrInvalid
		}
		req.Header.Set("User-Agent", "cc-connect-group-assistant/1.0")
		resp, err := c.client.Do(req)
		if err != nil {
			return nil, approval, ErrUnavailable
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			location, e := resp.Location()
			resp.Body.Close()
			if e != nil || u.Scheme == "https" && location.Scheme != "https" {
				return nil, approval, ErrDenied
			}
			raw = location.String()
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, approval, ErrUnavailable
		}
		return resp, approval, nil
	}
	return nil, approval, ErrDenied
}
