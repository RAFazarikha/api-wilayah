package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	dataembed "api-wilayah/data"
	"api-wilayah/internal/data"
)

var testStore *data.Store

func router(tb testing.TB, mod func(*Config)) http.Handler {
	tb.Helper()
	if testStore == nil {
		s, err := data.Load(dataembed.WilayahGz)
		if err != nil {
			tb.Fatal(err)
		}
		testStore = s
	}
	cfg, _ := LoadConfig(func(string) (string, bool) { return "", false })
	cfg.RateRPS = 0
	if mod != nil {
		mod(&cfg)
	}
	ctx, cancel := context.WithCancel(context.Background())
	tb.Cleanup(cancel)
	return NewRouter(ctx, cfg, testStore, slog.New(slog.NewJSONHandler(io.Discard, nil)), NewMetrics())
}

func do(h http.Handler, method, path string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func body(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("body %q: %v", w.Body.String(), err)
	}
	return m
}

func TestEndpointsStatus(t *testing.T) {
	h := router(t, nil)
	cases := []struct {
		path string
		code int
	}{
		{"/v1/provinsi", 200}, {"/v1/provinsi/11/kabupaten", 200}, {"/v1/kabupaten/11.01/kecamatan", 200},
		{"/v1/kecamatan/11.01.01/desa", 200}, {"/v1/wilayah/11.01.01.2001", 200}, {"/v1/kodepos/23773", 200},
		{"/v1/cari?q=bakongan", 200}, {"/v1/wilayah/11/pulau", 200}, {"/v1/wilayah/11.01/pulau", 200}, {"/v1/meta", 200}, {"/healthz", 200},
		{"/docs", 200}, {"/docs/openapi.yaml", 200},
		{"/v1/wilayah/1", 400}, {"/v1/wilayah/11.1", 400}, {"/v1/provinsi/11.01/kabupaten", 400}, {"/v1/kodepos/123", 400},
		{"/v1/wilayah/11.01.01/pulau", 400}, {"/v1/cari", 400}, {"/v1/cari?q=a", 400}, {"/v1/cari?q=" + strings.Repeat("a", 65), 400},
		{"/v1/cari?q=ab&level=9", 400}, {"/v1/cari?q=ab&limit=101", 400}, {"/v1/kecamatan/11.01.01/desa?cursor=bad", 400},
		{"/v1/kecamatan/11.01.01/desa?limit=0", 400}, {"/v1/kecamatan/11.01.01/desa?cursor=" + cursor(99999), 400},
		{"/v1/wilayah/99", 404}, {"/v1/kodepos/00000", 404}, {"/v1/provinsi/99/kabupaten", 404}, {"/nope", 404}, {"/v1/nope", 404},
	}
	for _, c := range cases {
		w := do(h, "GET", c.path)
		if w.Code != c.code {
			t.Errorf("%s: got %d want %d", c.path, w.Code, c.code)
		}
		if c.code >= 400 {
			if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
				t.Errorf("%s: content-type %q", c.path, ct)
			}
			if b := body(t, w); b["status"] != float64(c.code) || b["title"] == "" {
				t.Errorf("%s: problem body %v", c.path, b)
			}
		}
	}
}

