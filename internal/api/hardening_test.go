package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMetricsMethodCardinality(t *testing.T) {
	m := NewMetrics()
	for i := 0; i < 5000; i++ {
		m.start()
		m.done(fmt.Sprintf("M%d", i), "/v1/meta", 200, time.Millisecond)
	}
	for _, meth := range []string{"GET", "HEAD", "OPTIONS", "POST"} {
		m.start()
		m.done(meth, "/v1/meta", 200, time.Millisecond)
	}
	if len(m.reqs) != 4 { // GET, HEAD, OPTIONS, OTHER
		t.Fatalf("method labels not bounded: %d keys", len(m.reqs))
	}
	if m.reqs[mkey{"OTHER", "/v1/meta", 200}] != 5001 {
		t.Fatalf("OTHER count %d", m.reqs[mkey{"OTHER", "/v1/meta", 200}])
	}
}

func TestSearchCursorLimits(t *testing.T) {
	h := router(t, nil)
	for _, c := range []struct {
		path string
		code int
	}{
		{"/v1/cari?q=an&cursor=" + cursor(maxSearchOff), 200},
		{"/v1/cari?q=an&cursor=" + cursor(maxSearchOff+1), 400},
		{"/v1/cari?q=aceh&cursor=" + cursor(1_000_000), 400},
		{"/v1/cari?q=zzzzqqq&cursor=" + cursor(20), 400}, // past the end, like the other paged endpoints
		{"/v1/cari?q=aceh&cursor=" + cursor(20), 200},
	} {
		w := do(h, "GET", c.path)
		if w.Code != c.code {
			t.Errorf("%s: got %d want %d", c.path, w.Code, c.code)
		}
		if c.code == 400 && w.Header().Get("Content-Type") != "application/problem+json" {
			t.Errorf("%s: content-type %q", c.path, w.Header().Get("Content-Type"))
		}
	}
}

func TestEmptyListIsArray(t *testing.T) {
	w := do(router(t, nil), "GET", "/v1/cari?q=zzzzqqq")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"data":[]`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if got := nonNil[int](nil); got == nil {
		t.Fatal("nonNil(nil) must be an empty slice")
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := router(t, nil)
	for _, p := range []string{"/v1/meta", "/docs", "/nope", "/healthz"} {
		if got := do(h, "GET", p).Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: nosniff %q", p, got)
		}
	}
	if csp := do(h, "GET", "/docs").Header().Get("Content-Security-Policy"); csp != "default-src 'none'; style-src 'unsafe-inline'" {
		t.Errorf("docs CSP %q", csp)
	}
}

func TestPreflightIsRateLimited(t *testing.T) {
	h := router(t, func(c *Config) { c.RateRPS, c.RateBurst = 0.001, 1 })
	pre := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodOptions, "/v1/meta", nil)
		r.Header.Set("Origin", "https://example.org")
		r.Header.Set("Access-Control-Request-Method", "GET")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	a, b := pre(), pre()
	if a.Code != http.StatusNoContent || b.Code != http.StatusTooManyRequests {
		t.Fatalf("preflight codes %d, %d", a.Code, b.Code)
	}
	if b.Header().Get("Access-Control-Allow-Origin") != "*" || b.Header().Get("Retry-After") == "" {
		t.Fatalf("429 must carry ACAO and Retry-After: %v", b.Header())
	}
}

func TestRetryAfterBounded(t *testing.T) {
	l := newLimiter(1e-12, 1) // below what LoadConfig allows; the limiter must still not overflow
	ip := netip.MustParseAddr("1.2.3.4")
	l.allow(ip)
	ok, wait := l.allow(ip)
	if ok || wait <= 0 || wait > maxRetryAfter {
		t.Fatalf("ok=%v wait=%v", ok, wait)
	}
	h := rateLimit(l, nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	r := httptest.NewRequest("GET", "/v1/meta", nil)
	r.RemoteAddr = "1.2.3.4:1"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if n, err := strconv.Atoi(w.Header().Get("Retry-After")); err != nil || n < 1 || n > 3600 {
		t.Fatalf("Retry-After %q", w.Header().Get("Retry-After"))
	}
	// a wait under one second still advertises at least 1
	l2 := newLimiter(1000, 1)
	l2.allow(ip)
	if _, wait := l2.allow(ip); wait >= time.Second {
		t.Fatalf("wait %v", wait)
	}
	h2 := rateLimit(l2, nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	w = httptest.NewRecorder()
	h2.ServeHTTP(w, r)
	if got := w.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After %q", got)
	}
}

func TestSearchCursorChainNeverRejected(t *testing.T) {
	h := router(t, nil)
	for _, start := range []string{"", cursor(maxSearchOff - 1), cursor(maxSearchOff)} {
		cur, pages := start, 0
		for {
			p := "/v1/cari?q=an&limit=100"
			if cur != "" {
				p += "&cursor=" + cur
			}
			w := do(h, "GET", p)
			if w.Code != 200 {
				t.Fatalf("start %q page %d: %d %s", start, pages, w.Code, w.Body.String())
			}
			next, _ := body(t, w)["page"].(map[string]any)["next_cursor"].(string)
			if next == "" {
				break
			}
			cur, pages = next, pages+1
			if pages > 20 {
				t.Fatal("cursor chain does not terminate")
			}
		}
		if start == cursor(maxSearchOff) && pages != 0 {
			t.Fatal("cursor at the cap must not issue next_cursor")
		}
	}
}

func TestLoadConfigHardening(t *testing.T) {
	load := func(k, v string) (Config, error) {
		return LoadConfig(func(key string) (string, bool) { return v, key == k })
	}
	for _, v := range []string{"NaN", "Inf", "-Inf", "+Inf", "-1", "1e-12", "0.009"} {
		if _, err := load("RATE_LIMIT_RPS", v); err == nil {
			t.Errorf("RATE_LIMIT_RPS=%q must be rejected", v)
		}
	}
	if c, err := load("RATE_LIMIT_RPS", "0.01"); err != nil || c.RateRPS != 0.01 {
		t.Errorf("0.01 must be accepted: %v", err)
	}
	if c, err := load("RATE_LIMIT_RPS", "0"); err != nil || c.RateRPS != 0 {
		t.Errorf("0 must disable: %v", err)
	}
	for in, want := range map[string]string{
		"::ffff:10.0.0.0/104": "10.0.0.0/8",
		"::ffff:1.2.3.4":      "1.2.3.4/32",
		"10.0.0.0/8":          "10.0.0.0/8",
	} {
		c, err := load("TRUSTED_PROXY_CIDRS", in)
		if err != nil || len(c.TrustedProxies) != 1 || c.TrustedProxies[0].String() != want {
			t.Errorf("%s: got %v, %v want %s", in, c.TrustedProxies, err, want)
		}
	}
	if _, err := load("TRUSTED_PROXY_CIDRS", "::ffff:0.0.0.0/64"); err == nil {
		t.Error("mapped prefix shorter than /96 must be rejected")
	}
}
