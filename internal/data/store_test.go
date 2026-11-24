package data

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"strings"
	"testing"

	dataembed "api-wilayah/data"
	"api-wilayah/internal/model"
)

func load(tb testing.TB) *Store {
	tb.Helper()
	s, err := Load(dataembed.WilayahGz)
	if err != nil {
		tb.Fatal(err)
	}
	return s
}

func TestCountsAndNoOrphans(t *testing.T) {
	s := load(t)
	want := map[string]int{"provinsi": 38, "kabupaten": 514, "kecamatan": 7285, "desa": 83762}
	got := map[string]int{}
	for k, w := range s.byKode {
		got[model.LevelNames[w.Level]]++
		if p := model.Induk(k); p != "" {
			if _, ok := s.byKode[p]; !ok {
				t.Fatalf("orphan %s", k)
			}
		}
		if w.Level == 4 && !model.ValidKodepos(w.Kodepos) {
			t.Fatalf("desa %s kodepos %q", k, w.Kodepos)
		}
	}
	for l, n := range want {
		if got[l] != n || s.Meta.Counts[l] != n {
			t.Errorf("%s: got %d meta %d want %d", l, got[l], s.Meta.Counts[l], n)
		}
	}
	if len(s.Children("")) != 38 {
		t.Errorf("provinces %d", len(s.Children("")))
	}
	for _, p := range s.pulau {
		if _, ok := s.byKode[model.PulauInduk(p.Kode)]; !ok {
			t.Fatalf("pulau orphan %s", p.Kode)
		}
	}
}

func TestLookups(t *testing.T) {
	s := load(t)
	w, ok := s.Get("11.01.01.2001")
	if !ok || w.Kodepos != "23773" {
		t.Fatalf("get %+v", w)
	}
	if c := s.Chain(w.Kode); len(c) != 3 || c[0].Kode != "11" || c[2].Kode != "11.01.01" {
		t.Fatalf("chain %+v", c)
	}
	kids := s.Children("11.01.01")
	for i := 1; i < len(kids); i++ {
		if kids[i-1].Kode >= kids[i].Kode {
			t.Fatal("children not sorted")
		}
	}
	if len(s.ByKodepos("23773")) == 0 || len(s.Pulau("11.01")) == 0 || len(s.Pulau("11")) < len(s.Pulau("11.01")) {
		t.Fatal("kodepos/pulau lookup")
	}
	if d := mustGet(t, s, "11").Detail; d == nil || d.Ibukota != "Banda Aceh" {
		t.Fatalf("detail %+v", d)
	}
}

func mustGet(t *testing.T, s *Store, k string) *model.Wilayah {
	w, ok := s.Get(k)
	if !ok {
		t.Fatal(k)
	}
	return w
}

func TestSearch(t *testing.T) {
	s := load(t)
	res, done := s.Search("bakongan", 3, 0, 10, nil)
	if !done || len(res) == 0 || res[0].Nama != "Bakongan" {
		t.Fatalf("res %v", res)
	}
	all, _ := s.Search("aceh selatan", 0, 0, 5, nil)
	if len(all) == 0 {
		t.Fatal("substring match missing")
	}
	page2, _ := s.Search("aceh", 0, 2, 3, nil)
	full, _ := s.Search("aceh", 0, 0, 5, nil)
	if len(page2) != 3 || page2[0] != full[2] {
		t.Fatal("skip/offset inconsistent")
	}
	if r, _ := s.Search("zzzzqqq", 0, 0, 5, nil); len(r) != 0 {
		t.Fatal("expected empty")
	}
}

func BenchmarkGet(b *testing.B) {
	s := load(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Get("11.01.01.2001")
	}
}

func BenchmarkSearchPrefix(b *testing.B) {
	s := load(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Search("bakongan", 0, 0, 20, nil)
	}
}

func BenchmarkSearchSubstring(b *testing.B) {
	s := load(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Search("selatan", 0, 0, 20, nil)
	}
}

func TestSearchDeepSkipAllocatesLittle(t *testing.T) {
	s := load(t)
	full, _ := s.Search("a", 0, 0, 1050, nil)
	res, done := s.Search("a", 0, 1000, 21, nil)
	if !done || len(res) != 21 || res[0] != full[1000] {
		t.Fatalf("done=%v len=%d", done, len(res))
	}
	// only the returned page may be stored; keeping the skipped 1000 hits would need ~11 slice growths
	if n := testing.AllocsPerRun(5, func() { s.Search("a", 0, 1000, 21, nil) }); n > 8 {
		t.Fatalf("%v allocs", n)
	}
}

func TestLoadRejectsDuplicatePulau(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	p := model.Pulau{Kode: "11.01.40001", Nama: "P"}
	json.NewEncoder(zw).Encode(model.Dataset{Wilayah: []model.Wilayah{{Kode: "11", Nama: "A", Level: 1}}, Pulau: []model.Pulau{p, p}})
	zw.Close()
	if _, err := Load(buf.Bytes()); err == nil || !strings.Contains(err.Error(), "pulau") {
		t.Fatalf("err=%v", err)
	}
}

func TestLoadRejectsUnsortedAndDuplicate(t *testing.T) {
	enc := func(ws ...model.Wilayah) []byte {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		json.NewEncoder(zw).Encode(model.Dataset{Wilayah: ws})
		zw.Close()
		return buf.Bytes()
	}
	a, b := model.Wilayah{Kode: "11", Nama: "A", Level: 1}, model.Wilayah{Kode: "12", Nama: "B", Level: 1}
	if _, err := Load(enc(a, b)); err != nil {
		t.Fatalf("sorted data rejected: %v", err)
	}
	if _, err := Load(enc(b, a)); err == nil {
		t.Error("unsorted must be rejected")
	}
	if _, err := Load(enc(a, a)); err == nil {
		t.Error("duplicate must be rejected")
	}
}