func TestMethodNotAllowedIsProblem(t *testing.T) {
	w := do(router(t, nil), "POST", "/v1/provinsi")
	if w.Code != 405 || w.Header().Get("Content-Type") != "application/problem+json" || w.Header().Get("Allow") == "" {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
}

func TestETag304AndHeaders(t *testing.T) {
	h := router(t, nil)
	w := do(h, "GET", "/v1/provinsi")
	etag := w.Header().Get("ETag")
	if etag == "" || w.Header().Get("Cache-Control") == "" || w.Header().Get("X-Data-Version") != testStore.Meta.DataVersion {
		t.Fatalf("headers %v", w.Header())
	}
	for _, inm := range []string{etag, "W/" + etag, `"x", ` + etag, "*"} {
		w2 := do(h, "GET", "/v1/provinsi", "If-None-Match", inm)
		if w2.Code != 304 || w2.Body.Len() != 0 || w2.Header().Get("ETag") != etag || w2.Header().Get("X-Data-Version") == "" {
			t.Errorf("inm %q: %d", inm, w2.Code)
		}
	}
	if w3 := do(h, "GET", "/v1/provinsi", "If-None-Match", `"other"`); w3.Code != 200 {
		t.Fatal("mismatch must be 200")
	}
	if wh := do(h, "HEAD", "/v1/provinsi"); wh.Code != 200 {
		t.Fatalf("HEAD %d", wh.Code)
	}
}

func TestWilayahDetailAndData(t *testing.T) {
	h := router(t, nil)
	b := body(t, do(h, "GET", "/v1/wilayah/11.01.01.2001"))
	if b["kodepos"] != "23773" || b["level"] != float64(4) || len(b["induk"].([]any)) != 3 {
		t.Fatalf("%v", b)
	}
	b = body(t, do(h, "GET", "/v1/wilayah/11"))
	if b["detail"].(map[string]any)["ibukota"] != "Banda Aceh" {
		t.Fatalf("%v", b)
	}
	if n := len(body(t, do(h, "GET", "/v1/provinsi"))["data"].([]any)); n != 38 {
		t.Fatalf("provinsi %d", n)
	}
}

func TestPagination(t *testing.T) {
	h := router(t, nil)
	b := body(t, do(h, "GET", "/v1/kodepos/23773?limit=2"))
	page := b["page"].(map[string]any)
	next, _ := page["next_cursor"].(string)
	if len(b["data"].([]any)) != 2 || next == "" || page["total"].(float64) <= 2 {
		t.Fatalf("%v", b)
	}
	b2 := body(t, do(h, "GET", "/v1/kodepos/23773?limit=2&cursor="+next))
	if b2["data"].([]any)[0].(map[string]any)["kode"] == b["data"].([]any)[0].(map[string]any)["kode"] {
		t.Fatal("cursor did not advance")
	}
	s := body(t, do(h, "GET", "/v1/cari?q=aceh&limit=3"))
	if len(s["data"].([]any)) != 3 || s["page"].(map[string]any)["next_cursor"] == nil {
		t.Fatalf("%v", s)
	}
}

func TestSearchLevelFilter(t *testing.T) {
	b := body(t, do(router(t, nil), "GET", "/v1/cari?q=bakongan&level=kecamatan"))
	for _, x := range b["data"].([]any) {
		if x.(map[string]any)["level"] != float64(3) {
			t.Fatalf("%v", x)
		}
	}
	if len(b["data"].([]any)) == 0 {
		t.Fatal("no hits")
	}
}

func TestPanicRecovered(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := requestID(recoverer(log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })))
	w := do(h, "GET", "/")
	b := body(t, w)
	if w.Code != 500 || b["request_id"] == "" || strings.Contains(w.Body.String(), "boom") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestRequestID(t *testing.T) {
	h := router(t, nil)
	if got := do(h, "GET", "/healthz", "X-Request-Id", "abc-123").Header().Get("X-Request-Id"); got != "abc-123" {
		t.Fatal(got)
	}
	if got := do(h, "GET", "/healthz", "X-Request-Id", "bad id\x01").Header().Get("X-Request-Id"); len(got) != 16 {
		t.Fatalf("regenerated id %q", got)
	}
}

func TestCORS(t *testing.T) {
	h := router(t, func(c *Config) { c.CORSOrigins = []string{"https://a.example"} })
	if w := do(h, "GET", "/healthz", "Origin", "https://a.example"); w.Header().Get("Access-Control-Allow-Origin") != "https://a.example" {
		t.Fatal("allowed origin")
	}
	if w := do(h, "GET", "/healthz", "Origin", "https://evil.example"); w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("disallowed origin")
	}
	if w := do(h, "OPTIONS", "/v1/provinsi", "Origin", "https://a.example", "Access-Control-Request-Method", "GET"); w.Code != 204 {
		t.Fatalf("preflight %d", w.Code)
	}
}

func TestMethodNotAllowedAllow(t *testing.T) {
	h := router(t, nil)
	if w := do(h, "OPTIONS", "/v1/meta"); w.Code != 405 || w.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
}

func TestRateLimitEndToEnd(t *testing.T) {
	h := router(t, func(c *Config) { c.RateRPS, c.RateBurst = 0.001, 2 })
	codes := []int{do(h, "GET", "/v1/meta").Code, do(h, "GET", "/v1/meta").Code, 0}
	w := do(h, "GET", "/v1/meta", "Origin", "https://example.org")
	codes[2] = w.Code
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("429 lacks ACAO: %v", w.Header())
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 429 || w.Header().Get("Retry-After") == "" || w.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("%v %v", codes, w.Header())
	}
	if do(h, "GET", "/healthz").Code != 200 {
		t.Fatal("healthz must be exempt")
	}
}

