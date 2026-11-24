package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const hdr = "/*\nnote : Kepmendagri No 300.2.2-2138 Tahun 2025\nlast edit: 2026-01-02 03:04:05\n*/\n"

func fixture(t *testing.T, wil, pos string) Options {
	t.Helper()
	d := t.TempDir()
	w := func(n, s string) {
		if err := os.WriteFile(filepath.Join(d, n), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w("wilayah.sql", hdr+"INSERT INTO wilayah (kode, nama)\nVALUES\n"+wil+";")
	w("wilayah_kodepos.sql", "INSERT INTO wilayah_kodepos(kode,kodepos)\nVALUES\n"+pos+";")
	w("wilayah_pulau.sql", "INSERT INTO wilayah_pulau(kode,nama,lat,lng,status)\nVALUES\n('11.01.40001','P','3.3','97.1','TBP'),('11.00.40002','Q','1','2','TBP');")
	w("wilayah_level_1_2.sql", "INSERT INTO `wilayah_level_1_2`(`kode`,`nama`,`ibukota`,`lat`,`lng`,`elv`,`tz`,`luas`,`penduduk`,`path`,`status`) VALUES\n('11','Aceh','Banda', 5.5, 95.3, 11, 7, 56835.019, 5623479, '[[1,2]]', 1);")
	return Options{WilayahDir: d, KodeposDir: d, AllowCountMismatch: true, Now: time.Unix(0, 0)}
}

const okWil = "('11','Aceh'),('11.01','Kab A'),('11.01.01','Kec'),('11.01.01.2001','Desa')"

func TestBuildOK(t *testing.T) {
	ds, err := Build(fixture(t, okWil, "('11.01.01.2001', '23773')"))
	if err != nil {
		t.Fatal(err)
	}
	if ds.Meta.DataVersion != "2026-01-02" || ds.Meta.Dasar != "Kepmendagri No 300.2.2-2138 Tahun 2025" {
		t.Fatalf("meta %+v", ds.Meta)
	}
	if len(ds.Wilayah) != 4 || ds.Wilayah[3].Kodepos != "23773" || ds.Wilayah[0].Detail == nil || *ds.Wilayah[0].Detail.Penduduk != 5623479 {
		t.Fatalf("wilayah %+v", ds.Wilayah)
	}
	if len(ds.Pulau) != 2 || ds.Meta.Counts["desa"] != 1 || len(ds.Meta.Sources[0].SHA256) != 64 {
		t.Fatalf("pulau/meta %+v", ds.Meta)
	}
}

func TestBuildFailures(t *testing.T) {
	pos := "('11.01.01.2001', '23773')" // keeps kodepos.sql parseable so each case fails for its own reason
	cases := map[string][3]string{      // wilayah rows, kodepos rows, expected error substring
		"orphan parent":   {"('11','A'),('11.01.01','K')", pos, "parent 11.01 of 11.01.01 missing"},
		"bad kode":        {"('1','A')", pos, `invalid kode "1"`},
		"duplicate":       {"('11','A'),('11','B')", pos, "duplicate kode 11"},
		"desa no kodepos": {okWil + ",('11.01.01.2002','D2')", "('11.01.01.2001', '23773')", "desa 11.01.01.2002 has no kodepos"},
		"bad kodepos":     {okWil, "('11.01.01.2001', '2377')", `invalid kodepos "2377"`},
		"yatim kodepos":   {okWil, "('11.01.01.2001', '23773'),('99.01.01.2001', '23773')", `kode "99.01.01.2001" is not a known desa`},
	}
	for name, c := range cases {
		if _, err := Build(fixture(t, c[0], c[1])); err == nil || !strings.Contains(err.Error(), c[2]) {
			t.Errorf("%s: err=%v, want substring %q", name, err, c[2])
		}
	}
}

func TestBuildCountMismatchFailsHard(t *testing.T) {
	o := fixture(t, okWil, "('11.01.01.2001', '23773')")
	o.AllowCountMismatch = false
	_, err := Build(o)
	if err == nil || !strings.Contains(err.Error(), "count provinsi = 1, want 38") {
		t.Fatalf("err=%v", err)
	}
}

func TestBuildLevel12Strictness(t *testing.T) {
	const pre = "INSERT INTO `wilayah_level_1_2`(`kode`,`nama`,`ibukota`,`lat`,`lng`,`elv`,`tz`,`luas`,`penduduk`,`status`) VALUES\n"
	row := func(tz, pend, status string) string {
		return "('11','Aceh','Banda', 5.5, 95.3, 11, " + tz + ", 56835.019, " + pend + ", " + status + ")"
	}
	cases := map[string][2]string{ // rows, expected error substring
		"duplicate row":       {row("7", "1", "1") + "," + row("7", "1", "1"), "duplicate kode 11"},
		"fractional tz":       {row("7.5", "1", "1"), "11.tz: 7.5 is not an integer"},
		"fractional penduduk": {row("7", "1.5", "1"), "11.penduduk: 1.5 is not an integer"},
		"fractional status":   {row("7", "1", "1.5"), "11.status: 1.5 is not an integer"},
	}
	for name, c := range cases {
		vals := c[0]
		o := fixture(t, okWil, "('11.01.01.2001', '23773')")
		if err := os.WriteFile(filepath.Join(o.WilayahDir, "wilayah_level_1_2.sql"), []byte(pre+vals+";"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Build(o); err == nil || !strings.Contains(err.Error(), c[1]) {
			t.Errorf("%s: err=%v, want substring %q", name, err, c[1])
		}
	}
	// whole numbers written as floats are still accepted
	o := fixture(t, okWil, "('11.01.01.2001', '23773')")
	if err := os.WriteFile(filepath.Join(o.WilayahDir, "wilayah_level_1_2.sql"), []byte(pre+row("7.0", "100", "1")+";"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ds, err := Build(o); err != nil || *ds.Wilayah[0].Detail.Tz != 7 {
		t.Fatalf("whole float rejected: %v", err)
	}
}
