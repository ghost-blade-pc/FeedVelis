package httpfeed

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
)

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
	fetcher := NewFetcher()
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
	_, err := NewFetcher().Fetch(context.Background(), ports.FetchRequest{URL: "http://127.0.0.1:80/feed"})
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
	result := NewSanitizer().Sanitize(`<p onclick="x">hello<script>x</script><a href="javascript:x">bad</a><a href="https://example.com">ok</a><img src=x></p>`)
	for _, forbidden := range []string{"script", "onclick", "javascript", "<img"} {
		if strings.Contains(result.HTML, forbidden) {
			t.Fatalf("unsafe html: %s", result.HTML)
		}
	}
	if !strings.Contains(result.HTML, `rel="nofollow noopener noreferrer"`) || !strings.Contains(result.PlainText, "hello") || !strings.Contains(result.PlainText, "ok") {
		t.Fatalf("result=%+v", result)
	}
}
