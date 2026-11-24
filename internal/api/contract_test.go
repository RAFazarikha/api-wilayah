package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"

	apispec "api-wilayah/api"
)

// Minimal contract check: every spec path is routed, every routed /v1 path and
// /healthz is in the spec, and sample responses use documented statuses and carry the required fields.

func loadSpec(t *testing.T) map[string]any {
	var spec map[string]any
	if err := yaml.Unmarshal(apispec.OpenAPI, &spec); err != nil {
		t.Fatal(err)
	}
	return spec
}

func at(m any, keys ...string) any {
	for _, k := range keys {
		mm, ok := m.(map[string]any)
		if !ok {
			return nil
		}
		m = mm[k]
	}
	return m
}

func resolve(spec map[string]any, s any) map[string]any {
	m, _ := s.(map[string]any)
	if ref, ok := m["$ref"].(string); ok {
		return resolve(spec, at(spec, strings.Split(strings.TrimPrefix(ref, "#/"), "/")...))
	}
	return m
}

// check validates v against schema: required keys, object properties, array items, allOf, basic types.
func check(spec map[string]any, schema any, v any, path string) error {
	s := resolve(spec, schema)
	if all, ok := s["allOf"].([]any); ok {
		for _, sub := range all {
			if err := check(spec, sub, v, path); err != nil {
				return err
			}
		}
	}
	switch typ := s["type"].(type) {
	case string:
		if err := checkType(typ, v, path); err != nil {
			return err
		}
	case []any:
		okType := false
		for _, x := range typ {
			okType = okType || checkType(x.(string), v, path) == nil
		}
		if !okType {
			return fmt.Errorf("%s: %v does not match types %v", path, v, typ)
		}
	}
	switch x := v.(type) {
	case map[string]any:
		for _, r := range asList(s["required"]) {
			if _, ok := x[r.(string)]; !ok {
				return fmt.Errorf("%s: missing required %q", path, r)
			}
		}
		props, _ := s["properties"].(map[string]any)
		for k, sub := range props {
			if val, ok := x[k]; ok {
				if err := check(spec, sub, val, path+"."+k); err != nil {
					return err
				}
			}
		}
	case []any:
		if items := s["items"]; items != nil {
			for i, e := range x {
				if err := check(spec, items, e, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func asList(v any) []any { l, _ := v.([]any); return l }

func checkType(typ string, v any, path string) error {
	ok := false
	switch typ {
	case "object":
		_, ok = v.(map[string]any)
	case "array":
		_, ok = v.([]any)
	case "string":
		_, ok = v.(string)
	case "number":
		_, ok = v.(float64)
	case "integer":
		f, isF := v.(float64)
		ok = isF && f == float64(int64(f))
	case "null":
		ok = v == nil
	}
	if !ok {
		return fmt.Errorf("%s: %v is not %s", path, v, typ)
	}
	return nil
}

func TestContract(t *testing.T) {
	spec := loadSpec(t)
	h := router(t, nil)
	paths, _ := spec["paths"].(map[string]any)

	routed := map[string]bool{}
	chi.Walk(h.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == "GET" && (strings.HasPrefix(route, "/v1/") || route == "/healthz") {
			routed[route] = true
		}
		return nil
	})
	for p := range paths {
		if !routed[p] {
			t.Errorf("spec path %s has no route", p)
		}
	}
	for p := range routed {
		if paths[p] == nil {
			t.Errorf("route %s missing from spec", p)
		}
	}

	samples := map[string]string{
		"/healthz": "/healthz", "/v1/meta": "/v1/meta", "/v1/provinsi": "/v1/provinsi",
		"/v1/provinsi/{kode}/kabupaten": "/v1/provinsi/11/kabupaten", "/v1/kabupaten/{kode}/kecamatan": "/v1/kabupaten/11.01/kecamatan",
		"/v1/kecamatan/{kode}/desa": "/v1/kecamatan/11.01.01/desa", "/v1/wilayah/{kode}": "/v1/wilayah/11.01.01.2001",
		"/v1/wilayah/{kode}/pulau": "/v1/wilayah/11.01/pulau", "/v1/kodepos/{kodepos}": "/v1/kodepos/23773", "/v1/cari": "/v1/cari?q=bakongan",
	}
	for p := range paths {
		url, ok := samples[p]
		if !ok {
			t.Errorf("no sample request for %s", p)
			continue
		}
		get := at(paths[p], "get")
		// limit/cursor declared in the spec must be exactly what the handler validates
		declared := map[string]bool{}
		for _, prm := range asList(at(get, "parameters")) {
			if n, _ := resolve(spec, prm)["name"].(string); n != "" {
				declared[n] = true
			}
		}
		sep := "?"
		if strings.Contains(url, "?") {
			sep = "&"
		}
		for param, bad := range map[string]string{"limit": "0", "cursor": "bad"} {
			if handled := do(h, "GET", url+sep+param+"="+bad).Code == 400; handled != declared[param] {
				t.Errorf("%s: handler validates %s=%v but spec declares it=%v", p, param, handled, declared[param])
			}
		}
		responses := at(get, "responses").(map[string]any)
		w := do(h, "GET", url)
		if responses[fmt.Sprint(w.Code)] == nil {
			t.Errorf("%s: status %d not documented", url, w.Code)
			continue
		}
		var v any
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Errorf("%s: %v", url, err)
			continue
		}
		schema := at(responses[fmt.Sprint(w.Code)], "content", "application/json", "schema")
		if err := check(spec, schema, v, url); err != nil {
			t.Error(err)
		}
	}

	// documented error statuses must be produced and match the Problem schema
	for url, code := range map[string]int{"/v1/wilayah/zz": 400, "/v1/wilayah/99": 404} {
		w := do(h, "GET", url)
		var v any
		json.Unmarshal(w.Body.Bytes(), &v)
		resp := at(paths["/v1/wilayah/{kode}"], "get", "responses", fmt.Sprint(code))
		if w.Code != code || resp == nil {
			t.Fatalf("%s: %d", url, w.Code)
		}
		if err := check(spec, at(resolve(spec, resp), "content", "application/problem+json", "schema"), v, url); err != nil {
			t.Error(err)
		}
	}
}

// 405, 429 and 503 are documented and match the Problem schema.
func TestContractTransientErrors(t *testing.T) {
	spec := loadSpec(t)
	limited := router(t, func(c *Config) { c.RateRPS, c.RateBurst = 0.001, 1 })
	do(limited, "GET", "/v1/provinsi")
	slow := router(t, func(c *Config) { c.RequestTimeout = time.Nanosecond })
	cases := []struct {
		h           http.Handler
		method, url string
		path        string
		code        int
	}{
		{router(t, nil), "POST", "/v1/provinsi", "/v1/provinsi", 405},
		{limited, "GET", "/v1/provinsi", "/v1/provinsi", 429},
		{slow, "GET", "/v1/cari?q=zzzz", "/v1/cari", 503},
	}
	for _, c := range cases {
		w := do(c.h, c.method, c.url)
		resp := at(spec, "paths", c.path, "get", "responses", fmt.Sprint(c.code))
		if w.Code != c.code || resp == nil {
			t.Errorf("%s %s: got %d, documented=%v", c.method, c.url, w.Code, resp != nil)
			continue
		}
		var v any
		json.Unmarshal(w.Body.Bytes(), &v)
		if err := check(spec, at(resolve(spec, resp), "content", "application/problem+json", "schema"), v, c.url); err != nil {
			t.Error(err)
		}
	}
}
