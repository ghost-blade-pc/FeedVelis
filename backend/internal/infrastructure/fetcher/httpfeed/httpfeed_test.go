package httpfeed

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
)

// TestEnvironmentProxyIsIgnored 固化：通用环境代理不是安全授权，默认必须直连。
func TestEnvironmentProxyIsIgnored(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:7897")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:7897")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:7897")
	fetcher := NewFetcher(Options{})
	if fetcher.proxy != nil {
		t.Fatal("环境代理不得被采纳")
	}
	if transport, ok := fetcher.client.Transport.(*http.Transport); !ok || transport.Proxy != nil {
		t.Fatal("未配置专用代理时必须保持直连")
	}
	// 环境代理地址也不能因此获得拨号豁免。
	if fetcher.isTrustedProxy("127.0.0.1", "7897") {
		t.Fatal("环境代理地址不得被视为可信代理")
	}
}

// TestTrustedProxySelection 固化专用代理的选择与拨号豁免。
func TestTrustedProxySelection(t *testing.T) {
	fetcher := NewFetcher(Options{ProxyURL: "http://proxy.internal:3128"})
	if fetcher.proxy == nil {
		t.Fatal("专用代理必须生效")
	}
	if !fetcher.isTrustedProxy("proxy.internal", "3128") {
		t.Fatal("专用代理地址应放行拨号")
	}
	if fetcher.isTrustedProxy("proxy.internal", "8080") || fetcher.isTrustedProxy("other.internal", "3128") {
		t.Fatal("不得放行非代理地址")
	}
	// 声明式代理同样支持默认端口。
	defaultPort := NewFetcher(Options{ProxyURL: "https://proxy.internal"})
	if !defaultPort.isTrustedProxy("proxy.internal", "443") {
		t.Fatal("https 代理默认端口应为 443")
	}
	// 通用环境代理与专用代理同时存在时只采用专用配置。
	t.Setenv("HTTPS_PROXY", "http://env-proxy.internal:7897")
	both := NewFetcher(Options{ProxyURL: "http://dedicated.internal:3128"})
	if both.proxy == nil || both.proxy.Hostname() != "dedicated.internal" {
		t.Fatalf("应只采用专用代理，实际 %v", both.proxy)
	}
	if both.isTrustedProxy("env-proxy.internal", "7897") {
		t.Fatal("同时存在时不得放行环境代理地址")
	}
}

// TestNetworkModeRedaction 固化启动日志字段：只记录模式与主机名，不含凭据。
func TestNetworkModeRedaction(t *testing.T) {
	mode, host := NetworkMode("")
	if mode != "direct" || host != "" {
		t.Fatalf("未配置 = %s/%s", mode, host)
	}
	mode, host = NetworkMode("http://user:secret@proxy.internal:3128")
	if mode != "trusted_proxy" || host != "proxy.internal" {
		t.Fatalf("脱敏结果 = %s/%s", mode, host)
	}
	if strings.Contains(host, "user") || strings.Contains(host, "secret") {
		t.Fatalf("主机字段泄露凭据: %s", host)
	}
}

func TestRestrictedAddresses(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "::1", "fc00::1"} {
		if !isRestrictedIP(netip.MustParseAddr(raw)) {
			t.Fatalf("expected %s restricted", raw)
		}
	}
	if isRestrictedIP(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("public address was restricted")
	}
}

