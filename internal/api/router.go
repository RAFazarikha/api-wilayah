package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	apispec "api-wilayah/api"
	"api-wilayah/internal/data"
)

// NewRouter wires the public API. ctx stops the rate limiter's background sweeper.
func NewRouter(ctx context.Context, cfg Config, st *data.Store, log *slog.Logger, m *Metrics) http.Handler {
	s := &Server{store: st, log: log}
	r := chi.NewRouter()
	ipOf := func(r *http.Request) string { return clientIP(r, cfg.TrustedProxies).String() }
	r.Use(requestID, observe(log, m, ipOf), recoverer(log), nosniff)
	// cors headers come before the limiter so 429s carry them; preflight is answered after it so it is counted too.
	r.Use(cors(cfg.CORSOrigins))
	if cfg.RateRPS > 0 {
		l := newLimiter(cfg.RateRPS, cfg.RateBurst)
		go l.runSweeper(ctx)
		r.Use(rateLimit(l, cfg.TrustedProxies))
	}
	r.Use(preflight, timeout(cfg.RequestTimeout), middleware.GetHead)

	r.NotFound(func(w http.ResponseWriter, r *http.Request) { writeProblem(w, http.StatusNotFound, "route not found") })
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", "GET, HEAD")
		writeProblem(w, http.StatusMethodNotAllowed, "method not allowed")
	})

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Write([]byte(`{"status":"ok"}` + "\n"))
	})
	r.Get("/docs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
		w.Write([]byte(docsHTML))
	})
	r.Get("/docs/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		w.Write(apispec.OpenAPI)
	})

	r.Route("/v1", func(r chi.Router) {
		r.Get("/meta", s.meta)
		r.Get("/provinsi", s.provinsi)
		r.Get("/provinsi/{kode}/kabupaten", s.children(1))
		r.Get("/kabupaten/{kode}/kecamatan", s.children(2))
		r.Get("/kecamatan/{kode}/desa", s.children(3))
		r.Get("/wilayah/{kode}", s.wilayah)
		r.Get("/wilayah/{kode}/pulau", s.pulau)
		r.Get("/kodepos/{kodepos}", s.kodepos)
		r.Get("/cari", s.cari)
	})
	return r
}

const docsHTML = `<!doctype html>
<html lang="id"><head><meta charset="utf-8"><title>API Wilayah Indonesia</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:48rem;margin:2rem auto;padding:0 1rem}code{background:#eee;padding:0 .25em}td{padding:.2rem .8rem .2rem 0;vertical-align:top}</style></head>
<body><h1>API Wilayah Indonesia v1</h1>
<p>Spesifikasi lengkap (OpenAPI 3.1): <a href="/docs/openapi.yaml">/docs/openapi.yaml</a>. Semua respons data membawa <code>ETag</code>, <code>Cache-Control</code>, dan <code>X-Data-Version</code>; error memakai <code>application/problem+json</code>.</p>
<table>
<tr><td><code>GET /v1/provinsi</code></td><td>Daftar provinsi</td></tr>
<tr><td><code>GET /v1/provinsi/{kode}/kabupaten</code></td><td>Kabupaten/kota dalam provinsi</td></tr>
<tr><td><code>GET /v1/kabupaten/{kode}/kecamatan</code></td><td>Kecamatan (limit, cursor)</td></tr>
<tr><td><code>GET /v1/kecamatan/{kode}/desa</code></td><td>Desa/kelurahan (limit, cursor)</td></tr>
<tr><td><code>GET /v1/wilayah/{kode}</code></td><td>Detail satu wilayah beserta rantai induk</td></tr>
<tr><td><code>GET /v1/wilayah/{kode}/pulau</code></td><td>Pulau di provinsi atau kabupaten/kota</td></tr>
<tr><td><code>GET /v1/kodepos/{kodepos}</code></td><td>Desa dengan kodepos tersebut</td></tr>
<tr><td><code>GET /v1/cari?q=&amp;level=&amp;limit=&amp;cursor=</code></td><td>Cari nama wilayah</td></tr>
<tr><td><code>GET /v1/meta</code></td><td>Versi data, dasar Kepmendagri, jumlah, tanggal build</td></tr>
<tr><td><code>GET /healthz</code></td><td>Kesehatan service</td></tr>
</table></body></html>
`
