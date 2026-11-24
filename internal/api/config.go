package api

import (
	"fmt"
	"math"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

const minRateRPS = 0.01 // keeps Retry-After bounded

// Config is read from environment variables.
type Config struct {
	Port            string
	TrustedProxies  []netip.Prefix
	RateRPS         float64 // 0 disables rate limiting
	RateBurst       int
	CORSOrigins     []string
	RequestTimeout  time.Duration
	ShutdownTimeout time.Duration
	MetricsAddr     string // "" disables the metrics listener
	LogLevel        string
}

// LoadConfig builds a Config using lookup (os.LookupEnv in production).
func LoadConfig(lookup func(string) (string, bool)) (Config, error) {
	get := func(k, def string) string {
		if v, ok := lookup(k); ok {
			return strings.TrimSpace(v)
		}
		return def
	}
	c := Config{
		Port:        get("PORT", "8080"),
		MetricsAddr: get("METRICS_ADDR", "127.0.0.1:9090"),
		LogLevel:    get("LOG_LEVEL", "info"),
		CORSOrigins: splitList(get("CORS_ORIGINS", "*")),
	}
	if n, err := strconv.Atoi(c.Port); err != nil || n < 1 || n > 65535 {
		return c, fmt.Errorf("PORT %q invalid", c.Port)
	}
	for _, s := range splitList(get("TRUSTED_PROXY_CIDRS", "")) {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			a, err2 := netip.ParseAddr(s)
			if err2 != nil {
				return c, fmt.Errorf("TRUSTED_PROXY_CIDRS: %q is not a CIDR or IP", s)
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		if p.Addr().Is4In6() { // clientIP unmaps peers, so the prefix must be IPv4 too
			if p.Bits() < 96 {
				return c, fmt.Errorf("TRUSTED_PROXY_CIDRS: %q is an IPv4-mapped prefix shorter than /96", s)
			}
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		c.TrustedProxies = append(c.TrustedProxies, p.Masked())
	}
	var err error
	if c.RateRPS, err = strconv.ParseFloat(get("RATE_LIMIT_RPS", "10"), 64); err != nil {
		return c, fmt.Errorf("RATE_LIMIT_RPS: %w", err)
	}
	if math.IsNaN(c.RateRPS) || math.IsInf(c.RateRPS, 0) || (c.RateRPS != 0 && c.RateRPS < minRateRPS) {
		return c, fmt.Errorf("RATE_LIMIT_RPS must be 0 (disabled) or a finite number >= %v", minRateRPS)
	}
	if c.RateBurst, err = strconv.Atoi(get("RATE_LIMIT_BURST", "20")); err != nil || (c.RateRPS > 0 && c.RateBurst < 1) {
		return c, fmt.Errorf("RATE_LIMIT_BURST must be an integer >= 1")
	}
	if c.RequestTimeout, err = time.ParseDuration(get("REQUEST_TIMEOUT", "10s")); err != nil || c.RequestTimeout <= 0 {
		return c, fmt.Errorf("REQUEST_TIMEOUT must be a positive duration")
	}
	if c.ShutdownTimeout, err = time.ParseDuration(get("SHUTDOWN_TIMEOUT", "15s")); err != nil || c.ShutdownTimeout <= 0 {
		return c, fmt.Errorf("SHUTDOWN_TIMEOUT must be a positive duration")
	}
	return c, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
