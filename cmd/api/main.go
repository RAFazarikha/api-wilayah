// Command api serves the read-only Wilayah API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	dataembed "api-wilayah/data"
	"api-wilayah/internal/api"
	"api-wilayah/internal/data"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe /healthz on localhost and exit (for container HEALTHCHECK)")
	flag.Parse()
	cfg, err := api.LoadConfig(os.LookupEnv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(2)
	}
	if *healthcheck {
		c := http.Client{Timeout: 3 * time.Second}
		resp, err := c.Get("http://127.0.0.1:" + cfg.Port + "/healthz")
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		return
	}
	if err := run(cfg); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(cfg api.Config) error {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		return fmt.Errorf("LOG_LEVEL: %w", err)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
	slog.SetDefault(log)

	if cfg.RateRPS > 0 && len(cfg.TrustedProxies) == 0 {
		log.Warn("TRUSTED_PROXY_CIDRS is empty: behind a reverse proxy every client shares the proxy's IP and one rate-limit bucket; set it to the proxy's IP/subnet")
	}

	st, err := data.Load(dataembed.WilayahGz)
	if err != nil {
		return fmt.Errorf("load data: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	m := api.NewMetrics()
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           api.NewRouter(ctx, cfg, st, log, m),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      cfg.RequestTimeout + 5*time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	servers := []*http.Server{srv}
	errc := make(chan error, 2)
	if cfg.MetricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", m.Handler())
		ms := &http.Server{Addr: cfg.MetricsAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
		servers = append(servers, ms)
	}
	for _, s := range servers {
		go func() {
			log.Info("listening", "addr", s.Addr, "data_version", st.Meta.DataVersion)
			if err := s.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				errc <- err
			}
		}()
	}
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	var first error
	for _, s := range servers {
		if err := s.Shutdown(sctx); err != nil && first == nil {
			first = err
		}
	}
	return first
}
