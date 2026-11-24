package api

import (
	"context"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxRetryAfter = time.Hour

type bucket struct {
	tokens float64
	last   time.Time
}

type limiter struct {
	mu     sync.Mutex
	m      map[netip.Addr]*bucket
	rps    float64
	burst  float64
	now    func() time.Time
	idleTT time.Duration
}

func newLimiter(rps float64, burst int) *limiter {
	return &limiter{m: map[netip.Addr]*bucket{}, rps: rps, burst: float64(burst), now: time.Now, idleTT: 5 * time.Minute}
}

// allow takes one token for ip; when denied it returns the wait until one is available.
func (l *limiter) allow(ip netip.Addr) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b := l.m[ip]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.m[ip] = b
	}
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rps)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration(math.Min((1-b.tokens)/l.rps, maxRetryAfter.Seconds()) * float64(time.Second))
}

// sweep drops buckets idle for longer than idleTT so the map cannot grow without bound.
func (l *limiter) sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := l.now().Add(-l.idleTT)
	for k, b := range l.m {
		if b.last.Before(cut) {
			delete(l.m, k)
		}
	}
}

func (l *limiter) runSweeper(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.sweep()
		}
	}
}

func trusted(ip netip.Addr, nets []netip.Prefix) bool {
	for _, p := range nets {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP returns the caller address. X-Forwarded-For is honoured only when the TCP peer
// is a trusted proxy; the chain is then walked from the right and the first address that
// is not itself a trusted proxy wins, so entries forged by the client (on the left) are ignored.
func clientIP(r *http.Request, nets []netip.Prefix) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	peer = peer.Unmap()
	if len(nets) == 0 || !trusted(peer, nets) {
		return peer
	}
	parts := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
		if err != nil {
			return peer // malformed chain: do not guess
		}
		if a = a.Unmap(); !trusted(a, nets) {
			return a
		}
	}
	return peer
}

// limitKey groups IPv6 clients by /64 so one host cannot rotate through its whole prefix.
func limitKey(ip netip.Addr) netip.Addr {
	if ip.Is6() {
		return netip.PrefixFrom(ip, 64).Masked().Addr()
	}
	return ip
}

func rateLimit(l *limiter, nets []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.EscapedPath() == "/healthz" {
				next.ServeHTTP(w, r)
				return
			}
			if ok, wait := l.allow(limitKey(clientIP(r, nets))); !ok {
				w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(wait.Seconds())))))
				writeProblem(w, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
