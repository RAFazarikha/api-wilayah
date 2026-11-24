package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"api-wilayah/internal/model"
)

// ExpectedCounts are the verified totals per level (PLAN section 3).
var ExpectedCounts = [5]int{0, 38, 514, 7285, 83762}

const maxReported = 20

type sourceFile struct {
	name string
	text string
	meta model.Source
	edit string // "last edit" date from the header
}

var (
	kepRe  = regexp.MustCompile(`(?i)(?:Kepmendagri|Keputusan Menteri Dalam Negeri)\s+(?:No\.?|Nomor)\s*([0-9.]+-\d+)\s+Tahun\s+(\d{4})`)
	editRe = regexp.MustCompile(`last edit:\s*(\d{4}-\d{2}-\d{2})`)
)

func readSource(dir, name string) (*sourceFile, error) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	s := &sourceFile{name: name, text: string(b)}
	s.meta = model.Source{File: name, SHA256: hex.EncodeToString(sum[:])}
	head := s.text[:min(len(s.text), 2048)]
	if m := kepRe.FindStringSubmatch(head); m != nil {
		s.meta.Kepmendagri = fmt.Sprintf("Kepmendagri No %s Tahun %s", m[1], m[2])
	}
	if m := editRe.FindStringSubmatch(head); m != nil {
		s.edit = m[1]
	}
	return s, nil
}

// table returns the rows of the single INSERT target table, mapped by column name.
func table(src *sourceFile, want string, cols ...string) ([]map[string]Value, error) {
	ins, err := ParseInserts(src.text)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", src.name, err)
	}
	var rows []map[string]Value
	for _, in := range ins {
		if !strings.EqualFold(in.Table, want) {
			return nil, fmt.Errorf("%s: unexpected table %q", src.name, in.Table)
		}
		idx := map[string]int{}
		for i, c := range in.Cols {
			idx[strings.ToLower(c)] = i
		}
		for _, c := range cols {
			if _, ok := idx[c]; !ok {
				return nil, fmt.Errorf("%s: missing column %q", src.name, c)
			}
		}
		for _, r := range in.Rows {
			m := make(map[string]Value, len(idx))
			for c, i := range idx {
				m[c] = r[i]
			}
			rows = append(rows, m)
		}
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%s: no rows", src.name)
	}
	return rows, nil
}

func str(v Value) string { return v.S }

func num(v Value) (*float64, error) {
	if v.Kind == KNull || (v.Kind == KString && v.S == "") {
		return nil, nil
	}
	f, err := strconv.ParseFloat(v.S, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid number %q", v.S)
	}
	return &f, nil
}

type errs struct{ list []error }

func (e *errs) add(format string, a ...any) { e.list = append(e.list, fmt.Errorf(format, a...)) }
func (e *errs) err() error {
	if len(e.list) == 0 {
		return nil
	}
	l := e.list
	if len(l) > maxReported {
		l = append(l[:maxReported:maxReported], fmt.Errorf("... and %d more", len(e.list)-maxReported))
	}
	return errors.Join(l...)
}

// Options controls Build.
type Options struct {
	WilayahDir, KodeposDir string
	AllowCountMismatch     bool
	DataVersion, Dasar     string
	Now                    time.Time
}

