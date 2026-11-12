// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func TestInterpolateParameters_ValidTemplate(t *testing.T) {
	raw := []byte(`name: {{ .parameters.app_name }}
port: {{ .parameters.port | default "8080" }}`)
	params := map[string]any{
		"app_name": "my-api",
		"port":     "3000",
	}

	result, err := InterpolateParameters(raw, params)
	if err != nil {
		t.Fatal(err)
	}
	s := string(result)
	if !strings.Contains(s, "name: my-api") {
		t.Errorf("expected interpolated app_name, got:\n%s", s)
	}
	if !strings.Contains(s, "port: 3000") {
		t.Errorf("expected interpolated port, got:\n%s", s)
	}
}

func TestInterpolateParameters_SprigDefault(t *testing.T) {
	raw := []byte(`port: {{ .parameters.port | default "8080" }}`)
	params := map[string]any{}

	result, err := InterpolateParameters(raw, params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), "port: 8080") {
		t.Errorf("expected default value, got:\n%s", string(result))
	}
}

func TestInterpolateParameters_SprigUpper(t *testing.T) {
	raw := []byte(`env: {{ .parameters.env | upper }}`)
	params := map[string]any{"env": "production"}

	result, err := InterpolateParameters(raw, params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), "env: PRODUCTION") {
		t.Errorf("expected upper case, got:\n%s", string(result))
	}
}

// A parameter the Release never filled renders as nothing at all. The assertion is
// on the WHOLE output rather than on a substring, because the defect this pins —
// the template engine's own placeholder text reaching the rendered bytes — is a
// substring of exactly the prefix a containment check would look for, so
// `Contains(out, "name: ")` is satisfied by "name: <no value>" and by "name: "
// alike and can distinguish neither.
func TestInterpolateParameters_MissingParamRendersNothing(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		params map[string]any
		want   string
	}{
		{
			name:   "scalar read of an unfilled parameter",
			raw:    "name: {{ .parameters.missing_key }}\nother: {{ .parameters.filled }}\n",
			params: map[string]any{"filled": "real"},
			want:   "name: \nother: real\n",
		},
		{
			name:   "printf of an unfilled parameter",
			raw:    "name: {{ printf \"%s\" .parameters.missing_key }}\n",
			params: map[string]any{},
			want:   "name: \n",
		},
		{
			name:   "unfilled key under a filled parent",
			raw:    "host: {{ .parameters.db.missing }}\n",
			params: map[string]any{"db": map[string]any{"host": "db.local"}},
			want:   "host: \n",
		},
		{
			name:   "read through an alias for the parameter block",
			raw:    "{{ $p := .parameters }}name: {{ $p.missing_key }}\n",
			params: map[string]any{},
			want:   "name: \n",
		},
		{
			name:   "a default pipe still supplies its fallback",
			raw:    "port: {{ .parameters.port | default \"8080\" }}\n",
			params: map[string]any{},
			want:   "port: 8080\n",
		},
		{
			name:   "an if guard over an unfilled parameter is still falsy",
			raw:    "{{ if .parameters.missing_key }}yes{{ else }}no{{ end }}\n",
			params: map[string]any{},
			want:   "no\n",
		},
		{
			name:   "iterating an unfilled list still iterates as nothing",
			raw:    "list:\n{{ range .parameters.items }}- {{ . }}\n{{ end }}end\n",
			params: map[string]any{},
			want:   "list:\nend\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := InterpolateParameters([]byte(tc.raw), tc.params)
			if err != nil {
				t.Fatalf("expected no error for an unfilled parameter, got: %v", err)
			}
			if string(result) != tc.want {
				t.Errorf("rendered %q, want %q", string(result), tc.want)
			}
		})
	}
}

// A chain whose PARENT was never filled still halts. Supplying the parent would
// trade a loud halt for a silently wrong artifact, so only a leaf is supplied.
func TestInterpolateParameters_AbsentParentChainStillHalts(t *testing.T) {
	_, err := InterpolateParameters([]byte(`host: {{ .parameters.db.host }}`), map[string]any{})
	if err == nil {
		t.Fatal("a read through an unfilled parent must halt")
	}
	if !errors.Is(err, ErrInvalidParameterRef()) {
		t.Errorf("expected ErrInvalidParameterRef, got %v", err)
	}
	if !strings.Contains(err.Error(), "execution error") {
		t.Errorf("expected an execution-stage diagnostic, got %v", err)
	}
}

// The parameter map the caller passes is never modified. Both production callers
// hand the same map on to downstream consumers after interpolating with it, so a
// key supplied for the rendering would become a parameter those consumers see with
// an empty value the author never wrote.
func TestInterpolateParameters_DoesNotModifyTheCallersMap(t *testing.T) {
	params := map[string]any{"filled": "x", "db": map[string]any{"host": "h"}}
	raw := []byte("a: {{ .parameters.missing_key }}\nb: {{ .parameters.db.missing_nested }}\n")

	if _, err := InterpolateParameters(raw, params); err != nil {
		t.Fatal(err)
	}
	if len(params) != 2 {
		t.Errorf("caller's map gained entries: %v", params)
	}
	if _, present := params["missing_key"]; present {
		t.Error("an unfilled parameter must not be written back into the caller's map")
	}
	nested := params["db"].(map[string]any)
	if len(nested) != 1 {
		t.Errorf("caller's nested map gained entries: %v", nested)
	}
	if _, present := nested["missing_nested"]; present {
		t.Error("an unfilled nested parameter must not be written back into the caller's map")
	}
}