func TestClientIP(t *testing.T) {
	nets := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	cases := []struct {
		remote, xff string
		nets        []netip.Prefix
		want        string
	}{
		{"1.2.3.4:5", "9.9.9.9", nil, "1.2.3.4"},                      // no trusted proxies: XFF ignored
		{"1.2.3.4:5", "9.9.9.9", nets, "1.2.3.4"},                     // peer not trusted: XFF ignored
		{"10.0.0.1:5", "", nets, "10.0.0.1"},                          // trusted, no header
		{"10.0.0.1:5", "5.5.5.5", nets, "5.5.5.5"},                    // normal
		{"10.0.0.1:5", "6.6.6.6, 5.5.5.5, 10.0.0.9", nets, "5.5.5.5"}, // forged left entry ignored
		{"10.0.0.1:5", "10.0.0.8, 10.0.0.9", nets, "10.0.0.1"},        // all trusted
		{"10.0.0.1:5", "5.5.5.5, garbage", nets, "10.0.0.1"},          // malformed
		{"[::ffff:10.0.0.1]:5", "::ffff:5.5.5.5", nets, "5.5.5.5"},    // mapped v4
		{"[2001:db8::1]:5", "", nil, "2001:db8::1"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := clientIP(r, c.nets).String(); got != c.want {
			t.Errorf("%s xff=%q: got %s want %s", c.remote, c.xff, got, c.want)
		}
	}
	if limitKey(netip.MustParseAddr("2001:db8::1")) != limitKey(netip.MustParseAddr("2001:db8::ffff")) {
		t.Fatal("v6 /64 grouping")
	}
}

func TestLimiterRefillAndSweep(t *testing.T) {
	now := time.Unix(1000, 0)
	l := newLimiter(1, 1)
	l.now = func() time.Time { return now }
	ip := netip.MustParseAddr("1.1.1.1")
	if ok, _ := l.allow(ip); !ok {
		t.Fatal("first")
	}
	if ok, wait := l.allow(ip); ok || wait <= 0 {
		t.Fatal("second must be denied")
	}
	now = now.Add(1100 * time.Millisecond)
	if ok, _ := l.allow(ip); !ok {
		t.Fatal("refilled")
	}
	now = now.Add(time.Hour)
	l.sweep()
	if len(l.m) != 0 {
		t.Fatal("sweep did not clean")
	}
}

func TestMetricsHandler(t *testing.T) {
	m := NewMetrics()
	m.start()
	m.done("GET", "/v1/provinsi", 200, time.Millisecond)
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(w.Body.String(), `http_requests_total{method="GET",route="/v1/provinsi",code="200"} 1`) {
		t.Fatal(w.Body.String())
	}
}

func TestLoadConfig(t *testing.T) {
	env := map[string]string{"PORT": "9000", "TRUSTED_PROXY_CIDRS": "10.0.0.0/8, 192.168.1.1", "RATE_LIMIT_RPS": "5", "METRICS_ADDR": ""}
	c, err := LoadConfig(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil || c.Port != "9000" || len(c.TrustedProxies) != 2 || c.RateRPS != 5 || c.MetricsAddr != "" {
		t.Fatalf("%+v %v", c, err)
	}
	for _, bad := range []map[string]string{{"PORT": "x"}, {"TRUSTED_PROXY_CIDRS": "nope"}, {"RATE_LIMIT_BURST": "0"}, {"REQUEST_TIMEOUT": "-1s"}} {
		if _, err := LoadConfig(func(k string) (string, bool) { v, ok := bad[k]; return v, ok }); err == nil {
			t.Errorf("expected error for %v", bad)
		}
	}
	if d, _ := LoadConfig(func(string) (string, bool) { return "", false }); d.MetricsAddr != "127.0.0.1:9090" || len(d.TrustedProxies) != 0 {
		t.Fatalf("defaults %+v", d)
	}
}

func BenchmarkHandlerWilayah(b *testing.B) {
	h := router(b, nil)
	b.ResetTimer()
	r := httptest.NewRequest("GET", "/v1/wilayah/11.01.01.2001", nil)
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
}

func BenchmarkHandlerCari(b *testing.B) {
	h := router(b, nil)
	b.ResetTimer()
	r := httptest.NewRequest("GET", "/v1/cari?q=selatan", nil)
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
}
