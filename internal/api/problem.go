package api

import (
	"encoding/json"
	"net/http"
)

// problem is an RFC 9457 problem details body.
type problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

func writeProblem(w http.ResponseWriter, status int, detail string) {
	b, _ := json.Marshal(problem{Type: "about:blank", Title: http.StatusText(status), Status: status, Detail: detail, RequestID: w.Header().Get("X-Request-Id")})
	h := w.Header()
	h.Set("Content-Type", "application/problem+json")
	h.Set("Cache-Control", "no-store")
	h.Del("ETag")
	w.WriteHeader(status)
	w.Write(append(b, '\n'))
}