// Mandating a parameter is the author's own act, spelled with a guard over the
// parameter and Sprig's `fail`. The compiler supplies nothing and halts on nothing:
// whether a given parameter is mandatory is the author's judgement, not a rule the
// build imposes.
//
// The assertion is over the rendered outcome in both directions, because an error
// alone cannot tell an author-invoked halt from the template failing to parse — the
// parse and the execute stage wrap one sentinel here, so `errors.Is` alone is
// satisfied by a template that never ran at all.
func TestInterpolateParameters_AuthorMandatesWithFail(t *testing.T) {
	raw := []byte(`{{ if not .parameters.app_name }}{{ fail "app_name is required" }}{{ end }}name: {{ .parameters.app_name }}`)

	_, err := InterpolateParameters(raw, map[string]any{})
	if err == nil {
		t.Fatal("the author's own mandate must halt when the parameter is unfilled")
	}
	if !errors.Is(err, ErrInvalidParameterRef()) {
		t.Errorf("expected ErrInvalidParameterRef, got %v", err)
	}
	if !strings.Contains(err.Error(), "app_name is required") {
		t.Errorf("the halt must carry the author's own message, got %v", err)
	}
	if strings.Contains(err.Error(), "parse error") {
		t.Errorf("this must halt at execution, not by failing to parse: %v", err)
	}

	out, err := InterpolateParameters(raw, map[string]any{"app_name": "billing"})
	if err != nil {
		t.Fatalf("a filled parameter must satisfy the mandate, got: %v", err)
	}
	if string(out) != "name: billing" {
		t.Errorf("rendered %q, want %q", string(out), "name: billing")
	}
}

func TestLoadParametersImport_SingleFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "params.yaml")
	os.WriteFile(f, []byte("db_host: localhost\ndb_port: 5432"), 0644)

	params, err := LoadParametersImport("", dir, filepath.Join(dir, "release.yaml"), "./params.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if params["db_host"] != "localhost" {
		t.Errorf("expected db_host=localhost, got %v", params["db_host"])
	}
}

// TestLoadParametersImport_UnloadableFile pins both ways the import can fail to
// produce a document — the file is not there, and the file is there and its
// bytes do not parse — onto the one condition every unloadable file reports
// under. Multi-source layering is exercised by the Release extends: chain tests.
//
// Both arms match through the accessor rather than against the registered code,
// because a string comparison would keep compiling, keep reading like a match,
// and silently stop matching the day the condition is registered under a
// different code.
func TestLoadParametersImport_UnloadableFile(t *testing.T) {
	dir := t.TempDir()
	malformed := filepath.Join(dir, "malformed.yaml")
	if err := os.WriteFile(malformed, []byte("db_size: \"10Gi\n  bad: [unclosed\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "path names no file", path: filepath.Join(dir, "absent.yaml")},
		{name: "file holds bytes that do not parse", path: malformed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadParametersImport("", dir, filepath.Join(dir, "release.yaml"), "./"+filepath.Base(tc.path))
			if err == nil {
				t.Fatal("expected a load failure, got nil")
			}
			if !errors.Is(err, ErrYAMLLoad()) {
				t.Errorf("expected the document-load condition, got %v", err)
			}
		})
	}
}

func TestMergeParameters_ReleaseOverridesExternal(t *testing.T) {
	inline := types.OrderedMap[any]{}
	inline.Set("key", "release-val")
	release := types.ParametersBlock{Inline: inline}
	external := map[string]any{"key": "external-val", "extra": "ext"}

	merged := MergeParameters(release, external)
	if merged["key"] != "release-val" {
		t.Errorf("release should override external, got %v", merged["key"])
	}
	if merged["extra"] != "ext" {
		t.Errorf("external-only key should survive, got %v", merged["extra"])
	}
}

// TestOrderedMapToPlain_NestedOrderedMapShapes_RecursivelyUnwraps pins
// orderedMapToPlain's delegation to TemplateAlphabeticalMapDeep. The fixture
// shape mirrors what round-trip-fidelity JSON/YAML unmarshal produces for a
// nested Release.spec.parameters block: an outer OrderedMap[any] whose
// values include nested *OrderedMap[any] entries. After conversion the
// result must be a fully-unwrapped map[string]any walkable by Go templates
// via dotted field-access syntax (.parameters.db.host).
func TestOrderedMapToPlain_NestedOrderedMapShapes_RecursivelyUnwraps(t *testing.T) {
	t.Parallel()

	dbInfra := types.NewOrderedMap[any]()
	dbInfra.Set("host", "db.svc.cluster.local")
	dbInfra.Set("port", "5432")

	release := types.OrderedMap[any]{}
	release.Set("db", dbInfra)
	release.Set("app_name", "billing-api")

	result := orderedMapToPlain(release)
	if result == nil {
		t.Fatal("orderedMapToPlain returned nil for non-empty input")
	}

	dbAny, ok := result["db"]
	if !ok {
		t.Fatal("result missing key 'db'")
	}
	dbMap, ok := dbAny.(map[string]any)
	if !ok {
		t.Fatalf("result[db] type = %T, want map[string]any (recursive unwrap should have flattened the nested OrderedMap)", dbAny)
	}
	if got, _ := dbMap["host"].(string); got != "db.svc.cluster.local" {
		t.Errorf("result[db][host] = %v, want db.svc.cluster.local", dbMap["host"])
	}

	tmpl, err := template.New("test").Parse(`{{ .parameters.db.host }}:{{ .parameters.db.port }}`)
	if err != nil {
		t.Fatalf("template parse: %v", err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, map[string]any{"parameters": result}); err != nil {
		t.Fatalf("template execute: %v", err)
	}
	got := buf.String()
	want := "db.svc.cluster.local:5432"
	if got != want {
		t.Errorf("template output = %q, want %q", got, want)
	}
}
