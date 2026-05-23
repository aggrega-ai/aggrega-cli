// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	libschema "github.com/google/jsonschema-go/jsonschema"
	"gopkg.in/yaml.v3"

	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
	schemas "github.com/aggrega-ai/aggrega-schemas"
)

// ErrLockfileInvalid wraps every structural-validation failure
// raised by LoadLockfile. Errors satisfying
// errors.Is(err, ErrLockfileInvalid) carry a wrapped message naming
// the offending field path and the validation diagnostic.
var ErrLockfileInvalid = errors.New("lockfile: file failed schema validation")

// DiagnosticSeverity classifies a non-fatal observation surfaced by
// LoadLockfile. Two values: "soft" for tolerant-load conditions
// (missing optional sections, missing consumers lists), "deprecation"
// for legacy content the loader accepts but flags for migration.
type DiagnosticSeverity string

const (
	DiagnosticSoft        DiagnosticSeverity = "soft"
	DiagnosticDeprecation DiagnosticSeverity = "deprecation"
)

// Diagnostic carries a non-fatal observation about the lockfile —
// legacy shapes, missing optional sections, or deprecated content
// the loader accepts but flags. Field names the offending or
// observed location (dotted path into the document); Message is the
// human-readable observation.
type Diagnostic struct {
	Severity DiagnosticSeverity
	Field    string
	Message  string
}

// LoadLockfile reads the lockfile at path, validates it against the
// embedded JSON Schema, and returns the typed Lockfile value plus
// any tolerant-load diagnostics.
//
// Return contract:
//   - On any error (read failure, parse failure, schema-validation
//     failure, typed-unmarshal failure): returns (nil, nil, err).
//     Diagnostic collection runs only after the document passes
//     schema validation; no observations are emitted before that
//     point, so the diagnostics slice is nil for every error path.
//     Errors that originated in schema validation wrap
//     ErrLockfileInvalid and name the offending field path.
//   - On success: returns (*Lockfile, diagnostics, nil) where
//     diagnostics is the possibly-empty list of tolerant-load
//     observations.
//
// File absence is an error here (unlike workspaceconfig.Load): the
// lockfile is required for deterministic builds. Absence means "no
// pull has run yet"; the caller decides whether to auto-pull.
//
// Tolerant-load conditions that surface as diagnostics rather than
// errors:
//   - schema_libraries section absent from the document → soft
//     diagnostic ("treating as empty closure")
//   - mapping_libraries section absent → soft diagnostic
//   - cue_modules section populated → one deprecation diagnostic per
//     entry (the section is retained for third-party / enterprise
//     catalogs that have not migrated)
//   - schema_libraries or mapping_libraries entry with empty or
//     absent consumers list → one soft diagnostic per affected
//     library entry naming its URI
//
// Distinguishing "absent from document" vs "present-but-empty"
// requires inspecting the parsed `any` value before typed
// unmarshalling, because the typed Lockfile collapses both shapes
// into nil/empty slices. Explicit empty lists (`schema_libraries: []`)
// are silent — the soft diagnostic fires only on absence-from-document.
func LoadLockfile(path string) (*Lockfile, []Diagnostic, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("lockfile: read %s: %w", path, err)
	}

	var instance any
	if err := yaml.Unmarshal(data, &instance); err != nil {
		return nil, nil, fmt.Errorf("lockfile: parse %s: %w", path, err)
	}
	if instance == nil {
		return nil, nil, fmt.Errorf("lockfile: %s: document is empty", path)
	}
	instance = normaliseYAML(instance)

	v, err := newValidator()
	if err != nil {
		return nil, nil, fmt.Errorf("lockfile: schema build: %w", err)
	}
	report, err := v.Validate(instance, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("lockfile: validate %s: %w", path, err)
	}
	if len(report.Findings) > 0 {
		f := report.Findings[0]
		fieldPath := f.FieldPath
		if fieldPath == "" {
			fieldPath = "(root)"
		}
		return nil, nil, fmt.Errorf("lockfile: %s: %s: %s: %w",
			path, fieldPath, f.Message, ErrLockfileInvalid)
	}

	diagnostics := walkDiagnostics(instance)

	lf := &Lockfile{}
	if err := yaml.Unmarshal(data, lf); err != nil {
		return nil, diagnostics, fmt.Errorf("lockfile: typed unmarshal %s: %w", path, err)
	}
	return lf, diagnostics, nil
}

