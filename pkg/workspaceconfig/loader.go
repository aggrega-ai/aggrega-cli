// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	libschema "github.com/google/jsonschema-go/jsonschema"
	"gopkg.in/yaml.v3"

	schemas "github.com/aggrega-ai/aggrega-schemas"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
)

// ErrConfigInvalid wraps every structural-validation failure raised
// by Load. Errors satisfying errors.Is(err, ErrConfigInvalid) carry
// a wrapped message naming the offending field path and the
// validation diagnostic.
var ErrConfigInvalid = errors.New("workspaceconfig: file failed schema validation")

// Load reads the workspace's .aggrega-config.yaml file at path,
// validates it against the aggrega-config-v1alpha1 JSON Schema, and
// returns the typed Config value.
//
// File absence is NOT an error: a workspace without a
// .aggrega-config.yaml gets Default() (the documented defaults
// — hub.aggrega.ai for the registry, .aggrega for the cache,
// "error" for halt_on_deprecated_severity, true for
// enforce_monotonic_narrowing). Callers don't need to pre-check
// for the file.
//
// Validation runs BEFORE typed unmarshalling: an invalid file
// produces a wrapped ErrConfigInvalid error with the offending
// field path and message; the typed struct is returned only when
// the schema check passes. This ordering ensures error diagnostics
// come from the JSON Schema layer (with field-path precision)
// rather than from yaml.v3's looser type-mismatch messages.
//
// Overlay-on-Default semantics: after schema validation succeeds,
// the typed unmarshal target is seeded with Default() so fields the
// document omits inherit the documented non-zero defaults rather
// than the Go zero-value. yaml.v3 only writes fields present in
// the source mapping; absent keys preserve the target's
// pre-existing values.
//
// yaml.v3 explicit-empty-mapping semantics (pinned empirically by
// TestLoadExplicitEmptyMapHonoursYAMLSemantics): an explicit empty
// mapping in the document (e.g. `workspace: {}`) is treated by
// yaml.v3's recursive unmarshal the same as the key being omitted
// — defaults are preserved. The empty mapping has no keys to
// write into the target struct, so the Default()-seeded values
// survive. The test pins this behaviour; if a future yaml.v3
// upgrade changes the disposition the test catches it.
//
// Returns wrapped errors on:
//   - File read failure (other than file-not-found).
//   - YAML parse failure (malformed document structure).
//   - Schema validation failure (well-formed YAML violating the schema).
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Default(), nil
		}
		return Config{}, fmt.Errorf("workspaceconfig: read %s: %w", path, err)
	}

	var instance any
	if err := yaml.Unmarshal(data, &instance); err != nil {
		return Config{}, fmt.Errorf("workspaceconfig: parse %s: %w", path, err)
	}
	if instance == nil {
		// Empty file: every field at its documented default.
		return Default(), nil
	}
	instance = normaliseYAML(instance)

	v, err := newValidator()
	if err != nil {
		return Config{}, fmt.Errorf("workspaceconfig: schema build: %w", err)
	}
	report, err := v.Validate(instance, nil)
	if err != nil {
		return Config{}, fmt.Errorf("workspaceconfig: validate %s: %w", path, err)
	}
	if len(report.Findings) > 0 {
		f := report.Findings[0]
		fieldPath := f.FieldPath
		if fieldPath == "" {
			fieldPath = "(root)"
		}
		return Config{}, fmt.Errorf("workspaceconfig: %s: %s: %s: %w",
			path, fieldPath, f.Message, ErrConfigInvalid)
	}

	p := Default()
	if err := yaml.Unmarshal(data, &p); err != nil {
		// Defensive: schema accepted the instance but typed unmarshal
		// failed. Should not happen with a well-formed schema; surface
		// as a wrapped error rather than a panic so the call site can
		// still report something.
		return Config{}, fmt.Errorf("workspaceconfig: typed unmarshal %s: %w", path, err)
	}
	return p, nil
}

// newValidator compiles the aggrega-config schema bytes into a
// Validator. The schema source lives in the aggrega-schemas Go
// module (schemas.AggregaConfigV1alpha1); aggrega-cli imports it
// rather than carrying a local JSON copy. The schema is small
// (~250 LOC of JSON) and compiles in microseconds; per-call
// compilation keeps the package import graph small. Callers loading
// multiple workspaces in a hot loop would cache the validator; the
// current expected call shape is once per build invocation.
func newValidator() (jsonvalidator.Validator, error) {
	schema := &libschema.Schema{}
	if err := json.Unmarshal([]byte(schemas.AggregaConfigV1alpha1), schema); err != nil {
		return nil, fmt.Errorf("decode embedded schema: %w", err)
	}
	v, err := jsonvalidator.New(schema)
	if err != nil {
		return nil, fmt.Errorf("compile embedded schema: %w", err)
	}
	return v, nil
}

// normaliseYAML walks the value yaml.Unmarshal produced for an `any`
// destination and converts every map[interface{}]interface{} into
// map[string]any. yaml.v3 produces map[string]any directly for keys
// that are all strings, but legacy paths or mixed-key documents may
// return the broader map type which the JSON Schema validator's
// walker does not consume. The validator expects map[string]any;
// this normalisation aligns the two layers.
func normaliseYAML(v any) any {
	switch val := v.(type) {
	case map[any]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			if ks, ok := k.(string); ok {
				out[ks] = normaliseYAML(vv)
			}
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			out[k] = normaliseYAML(vv)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = normaliseYAML(item)
		}
		return out
	default:
		return v
	}
}