// Build parses, validates and assembles the dataset.
func Build(o Options) (*model.Dataset, error) {
	var srcs [4]*sourceFile
	for i, p := range [][2]string{{o.WilayahDir, "wilayah.sql"}, {o.KodeposDir, "wilayah_kodepos.sql"}, {o.WilayahDir, "wilayah_pulau.sql"}, {o.WilayahDir, "wilayah_level_1_2.sql"}} {
		s, err := readSource(p[0], p[1])
		if err != nil {
			return nil, err
		}
		srcs[i] = s
	}
	wsrc, ksrc, psrc, dsrc := srcs[0], srcs[1], srcs[2], srcs[3]

	wrows, err := table(wsrc, "wilayah", "kode", "nama")
	if err != nil {
		return nil, err
	}
	krows, err := table(ksrc, "wilayah_kodepos", "kode", "kodepos")
	if err != nil {
		return nil, err
	}
	prows, err := table(psrc, "wilayah_pulau", "kode", "nama", "lat", "lng", "status") // luas, notes absent in one INSERT
	if err != nil {
		return nil, err
	}
	drows, err := table(dsrc, "wilayah_level_1_2", "kode", "nama", "ibukota", "lat", "lng", "elv", "tz", "luas", "penduduk", "status")
	if err != nil {
		return nil, err
	}

	var e errs
	byKode := make(map[string]*model.Wilayah, len(wrows))
	var counts [5]int
	for _, r := range wrows {
		k, n := str(r["kode"]), strings.TrimSpace(str(r["nama"]))
		lv := model.Level(k)
		switch {
		case lv == 0:
			e.add("wilayah: invalid kode %q", k)
			continue
		case n == "":
			e.add("wilayah: empty nama for %s", k)
			continue
		case byKode[k] != nil:
			e.add("wilayah: duplicate kode %s", k)
			continue
		}
		byKode[k] = &model.Wilayah{Kode: k, Nama: n, Level: lv}
		counts[lv]++
	}
	for k := range byKode {
		if p := model.Induk(k); p != "" && byKode[p] == nil {
			e.add("wilayah: parent %s of %s missing", p, k)
		}
	}
	if !o.AllowCountMismatch {
		for lv := 1; lv <= 4; lv++ {
			if counts[lv] != ExpectedCounts[lv] {
				e.add("count %s = %d, want %d (use -allow-count-mismatch to override)", model.LevelNames[lv], counts[lv], ExpectedCounts[lv])
			}
		}
	}

	seenPos := map[string]bool{}
	for _, r := range krows {
		k, p := str(r["kode"]), str(r["kodepos"])
		w := byKode[k]
		switch {
		case w == nil || w.Level != 4:
			e.add("kodepos: kode %q is not a known desa", k)
		case !model.ValidKodepos(p):
			e.add("kodepos: %s has invalid kodepos %q", k, p)
		case seenPos[k]:
			e.add("kodepos: duplicate kode %s", k)
		default:
			w.Kodepos = p
		}
		seenPos[k] = true
	}
	for k, w := range byKode {
		if w.Level == 4 && w.Kodepos == "" {
			e.add("kodepos: desa %s has no kodepos", k)
		}
	}

	seenDetail := map[string]bool{}
	for _, r := range drows {
		k := str(r["kode"])
		w := byKode[k]
		if w == nil || w.Level > 2 {
			e.add("level_1_2: kode %q is not a province/regency", k)
			continue
		}
		if seenDetail[k] {
			e.add("level_1_2: duplicate kode %s", k)
			continue
		}
		seenDetail[k] = true
		d := &model.Detail{Ibukota: str(r["ibukota"])}
		var perr error
		pf := func(c string) *float64 {
			f, err := num(r[c])
			if err != nil {
				perr = fmt.Errorf("level_1_2: %s.%s: %w", k, c, err)
			}
			return f
		}
		d.Lat, d.Lng, d.Elv, d.Luas = pf("lat"), pf("lng"), pf("elv"), pf("luas")
		whole := func(c string) *int64 { // integers must not be silently truncated
			f := pf(c)
			if f == nil {
				return nil
			}
			if *f != math.Trunc(*f) || math.Abs(*f) > 1<<53 {
				perr = fmt.Errorf("level_1_2: %s.%s: %v is not an integer", k, c, *f)
				return nil
			}
			n := int64(*f)
			return &n
		}
		tz, st := whole("tz"), whole("status")
		d.Penduduk = whole("penduduk")
		if tz != nil {
			t := int(*tz)
			d.Tz = &t
		}
		if st != nil {
			t := int(*st)
			d.Status = &t
		}
		if perr != nil {
			e.add("%v", perr)
			continue
		}
		w.Detail = d
	}

	pulau := make([]model.Pulau, 0, len(prows))
	seenPulau := map[string]bool{}
	for _, r := range prows {
		k := str(r["kode"])
		if !model.ValidPulauKode(k) {
			e.add("pulau: invalid kode %q", k)
			continue
		}
		if byKode[model.PulauInduk(k)] == nil {
			e.add("pulau: parent of %s missing", k)
			continue
		}
		if seenPulau[k] {
			e.add("pulau: duplicate kode %s", k)
			continue
		}
		seenPulau[k] = true
		lat, err1 := num(r["lat"])
		lng, err2 := num(r["lng"])
		luas, err3 := num(r["luas"])
		if err := errors.Join(err1, err2, err3); err != nil {
			e.add("pulau %s: %v", k, err)
			continue
		}
		pulau = append(pulau, model.Pulau{Kode: k, Nama: str(r["nama"]), Lat: lat, Lng: lng, Status: str(r["status"]), Luas: luas, Notes: str(r["notes"])})
	}
	if err := e.err(); err != nil {
		return nil, err
	}

	ws := make([]model.Wilayah, 0, len(byKode))
	for _, w := range byKode {
		ws = append(ws, *w)
	}
	sort.Slice(ws, func(i, j int) bool { return ws[i].Kode < ws[j].Kode })
	sort.Slice(pulau, func(i, j int) bool { return pulau[i].Kode < pulau[j].Kode })

	latest := ""
	for _, s := range srcs {
		latest = max(latest, s.edit)
	}
	version := o.DataVersion
	if version == "" {
		version = latest
	}
	dasar := o.Dasar
	if dasar == "" {
		dasar = wsrc.meta.Kepmendagri
	}
	cm := map[string]int{"pulau": len(pulau)}
	for lv := 1; lv <= 4; lv++ {
		cm[model.LevelNames[lv]] = counts[lv]
	}
	note := fmt.Sprintf("Label dasar diambil dari header %s (%s); header %s menyebut %s. "+
		"Versi data = tanggal 'last edit' sumber terbaru kecuali di-override; tidak mengklaim kesesuaian dengan Kepmendagri yang lebih baru.",
		wsrc.name, orNone(wsrc.meta.Kepmendagri), psrc.name, orNone(psrc.meta.Kepmendagri))
	ds := &model.Dataset{
		Meta: model.Meta{DataVersion: version, BuildDate: o.Now.UTC().Truncate(time.Second), Dasar: dasar, Catatan: note, Counts: cm,
			Sources: []model.Source{wsrc.meta, ksrc.meta, psrc.meta, dsrc.meta}},
		Wilayah: ws, Pulau: pulau,
	}
	if version == "" || dasar == "" {
		return nil, errors.New("data version or dasar kepmendagri undetermined; pass -data-version and -dasar")
	}
	return ds, nil
}

func orNone(s string) string {
	if s == "" {
		return "tidak ada"
	}
	return s
}