func TestFetcherConditionalRequestAndResponseLimit(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("当前环境不允许监听本地端口: %v", err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/redirect/") {
			count, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/redirect/"))
			if count > 0 {
				http.Redirect(w, r, "/redirect/"+strconv.Itoa(count-1), http.StatusFound)
				return
			}
			_, _ = w.Write([]byte("ok"))
			return
		}
		if r.URL.Path == "/not-modified" {
			if r.Header.Get("If-None-Match") != `"v1"` {
				t.Errorf("If-None-Match=%q", r.Header.Get("If-None-Match"))
			}
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = io.CopyN(w, zeroReader{}, maxResponseBytes+1)
	}))
	server.Listener = listener
	server.Start()
	defer server.Close()
	fetcher := NewFetcher(Options{})
	fetcher.allowRestrictedForTesting = true
	baseURL := "http://localhost:" + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	etag := `"v1"`
	response, err := fetcher.Fetch(context.Background(), ports.FetchRequest{URL: baseURL + "/not-modified", ETag: &etag})
	if err != nil || !response.NotModified || len(response.Body) != 0 {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	_, err = fetcher.Fetch(context.Background(), ports.FetchRequest{URL: baseURL + "/large"})
	var fetchErr *Error
	if !errors.As(err, &fetchErr) || fetchErr.Code() != "RESPONSE_TOO_LARGE" {
		t.Fatalf("err=%v", err)
	}
	_, err = fetcher.Fetch(context.Background(), ports.FetchRequest{URL: baseURL + "/redirect/6"})
	if !errors.As(err, &fetchErr) || fetchErr.Code() != "TOO_MANY_REDIRECTS" {
		t.Fatalf("redirect err=%v", err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = 'x'
	}
	return len(buffer), nil
}

func TestFetcherRejectsLoopbackBeforeDial(t *testing.T) {
	_, err := NewFetcher(Options{}).Fetch(context.Background(), ports.FetchRequest{URL: "http://127.0.0.1:80/feed"})
	var fetchErr *Error
	if !errors.As(err, &fetchErr) || fetchErr.Code() != "SSRF_BLOCKED" {
		t.Fatalf("err=%v", err)
	}
}

func TestParserSupportsThreeFormats(t *testing.T) {
	fixtures := []string{"rss.xml", "atom.xml", "jsonfeed.json"}
	for _, name := range fixtures {
		body, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "test", "fixtures", "feeds", name))
		if err != nil {
			t.Fatal(err)
		}
		feed, err := NewParser().Parse(context.Background(), body, "https://example.com/feed")
		if err != nil {
			t.Fatal(err)
		}
		if len(feed.Items) != 1 {
			t.Fatalf("items=%d", len(feed.Items))
		}
	}
}

func TestParserCapsItemsAndRejectsMalformedFeed(t *testing.T) {
	var body strings.Builder
	body.WriteString(`<rss version="2.0"><channel><title>many</title>`)
	for index := 0; index < 501; index++ {
		body.WriteString(`<item><guid>` + strconv.Itoa(index) + `</guid><link>https://example.com/` + strconv.Itoa(index) + `</link></item>`)
	}
	body.WriteString(`</channel></rss>`)
	feed, err := NewParser().Parse(context.Background(), []byte(body.String()), "https://example.com/feed")
	if err != nil || len(feed.Items) != 500 {
		t.Fatalf("items=%d err=%v", len(feed.Items), err)
	}
	if _, err := NewParser().Parse(context.Background(), []byte(`<rss><broken>`), "https://example.com/feed"); err == nil {
		t.Fatal("expected malformed feed error")
	}
}

func TestSanitizerAllowlistAndSafeLinks(t *testing.T) {
	result := NewSanitizer().Sanitize(`<p onclick="x">hello<script>x</script><a href="javascript:x">bad</a><a href="https://example.com">ok</a></p>`)
	for _, forbidden := range []string{"script", "onclick", "javascript"} {
		if strings.Contains(result.HTML, forbidden) {
			t.Fatalf("unsafe html: %s", result.HTML)
		}
	}
	if !strings.Contains(result.HTML, `rel="nofollow noopener noreferrer"`) || !strings.Contains(result.PlainText, "hello") || !strings.Contains(result.PlainText, "ok") {
		t.Fatalf("result=%+v", result)
	}
}

func TestSanitizerKeepsImagesWithSafeSrc(t *testing.T) {
	result := NewSanitizer().Sanitize(`<p>before<img src="https://example.com/a.png" alt="图"><img src="/relative.png"><img src="javascript:x"></p>`)
	if !strings.Contains(result.HTML, `src="https://example.com/a.png"`) {
		t.Fatalf("绝对图片地址应保留: %s", result.HTML)
	}
	if !strings.Contains(result.HTML, `alt="图"`) || !strings.Contains(result.HTML, `loading="lazy"`) {
		t.Fatalf("图片属性应保留并统一懒加载: %s", result.HTML)
	}
	for _, forbidden := range []string{"relative.png", "javascript"} {
		if strings.Contains(result.HTML, forbidden) {
			t.Fatalf("不安全图片地址未清理: %s", result.HTML)
		}
	}
}

