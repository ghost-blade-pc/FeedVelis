// Package httpfeed 实现不信任公网 Feed 的受限抓取、解析和清理适配器。
package httpfeed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
)

const (
	maxResponseBytes = 5 * 1024 * 1024
	maxRedirects     = 5
)

type Error struct {
	code string
	err  error
}

func (e *Error) Error() string { return e.code + ": " + e.err.Error() }
func (e *Error) Unwrap() error { return e.err }
func (e *Error) Code() string  { return e.code }

type Fetcher struct {
	client                    *http.Client
	resolver                  *net.Resolver
	allowRestrictedForTesting bool
}

func NewFetcher() *Fetcher {
	resolver := net.DefaultResolver
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	fetcher := &Fetcher{resolver: resolver}
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            fetcher.safeDialContext(dialer),
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    5 * time.Second,
		ResponseHeaderTimeout:  10 * time.Second,
		IdleConnTimeout:        90 * time.Second,
		MaxResponseHeaderBytes: 64 * 1024,
	}
	fetcher.client = &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return &Error{code: "TOO_MANY_REDIRECTS", err: errors.New("重定向次数超过限制")}
			}
			return fetcher.validateURL(req.URL)
		},
	}
	return fetcher
}

func (f *Fetcher) Fetch(ctx context.Context, request ports.FetchRequest) (ports.FetchResponse, error) {
	u, err := url.Parse(request.URL)
	if err != nil {
		return ports.FetchResponse{}, &Error{code: "INVALID_URL", err: err}
	}
	if err := f.validateURL(u); err != nil {
		return ports.FetchResponse{}, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return ports.FetchResponse{}, &Error{code: "INVALID_URL", err: err}
	}
	httpRequest.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/feed+json, application/json, application/xml, text/xml;q=0.9, */*;q=0.1")
	httpRequest.Header.Set("User-Agent", "VelisFeed/0.1")
	if request.ETag != nil {
		httpRequest.Header.Set("If-None-Match", headerValue(*request.ETag))
	}
	if request.LastModified != nil {
		httpRequest.Header.Set("If-Modified-Since", headerValue(*request.LastModified))
	}
	response, err := f.client.Do(httpRequest)
	if err != nil {
		if errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			return ports.FetchResponse{}, &Error{code: "FETCH_TIMEOUT", err: requestCtx.Err()}
		}
		type coded interface{ Code() string }
		var codedError coded
		if errors.As(err, &codedError) {
			return ports.FetchResponse{}, &Error{code: codedError.Code(), err: err}
		}
		return ports.FetchResponse{}, &Error{code: "FETCH_FAILED", err: err}
	}
	defer response.Body.Close()
	result := ports.FetchResponse{
		FinalURL:     response.Request.URL.String(),
		ETag:         optionalHeader(response.Header.Get("ETag")),
		LastModified: optionalHeader(response.Header.Get("Last-Modified")),
	}
	if response.StatusCode == http.StatusNotModified {
		result.NotModified = true
		return result, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ports.FetchResponse{}, &Error{code: "HTTP_STATUS", err: fmt.Errorf("上游返回状态 %d", response.StatusCode)}
	}
	limited := io.LimitReader(response.Body, maxResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return ports.FetchResponse{}, &Error{code: "READ_FAILED", err: err}
	}
	if len(body) > maxResponseBytes {
		return ports.FetchResponse{}, &Error{code: "RESPONSE_TOO_LARGE", err: errors.New("响应体超过 5 MiB")}
	}
	result.Body = body
	return result, nil
}

func (f *Fetcher) safeDialContext(dialer *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, &Error{code: "SSRF_BLOCKED", err: errors.New("目标地址无效")}
		}
		addresses, err := f.resolveAllowed(connectCtx, host)
		if err != nil {
			return nil, err
		}
		var dialErr error
		for _, address := range addresses {
			conn, err := dialer.DialContext(connectCtx, network, net.JoinHostPort(address.String(), port))
			if err == nil {
				return conn, nil
			}
			dialErr = err
		}
		return nil, &Error{code: "CONNECT_FAILED", err: dialErr}
	}
}

func (f *Fetcher) resolveAllowed(ctx context.Context, host string) ([]netip.Addr, error) {
	if address, err := netip.ParseAddr(host); err == nil {
		if isRestrictedIP(address) && !f.allowRestrictedForTesting {
			return nil, &Error{code: "SSRF_BLOCKED", err: errors.New("目标 IP 不允许访问")}
		}
		return []netip.Addr{address.Unmap()}, nil
	}
	addresses, err := f.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addresses) == 0 {
		return nil, &Error{code: "DNS_FAILED", err: errors.New("域名解析失败")}
	}
	for _, address := range addresses {
		if isRestrictedIP(address) && !f.allowRestrictedForTesting {
			return nil, &Error{code: "SSRF_BLOCKED", err: errors.New("域名解析到受限地址")}
		}
	}
	return addresses, nil
}

func validateRemoteURL(u *url.URL) error {
	if u == nil {
		return &Error{code: "INVALID_URL", err: errors.New("仅允许无凭据的 HTTP/HTTPS URL")}
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return &Error{code: "INVALID_URL", err: errors.New("仅允许无凭据的 HTTP/HTTPS URL")}
	}
	if port := u.Port(); port != "" && port != "80" && port != "443" {
		return &Error{code: "SSRF_BLOCKED", err: errors.New("目标端口不允许访问")}
	}
	if address, err := netip.ParseAddr(u.Hostname()); err == nil && isRestrictedIP(address) {
		return &Error{code: "SSRF_BLOCKED", err: errors.New("目标 IP 不允许访问")}
	}
	return nil
}

func (f *Fetcher) validateURL(u *url.URL) error {
	if !f.allowRestrictedForTesting {
		return validateRemoteURL(u)
	}
	if u == nil {
		return &Error{code: "INVALID_URL", err: errors.New("仅允许无凭据的 HTTP/HTTPS URL")}
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return &Error{code: "INVALID_URL", err: errors.New("仅允许无凭据的 HTTP/HTTPS URL")}
	}
	return nil
}

func isRestrictedIP(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || address.IsUnspecified() || address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() {
		return true
	}
	for _, prefix := range restrictedPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

var restrictedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

func optionalHeader(value string) *string {
	value = headerValue(value)
	if value == "" {
		return nil
	}
	return &value
}

func headerValue(value string) string {
	value = strings.ToValidUTF8(value, "�")
	value = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == 0 {
			return -1
		}
		return r
	}, value)
	value = strings.TrimSpace(value)
	if len(value) > 1024 {
		value = value[:1024]
	}
	return value
}
