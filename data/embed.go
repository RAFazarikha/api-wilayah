// Package dataembed embeds the build artifact produced by cmd/builddata.
package dataembed

import _ "embed"

// WilayahGz is the gzip-compressed JSON dataset.
//
//go:embed wilayah.json.gz
var WilayahGz []byte
