// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile

import (
	"errors"
	"fmt"
	"os"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	"go.yaml.in/yaml/v3"
)

// ErrLockfileInvalid wraps every structural-validation failure
// raised by LoadLockfile. Errors satisfying
// errors.Is(err, ErrLockfileInvalid()) carry a wrapped message naming
// the offending field path and the validation diagnostic.
//
// A workspace configuration's own load registers a near-identical sentence under
// a separate condition. That split is deliberate and is not to be unified: what
// separates the two is the reason this registration already declares.
func ErrLockfileInvalid() error { return errLockfileInvalid }

var errLockfileInvalid = cerrors.Register("lockfile-invalid", cerrors.Meta{
	ShortCode: "E0131",
	Message:   "lockfile: file failed schema validation",
	RulePlace: cerrors.NotARule("A lockfile failed structural validation as it was loaded. The file is opened and validated " +
		"as one operation, and the machine writes it rather than an author, so what it reports is a " +
		"load that could not produce a lockfile."),
})

// DiagnosticSeverity classifies a non-fatal observation surfaced by
// LoadLockfile. "soft" covers tolerant-load conditions (missing
// optional sections, missing consumers lists).
type DiagnosticSeverity string

const (
	DiagnosticSoft DiagnosticSeverity = "soft"
)

// Diagnostic carries a non-fatal observation about the lockfile —
// missing optional sections or other tolerant-load conditions. Field
// names the offending or observed location (dotted path into the
// document); Message is the human-readable observation.
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

	instance, err := envelope.ValidateBytes(envelope.KindLockfile, data, path)
	var envErr *envelope.Error
	if errors.As(err, &envErr) {
		f := envErr.Findings[0]
		if f.Message == "document is empty" {
			return nil, nil, fmt.Errorf("lockfile: %s: document is empty", path)
		}
		fieldPath := f.FieldPath
		if fieldPath == "" {
			fieldPath = "(root)"
		}
		return nil, nil, fmt.Errorf("lockfile: %s: %s: %s: %w",
			path, fieldPath, f.Message, ErrLockfileInvalid())
	}
	if err != nil {
		return nil, nil, fmt.Errorf("lockfile: parse %s: %w", path, err)
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
