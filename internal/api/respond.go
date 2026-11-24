package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

const (
	defaultLimit = 100
	maxLimit     = 500
	maxCursorOff = 1_000_000
)

type pageInfo struct {
	Limit      int    `json:"limit"`
	Total      *int   `json:"total,omitempty"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type listResp[T any] struct {
	Data []T       `json:"data"`
	Page *pageInfo `json:"page,omitempty"`
}

// nonNil makes an empty list encode as [] instead of null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// writeData sends a JSON data response with a strong content-hash ETag and answers 304 on a match.
func (s *Server) writeData(w http.ResponseWriter, r *http.Request, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		s.log.Error("marshal response", "err", err)
		writeProblem(w, http.StatusInternalServerError, "")
		return
	}
	sum := sha256.Sum256(b)
	etag := `"` + base64.RawURLEncoding.EncodeToString(sum[:12]) + `"`
	h := w.Header()
	h.Set("ETag", etag)
	h.Set("Cache-Control", "public, max-age=3600")
	h.Set("X-Data-Version", s.store.Meta.DataVersion)
	if etagMatch(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(len(b)+1))
	w.Write(append(b, '\n'))
}

func etagMatch(header, etag string) bool {
	for _, c := range strings.Split(header, ",") {
		c = strings.TrimPrefix(strings.TrimSpace(c), "W/")
		if c == "*" || c == etag {
			return true
		}
	}
	return false
}

// pageParams validates limit and cursor. The cursor is an opaque base64url offset.
func pageParams(r *http.Request, defLimit, maxLim int) (limit, off int, err string) {
	q := r.URL.Query()
	limit = defLimit
	if v := q.Get("limit"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > maxLim {
			return 0, 0, "limit must be an integer between 1 and " + strconv.Itoa(maxLim)
		}
		limit = n
	}
	if v := q.Get("cursor"); v != "" {
		b, e := base64.RawURLEncoding.DecodeString(v)
		n, e2 := strconv.Atoi(strings.TrimPrefix(string(b), "o:"))
		if e != nil || !strings.HasPrefix(string(b), "o:") || e2 != nil || n < 0 || n > maxCursorOff {
			return 0, 0, "invalid cursor"
		}
		off = n
	}
	return limit, off, ""
}

func cursor(off int) string {
	return base64.RawURLEncoding.EncodeToString([]byte("o:" + strconv.Itoa(off)))
}

// paginate slices items by offset; ok is false when the cursor points past the end.
func paginate[T any](items []T, limit, off int) (listResp[T], bool) {
	if off > len(items) {
		return listResp[T]{}, false
	}
	end := min(off+limit, len(items))
	total := len(items)
	p := &pageInfo{Limit: limit, Total: &total}
	if end < len(items) {
		p.NextCursor = cursor(end)
	}
	return listResp[T]{Data: append([]T{}, items[off:end]...), Page: p}, true
}
