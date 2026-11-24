// Package apispec embeds the OpenAPI document.
package apispec

import _ "embed"

// OpenAPI is the YAML specification served at /docs/openapi.yaml.
//
//go:embed openapi.yaml
var OpenAPI []byte