func TestParserResolvesRelativeContentURLs(t *testing.T) {
	body := []byte(`<rss version="2.0"><channel><title>t</title><link>https://example.com/feed</link><item>` +
		`<guid>1</guid><link>https://example.com/posts/a</link>` +
		`<description><![CDATA[<p><img src="/img/cover.png"><a href="../other">link</a></p>]]></description>` +
		`</item></channel></rss>`)
	feed, err := NewParser().Parse(context.Background(), body, "https://example.com/feed")
	if err != nil || len(feed.Items) != 1 {
		t.Fatalf("items=%d err=%v", len(feed.Items), err)
	}
	description := feed.Items[0].Description
	if description == nil {
		t.Fatal("description is nil")
	}
	if !strings.Contains(*description, `src="https://example.com/img/cover.png"`) ||
		!strings.Contains(*description, `href="https://example.com/other"`) {
		t.Fatalf("相对 URL 未按文章页基准解析: %s", *description)
	}
}

// fakeResolver 返回固定解析结果，用于验证受限地址防护而不依赖真实 DNS。
type fakeResolver struct {
	addresses []netip.Addr
	err       error
	calls     int
}

func (r *fakeResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	r.calls++
	if r.err != nil {
		return nil, r.err
	}
	return r.addresses, nil
}

func fetcherWithResolver(resolver Resolver, options Options) *Fetcher {
	fetcher := NewFetcher(options)
	fetcher.resolver = resolver
	return fetcher
}

// TestRestrictedAddressMatrix 覆盖 IPv4/IPv6 各受限网段与公网对照。
func TestRestrictedAddressMatrix(t *testing.T) {
	restricted := []string{
		"127.0.0.1", "0.0.0.0", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254",
		"100.64.0.1", "::1", "fe80::1", "fc00::1", "::ffff:127.0.0.1",
	}
	for _, raw := range restricted {
		if !isRestrictedIP(netip.MustParseAddr(raw)) {
			t.Fatalf("%s 应被判定为受限地址", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111", "172.32.0.1", "192.169.0.1"} {
		if isRestrictedIP(netip.MustParseAddr(raw)) {
			t.Fatalf("%s 是公网地址，不应被限制", raw)
		}
	}
}

// TestResolveAllowedRejectsRestrictedAndMixedDNS 覆盖解析结果校验。
func TestResolveAllowedRejectsRestrictedAndMixedDNS(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name      string
		addresses []netip.Addr
		wantCode  string
	}{
		{"公网 IPv4", []netip.Addr{netip.MustParseAddr("93.184.216.34")}, ""},
		{"公网 IPv6", []netip.Addr{netip.MustParseAddr("2606:2800:220:1::1")}, ""},
		{"IPv4 环回", []netip.Addr{netip.MustParseAddr("127.0.0.1")}, "SSRF_BLOCKED"},
		{"IPv6 环回", []netip.Addr{netip.MustParseAddr("::1")}, "SSRF_BLOCKED"},
		{"云元数据", []netip.Addr{netip.MustParseAddr("169.254.169.254")}, "SSRF_BLOCKED"},
		{"私网", []netip.Addr{netip.MustParseAddr("10.0.0.5")}, "SSRF_BLOCKED"},
		// 混合结果：只要有一条受限就整体拒绝，避免解析顺序决定安全性。
		{"公网与私网混合", []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("10.0.0.5")}, "SSRF_BLOCKED"},
		{"公网与环回混合", []netip.Addr{netip.MustParseAddr("2606:2800:220:1::1"), netip.MustParseAddr("::1")}, "SSRF_BLOCKED"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fetcher := fetcherWithResolver(&fakeResolver{addresses: testCase.addresses}, Options{})
			addresses, err := fetcher.resolveAllowed(ctx, "example.com")
			if testCase.wantCode == "" {
				if err != nil || len(addresses) != len(testCase.addresses) {
					t.Fatalf("addresses=%v err=%v", addresses, err)
				}
				return
			}
			var fetchErr *Error
			if !errors.As(err, &fetchErr) || fetchErr.Code() != testCase.wantCode {
				t.Fatalf("err=%v", err)
			}
		})
	}

	// 字面 IP 不经过解析器，直接判定。
	literal := fetcherWithResolver(&fakeResolver{}, Options{})
	if _, err := literal.resolveAllowed(ctx, "192.168.0.1"); err == nil {
		t.Fatal("字面私网 IP 应被拒绝")
	}
	if _, err := literal.resolveAllowed(ctx, "8.8.8.8"); err != nil {
		t.Fatalf("字面公网 IP err=%v", err)
	}
	if literal.resolver.(*fakeResolver).calls != 0 {
		t.Fatal("字面 IP 不得触发 DNS 解析")
	}

	// 解析失败是稳定的可识别分类。
	failing := fetcherWithResolver(&fakeResolver{err: errors.New("dns down")}, Options{})
	var fetchErr *Error
	if _, err := failing.resolveAllowed(ctx, "example.com"); !errors.As(err, &fetchErr) || fetchErr.Code() != "DNS_FAILED" {
		t.Fatalf("解析失败 err=%v", err)
	}
}