// walkDiagnostics inspects the parsed `any` document and emits
// tolerant-load diagnostics. Operates on the pre-typed value because
// the typed Lockfile collapses absent-from-document and
// present-but-empty into the same nil/empty slice; the absent-vs-empty
// distinction is observable only at the `any` layer.
//
// Tolerance asymmetry between section-level and per-entry diagnostics:
//
//   - schema_libraries / mapping_libraries SECTION: present-but-empty
//     (`schema_libraries: []`) is silent; absent-from-document
//     emits a soft diagnostic. Rationale: an explicit empty list is
//     an intentional statement ("this Release has no library
//     closure"); absence is a structural gap that may indicate a
//     legacy or partially-walked lockfile worth flagging.
//   - schema_libraries / mapping_libraries ENTRY's consumers field:
//     present-but-empty (`consumers: []`) AND absent both emit a
//     soft diagnostic. Rationale: a library entry in a
//     properly-walked lockfile MUST carry consumers (every library
//     pin exists because at least one catalog entity references it);
//     an empty consumers list is a provenance gap regardless of
//     whether it was explicit or omitted. The two cases produce
//     distinct diagnostic messages ("has no consumers list" vs "has
//     an empty consumers list") so operators can tell them apart.
//
// The asymmetry reflects what each tolerance is protecting against:
// section-level tolerates legacy or no-library Releases without
// noise; entry-level treats consumers gaps as always worth flagging.
func walkDiagnostics(instance any) []Diagnostic {
	root, ok := instance.(map[string]any)
	if !ok {
		return nil
	}

	var out []Diagnostic

	if _, present := root["schema_libraries"]; !present {
		out = append(out, Diagnostic{
			Severity: DiagnosticSoft,
			Field:    "schema_libraries",
			Message:  "section absent from the document; treating as empty library closure",
		})
	}
	if _, present := root["mapping_libraries"]; !present {
		out = append(out, Diagnostic{
			Severity: DiagnosticSoft,
			Field:    "mapping_libraries",
			Message:  "section absent from the document; treating as empty library closure",
		})
	}
	if _, present := root["source_artifacts"]; !present {
		out = append(out, Diagnostic{
			Severity: DiagnosticSoft,
			Field:    "source_artifacts",
			Message:  "section absent from the document; treating as empty source-artifact closure",
		})
	}

	if cueModules, ok := root["cue_modules"].([]any); ok {
		for i, entry := range cueModules {
			obj, _ := entry.(map[string]any)
			module, _ := obj["module"].(string)
			out = append(out, Diagnostic{
				Severity: DiagnosticDeprecation,
				Field:    fmt.Sprintf("cue_modules[%d]", i),
				Message: fmt.Sprintf(
					"CUE module %q is recorded under cue_modules; first-party Aggrega catalogs no longer ship CUE modules (migrated to SchemaLibrary / MappingLibrary entities). The section is retained for third-party / enterprise catalogs that have not migrated.",
					module),
			})
		}
	}

	out = append(out, libraryConsumerDiagnostics(root, "schema_libraries")...)
	out = append(out, libraryConsumerDiagnostics(root, "mapping_libraries")...)

	return out
}

// libraryConsumerDiagnostics walks a library section (schema_libraries
// or mapping_libraries) and emits a soft diagnostic per entry whose
// `consumers` list is absent from the document or empty. The
// closure walker is the producer of `consumers`; an empty list is
// a provenance gap the operator may want to know about.
func libraryConsumerDiagnostics(root map[string]any, section string) []Diagnostic {
	entries, ok := root[section].([]any)
	if !ok {
		return nil
	}

	var out []Diagnostic
	for i, entry := range entries {
		obj, _ := entry.(map[string]any)
		uri, _ := obj["uri"].(string)
		consumers, present := obj["consumers"]
		if !present {
			out = append(out, Diagnostic{
				Severity: DiagnosticSoft,
				Field:    fmt.Sprintf("%s[%d].consumers", section, i),
				Message: fmt.Sprintf(
					"library %q has no consumers list; closure-walker provenance unavailable for this entry",
					uri),
			})
			continue
		}
		if list, ok := consumers.([]any); ok && len(list) == 0 {
			out = append(out, Diagnostic{
				Severity: DiagnosticSoft,
				Field:    fmt.Sprintf("%s[%d].consumers", section, i),
				Message: fmt.Sprintf(
					"library %q has an empty consumers list; closure-walker provenance unavailable for this entry",
					uri),
			})
		}
	}
	return out
}

// newValidator compiles the lockfile schema bytes into a Validator.
// The schema source lives in the aggrega-schemas Go module
// (schemas.LockfileV1alpha1); aggrega-cli imports it rather than
// carrying a local JSON copy. The schema is small (~300 LOC of
// JSON) and compiles in microseconds; per-call compilation keeps
// the package import graph small. Callers loading multiple
// lockfiles in a hot loop would cache the validator; the current
// expected call shape is once per build invocation.
func newValidator() (jsonvalidator.Validator, error) {
	schema := &libschema.Schema{}
	if err := json.Unmarshal([]byte(schemas.LockfileV1alpha1), schema); err != nil {
		return nil, fmt.Errorf("decode embedded schema: %w", err)
	}
	v, err := jsonvalidator.New(schema)
	if err != nil {
		return nil, fmt.Errorf("compile embedded schema: %w", err)
	}
	return v, nil
}

// normaliseYAML walks the value yaml.Unmarshal produced for an `any`
// destination and applies two transformations needed before the
// JSON Schema validator can consume the document:
//
//   - map[interface{}]interface{} → map[string]any. yaml.v3 produces
//     map[string]any directly for keys that are all strings, but
//     legacy paths or mixed-key documents may return the broader
//     map type which the validator's walker does not consume.
//
//   - time.Time → RFC 3339 string. yaml.v3 auto-detects ISO-8601
//     timestamp literals and produces time.Time values when
//     unmarshalling into `any`. The schema declares the
//     `generated_at` field as `type: "string"` with `format:
//     "date-time"`; without this normalisation the validator
//     reports "type: ... has type object, want string" because
//     time.Time marshals as an object. The conversion preserves the
//     wire format the schema documents.
//
//     Convention enforced here: every timestamp in the lockfile
//     crosses the JSON Schema validator as an RFC 3339 string. A
//     future schema evolution that wants a typed time.Time field
//     would fight this normalisation — adding such a field needs
//     a corresponding carve-out in normaliseYAML or a per-field
//     type tag on the validator.
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
	case time.Time:
		return val.UTC().Format(time.RFC3339)
	default:
		return v
	}
}
