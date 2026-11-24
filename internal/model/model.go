// Package model holds the types shared by the data builder, the loader and the API.
package model

import (
	"regexp"
	"strings"
	"time"
)

var (
	kodeRe    = regexp.MustCompile(`^\d{2}(\.\d{2}(\.\d{2}(\.\d{4})?)?)?$`)
	kodeposRe = regexp.MustCompile(`^\d{5}$`)
	pulauRe   = regexp.MustCompile(`^\d{2}(\.\d{2})?\.\d{5}$`)
)

// ValidKode reports whether s is a region code (levels 1-4).
func ValidKode(s string) bool { return kodeRe.MatchString(s) }

// ValidKodepos reports whether s is a 5-digit postal code.
func ValidKodepos(s string) bool { return kodeposRe.MatchString(s) }

// ValidPulauKode reports whether s is an island code (province or regency scoped).
func ValidPulauKode(s string) bool { return pulauRe.MatchString(s) }

// Level returns 1..4 for a valid region code, 0 otherwise.
func Level(kode string) int {
	if !ValidKode(kode) {
		return 0
	}
	return strings.Count(kode, ".") + 1
}

// Induk returns the parent code of a region ("" for provinces).
func Induk(kode string) string {
	i := strings.LastIndexByte(kode, '.')
	if i < 0 {
		return ""
	}
	return kode[:i]
}

// Detail is the light detail of a province or regency (polygon excluded).
type Detail struct {
	Ibukota  string   `json:"ibukota"`
	Lat      *float64 `json:"lat"`
	Lng      *float64 `json:"lng"`
	Elv      *float64 `json:"elv"`
	Tz       *int     `json:"tz"`
	Luas     *float64 `json:"luas"`
	Penduduk *int64   `json:"penduduk"`
	Status   *int     `json:"status"`
}

// Wilayah is one region of any level.
type Wilayah struct {
	Kode    string  `json:"kode"`
	Nama    string  `json:"nama"`
	Level   int     `json:"level"`
	Kodepos string  `json:"kodepos,omitempty"`
	Detail  *Detail `json:"detail,omitempty"`
}

// Pulau is an island attached to a province or regency.
type Pulau struct {
	Kode   string   `json:"kode"`
	Nama   string   `json:"nama"`
	Lat    *float64 `json:"lat"`
	Lng    *float64 `json:"lng"`
	Status string   `json:"status"`
	Luas   *float64 `json:"luas"`
	Notes  string   `json:"notes,omitempty"`
}

// Source records one input file and its content hash.
type Source struct {
	File        string `json:"file"`
	SHA256      string `json:"sha256"`
	Kepmendagri string `json:"kepmendagri,omitempty"`
}

// Meta describes the dataset.
type Meta struct {
	DataVersion string         `json:"data_version"`
	BuildDate   time.Time      `json:"build_date"`
	Dasar       string         `json:"dasar_kepmendagri"`
	Catatan     string         `json:"catatan"`
	Counts      map[string]int `json:"counts"`
	Sources     []Source       `json:"sources"`
}

// Dataset is the embedded build artifact.
type Dataset struct {
	Meta    Meta      `json:"meta"`
	Wilayah []Wilayah `json:"wilayah"`
	Pulau   []Pulau   `json:"pulau"`
}

// Level names used in Meta.Counts and the API.
var LevelNames = [...]string{"", "provinsi", "kabupaten", "kecamatan", "desa"}

// PulauInduk returns the region an island belongs to. Codes like "21.00.40001"
// (regency part "00") are islands outside any regency and belong to the province.
func PulauInduk(kode string) string {
	p := Induk(kode)
	if strings.HasSuffix(p, ".00") {
		return p[:len(p)-3]
	}
	return p
}
