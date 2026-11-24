// Command builddata converts the SQL sources into the embedded data/wilayah.json.gz.
package main

import (
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "builddata:", err)
		os.Exit(1)
	}
}

func run() error {
	var o Options
	var out string
	flag.StringVar(&o.WilayahDir, "wilayah-dir", "", "directory with wilayah.sql, wilayah_pulau.sql, wilayah_level_1_2.sql")
	flag.StringVar(&o.KodeposDir, "kodepos-dir", "", "directory with wilayah_kodepos.sql")
	flag.StringVar(&out, "out", "data/wilayah.json.gz", "output artifact")
	flag.StringVar(&o.DataVersion, "data-version", "", "data version label (default: newest 'last edit' date in source headers)")
	flag.StringVar(&o.Dasar, "dasar", "", "Kepmendagri basis label (default: from wilayah.sql header)")
	flag.BoolVar(&o.AllowCountMismatch, "allow-count-mismatch", false, "do not fail when level counts differ from 38/514/7285/83762")
	flag.Parse()
	if o.WilayahDir == "" || o.KodeposDir == "" {
		return fmt.Errorf("-wilayah-dir and -kodepos-dir are required")
	}
	o.Now = time.Now()
	ds, err := Build(o)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	tmp := out + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	zw, _ := gzip.NewWriterLevel(f, gzip.BestCompression)
	err = json.NewEncoder(zw).Encode(ds)
	if e := zw.Close(); err == nil {
		err = e
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, out); err != nil {
		return err
	}
	fmt.Printf("wrote %s version=%s dasar=%q counts=%v\n", out, ds.Meta.DataVersion, ds.Meta.Dasar, ds.Meta.Counts)
	return nil
}