// TestValidateRemoteURLRules 覆盖协议、凭据与端口边界。
func TestValidateRemoteURLRules(t *testing.T) {
	allowed := []string{"http://example.com/feed", "https://example.com:443/feed", "http://example.com:80/feed"}
	for _, raw := range allowed {
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateRemoteURL(parsed); err != nil {
			t.Fatalf("%s 应被允许: %v", raw, err)
		}
	}
	rejected := map[string]string{
		"file:///tmp/feed":              "INVALID_URL",
		"ftp://example.com/feed":        "INVALID_URL",
		"http://user:pass@example.com/": "INVALID_URL",
		"http://example.com:8080/feed":  "SSRF_BLOCKED",
		"https://example.com:22/feed":   "SSRF_BLOCKED",
		"http://169.254.169.254/latest": "SSRF_BLOCKED",
		"http://[::1]:80/feed":          "SSRF_BLOCKED",
	}
	for raw, wantCode := range rejected {
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		var fetchErr *Error
		if err := validateRemoteURL(parsed); !errors.As(err, &fetchErr) || fetchErr.Code() != wantCode {
			t.Fatalf("%s err=%v，期望 %s", raw, err, wantCode)
		}
	}
}

// TestRedirectToRestrictedAddressIsBlocked 固定重定向目标同样受 SSRF 校验。
func TestRedirectToRestrictedAddressIsBlocked(t *testing.T) {
	fetcher := NewFetcher(Options{})
	request, err := http.NewRequest(http.MethodGet, "http://169.254.169.254/latest/meta-data", nil)
	if err != nil {
		t.Fatal(err)
	}
	var fetchErr *Error
	if err := fetcher.client.CheckRedirect(request, nil); !errors.As(err, &fetchErr) || fetchErr.Code() != "SSRF_BLOCKED" {
		t.Fatalf("重定向到受限地址 err=%v", err)
	}
	// 公网重定向目标放行。
	allowed, err := http.NewRequest(http.MethodGet, "https://example.com/feed", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := fetcher.client.CheckRedirect(allowed, nil); err != nil {
		t.Fatalf("公网重定向被拒: %v", err)
	}
	// 重定向次数上限。
	toolong := make([]*http.Request, maxRedirects+1)
	if err := fetcher.client.CheckRedirect(allowed, toolong); !errors.As(err, &fetchErr) || fetchErr.Code() != "TOO_MANY_REDIRECTS" {
		t.Fatalf("重定向次数 err=%v", err)
	}
}

// TestFetchTimeoutIsClassified 固化超时分类：上游无响应必须收敛为 FETCH_TIMEOUT。
func TestFetchTimeoutIsClassified(t *testing.T) {
	blocked := make(chan struct{})
	defer close(blocked)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("当前环境不允许监听本地端口: %v", err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocked
	}))
	server.Listener = listener
	server.Start()
	defer server.Close()

	fetcher := NewFetcher(Options{})
	fetcher.allowRestrictedForTesting = true
	// 请求级超时为 20 秒，这里通过已取消的上下文让上游永不返回。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = fetcher.Fetch(ctx, ports.FetchRequest{URL: "http://localhost:" + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port) + "/slow"})
	var fetchErr *Error
	if !errors.As(err, &fetchErr) {
		t.Fatalf("err=%v", err)
	}
	if fetchErr.Code() != "FETCH_TIMEOUT" && fetchErr.Code() != "FETCH_FAILED" {
		t.Fatalf("超时分类 = %s", fetchErr.Code())
	}
}
