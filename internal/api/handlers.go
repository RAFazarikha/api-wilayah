package api

import (
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"api-wilayah/internal/data"
	"api-wilayah/internal/model"
)

// Server holds the handlers' dependencies.
type Server struct {
	store *data.Store
	log   *slog.Logger
}

type ref struct {
	Kode  string `json:"kode"`
	Nama  string `json:"nama"`
	Level int    `json:"level"`
}

type wilayahDetail struct {
	*model.Wilayah
	Induk []ref `json:"induk"`
}

func (s *Server) provinsi(w http.ResponseWriter, r *http.Request) {
	s.writeData(w, r, listResp[*model.Wilayah]{Data: nonNil(s.store.Children(""))})
}

// children serves the list of children for a path parameter that must be a region of parentLevel.
func (s *Server) children(parentLevel int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kode := chi.URLParam(r, "kode")
		if model.Level(kode) != parentLevel {
			writeProblem(w, http.StatusBadRequest, "kode must be a valid "+model.LevelNames[parentLevel]+" code")
			return
		}
		if _, ok := s.store.Get(kode); !ok {
			writeProblem(w, http.StatusNotFound, "wilayah "+kode+" not found")
			return
		}
		items := s.store.Children(kode)
		if parentLevel < 2 {
			s.writeData(w, r, listResp[*model.Wilayah]{Data: nonNil(items)})
			return
		}
		servePaged(s, w, r, items)
	}
}

func servePaged[T any](s *Server, w http.ResponseWriter, r *http.Request, items []T) {
	limit, off, msg := pageParams(r, defaultLimit, maxLimit)
	if msg != "" {
		writeProblem(w, http.StatusBadRequest, msg)
		return
	}
	resp, ok := paginate(items, limit, off)
	if !ok {
		writeProblem(w, http.StatusBadRequest, "invalid cursor")
		return
	}
	s.writeData(w, r, resp)
}

func (s *Server) wilayah(w http.ResponseWriter, r *http.Request) {
	kode := chi.URLParam(r, "kode")
	if !model.ValidKode(kode) {
		writeProblem(w, http.StatusBadRequest, "kode must look like 11, 11.01, 11.01.01 or 11.01.01.2001")
		return
	}
	wl, ok := s.store.Get(kode)
	if !ok {
		writeProblem(w, http.StatusNotFound, "wilayah "+kode+" not found")
		return
	}
	d := wilayahDetail{Wilayah: wl, Induk: []ref{}}
	for _, p := range s.store.Chain(kode) {
		d.Induk = append(d.Induk, ref{p.Kode, p.Nama, p.Level})
	}
	s.writeData(w, r, d)
}

func (s *Server) kodepos(w http.ResponseWriter, r *http.Request) {
	kp := chi.URLParam(r, "kodepos")
	if !model.ValidKodepos(kp) {
		writeProblem(w, http.StatusBadRequest, "kodepos must be 5 digits")
		return
	}
	items := s.store.ByKodepos(kp)
	if len(items) == 0 {
		writeProblem(w, http.StatusNotFound, "kodepos "+kp+" not found")
		return
	}
	servePaged(s, w, r, items)
}

func (s *Server) pulau(w http.ResponseWriter, r *http.Request) {
	kode := chi.URLParam(r, "kode")
	if l := model.Level(kode); l != 1 && l != 2 {
		writeProblem(w, http.StatusBadRequest, "kode must be a valid provinsi or kabupaten code")
		return
	}
	if _, ok := s.store.Get(kode); !ok {
		writeProblem(w, http.StatusNotFound, "wilayah "+kode+" not found")
		return
	}
	servePaged(s, w, r, s.store.Pulau(kode))
}

var levelParam = map[string]int{"1": 1, "2": 2, "3": 3, "4": 4, "provinsi": 1, "kabupaten": 2, "kecamatan": 3, "desa": 4}

const (
	maxQueryRunes = 64
	maxSearchOff  = 1000 // search is a linear scan, so deep offsets are refused
)

func (s *Server) cari(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if n := utf8.RuneCountInString(q); !utf8.ValidString(q) || n < 2 || n > maxQueryRunes {
		writeProblem(w, http.StatusBadRequest, "q must be 2 to 64 characters")
		return
	}
	level := 0
	if v := r.URL.Query().Get("level"); v != "" {
		if level = levelParam[strings.ToLower(v)]; level == 0 {
			writeProblem(w, http.StatusBadRequest, "level must be 1-4 or provinsi, kabupaten, kecamatan, desa")
			return
		}
	}
	limit, off, msg := pageParams(r, 20, 100)
	if msg == "" && off > maxSearchOff {
		msg = "invalid cursor"
	}
	if msg != "" {
		writeProblem(w, http.StatusBadRequest, msg)
		return
	}
	ctx := r.Context()
	res, done := s.store.Search(q, level, off, limit+1, func() bool { return ctx.Err() != nil })
	if !done {
		writeProblem(w, http.StatusServiceUnavailable, "search timed out")
		return
	}
	if off > 0 && len(res) == 0 { // same as the other paged endpoints: a cursor past the end is a 400
		writeProblem(w, http.StatusBadRequest, "invalid cursor")
		return
	}
	p := &pageInfo{Limit: limit}
	if len(res) > limit {
		res = res[:limit]
		if off+limit <= maxSearchOff { // never issue a cursor that this endpoint would reject
			p.NextCursor = cursor(off + limit)
		}
	}
	s.writeData(w, r, listResp[*model.Wilayah]{Data: nonNil(res), Page: p})
}

func (s *Server) meta(w http.ResponseWriter, r *http.Request) { s.writeData(w, r, s.store.Meta) }
