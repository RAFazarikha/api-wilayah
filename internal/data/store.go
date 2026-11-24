// Package data loads the embedded dataset into immutable in-memory indexes.
package data

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"api-wilayah/internal/model"
)

type nameEntry struct {
	lower string
	w     *model.Wilayah
}

// Store is built once by Load and never mutated, so it is safe for concurrent reads.
type Store struct {
	Meta model.Meta

	byKode   map[string]*model.Wilayah
	children map[string][]*model.Wilayah // sorted by kode
	byPos    map[string][]*model.Wilayah // sorted by kode
	byName   []nameEntry                 // sorted by lower name, then kode
	ordered  []nameEntry                 // kode order
	pulau    []model.Pulau               // sorted by kode
}

// Load decodes a gzip JSON dataset and builds the indexes. It rejects inconsistent data.
func Load(gz []byte) (*Store, error) {
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, fmt.Errorf("open dataset: %w", err)
	}
	var ds model.Dataset
	if err := json.NewDecoder(zr).Decode(&ds); err != nil {
		return nil, fmt.Errorf("decode dataset: %w", err)
	}
	s := &Store{
		Meta:     ds.Meta,
		byKode:   make(map[string]*model.Wilayah, len(ds.Wilayah)),
		children: map[string][]*model.Wilayah{},
		byPos:    map[string][]*model.Wilayah{},
		byName:   make([]nameEntry, 0, len(ds.Wilayah)),
		pulau:    ds.Pulau,
	}
	for i := range ds.Wilayah {
		w := &ds.Wilayah[i]
		if model.Level(w.Kode) != w.Level || w.Level == 0 {
			return nil, fmt.Errorf("bad kode/level %q/%d", w.Kode, w.Level)
		}
		if i > 0 && ds.Wilayah[i-1].Kode >= w.Kode { // also rejects duplicates
			return nil, fmt.Errorf("wilayah not sorted or duplicate kode at %q", w.Kode)
		}
		s.byKode[w.Kode] = w
	}
	for i := range ds.Wilayah { // ds.Wilayah is sorted by kode, so every slice below is too
		w := &ds.Wilayah[i]
		p := model.Induk(w.Kode)
		if p != "" && s.byKode[p] == nil {
			return nil, fmt.Errorf("orphan %s", w.Kode)
		}
		s.children[p] = append(s.children[p], w) // p == "" holds the provinces
		if w.Kodepos != "" {
			s.byPos[w.Kodepos] = append(s.byPos[w.Kodepos], w)
		}
		s.ordered = append(s.ordered, nameEntry{strings.ToLower(w.Nama), w})
	}
	s.byName = append([]nameEntry(nil), s.ordered...)
	sort.SliceStable(s.byName, func(i, j int) bool { return s.byName[i].lower < s.byName[j].lower })
	for i := 1; i < len(s.pulau); i++ {
		if s.pulau[i-1].Kode >= s.pulau[i].Kode {
			return nil, fmt.Errorf("pulau not sorted or duplicate kode at %q", s.pulau[i].Kode)
		}
	}
	return s, nil
}

// Get returns a region by code.
func (s *Store) Get(kode string) (*model.Wilayah, bool) {
	w, ok := s.byKode[kode]
	return w, ok
}

// Children returns the direct children of kode ("" = provinces), sorted by kode.
func (s *Store) Children(kode string) []*model.Wilayah { return s.children[kode] }

// Chain returns the ancestors of kode from province down (excluding kode itself).
func (s *Store) Chain(kode string) []*model.Wilayah {
	var out []*model.Wilayah
	for p := model.Induk(kode); p != ""; p = model.Induk(p) {
		out = append([]*model.Wilayah{s.byKode[p]}, out...)
	}
	return out
}

// ByKodepos returns the villages with a postal code, sorted by kode.
func (s *Store) ByKodepos(kp string) []*model.Wilayah { return s.byPos[kp] }

// Pulau returns islands under a province or regency, sorted by kode.
func (s *Store) Pulau(kode string) []model.Pulau {
	var out []model.Pulau
	for _, p := range s.pulau {
		if k := model.PulauInduk(p.Kode); k == kode || strings.HasPrefix(k, kode+".") {
			out = append(out, p)
		}
	}
	return out
}

// Search returns up to max regions whose name matches q (already lowercased/trimmed),
// skipping the first skip hits. level 0 means any level. Exact/prefix matches come first
// (sorted by name), then the remaining substring matches in kode order. done is false when
// the scan was cut short by stop().
func (s *Store) Search(q string, level, skip, max int, stop func() bool) (res []*model.Wilayah, done bool) {
	hits := 0
	// add counts a hit and keeps it only once past skip, so a large skip allocates nothing.
	add := func(w *model.Wilayah) (full bool) {
		if hits++; hits > skip {
			res = append(res, w)
		}
		return len(res) >= max
	}
	i := sort.Search(len(s.byName), func(i int) bool { return s.byName[i].lower >= q })
	for ; i < len(s.byName) && strings.HasPrefix(s.byName[i].lower, q); i++ {
		if e := s.byName[i]; (level == 0 || e.w.Level == level) && add(e.w) {
			return res, true
		}
	}
	// shortcut: linear substring scan over ~92k names (~1ms); upgrade to n-gram index if it gets hot.
	for n, w := range s.ordered {
		if n&4095 == 0 && stop != nil && stop() {
			return nil, false
		}
		if (level == 0 || w.w.Level == level) && !strings.HasPrefix(w.lower, q) && strings.Contains(w.lower, q) && add(w.w) {
			break
		}
	}
	return res, true
}
