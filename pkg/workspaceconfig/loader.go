// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"errors"
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// ErrConfigInvalid wraps every structural-validation failure raised
// by Load. Errors satisfying errors.Is(err, ErrConfigInvalid) carry
// a wrapped message naming the offending field path and the
// validation diagnostic.
var ErrConfigInvalid = errors.New("workspaceconfig: file failed schema validation")

// ErrDuplicateProviderTarget reports that the composed provider list binds
// one capability more than once. Errors satisfying
// errors.Is(err, ErrDuplicateProviderTarget) carry the offending capability
// name. A capability may have exactly one provider-rendering plugin
// binding in the resolved configuration, so two entries for the same capability
// — whether within one layer or contributed across the cascade and its
// `_append` companions — halt resolution loudly rather than silently letting
// one win.
var ErrDuplicateProviderTarget = errors.New("workspaceconfig: capability is bound to more than one provider plugin")

// ErrDuplicateRegistryAlias reports that a registry alias is already used by a
// different host entry. Errors satisfying errors.Is(err, ErrDuplicateRegistryAlias)
// carry the offending alias and the host that already holds it. A registry alias
// is a unique convenience selector across entries, so registering one already
// bound to another host halts loudly rather than shadowing the existing binding.
var ErrDuplicateRegistryAlias = errors.New("workspaceconfig: registry alias is already used by another host")

// ErrAmbiguousRegistryAlias reports that a registry token matched the alias of
// more than one entry during resolution. Errors satisfying
// errors.Is(err, ErrAmbiguousRegistryAlias) carry the offending alias. An alias
// selects exactly one host; a hand-edited config that binds the same alias to two
// hosts makes the selection ambiguous, so resolution halts loudly rather than
// silently choosing one host over the other.
var ErrAmbiguousRegistryAlias = errors.New("workspaceconfig: registry alias matches more than one host")

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
//
// Unexported: the only supported entry point is Resolve, which performs the
// whole discovery-and-cascade job. Keeping the single-file loader internal
// means no command outside this package can bypass Resolve to read a lone
// config file — the single resolution path is compiler-enforced.
func load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Default(), nil
		}
		return Config{}, fmt.Errorf("workspaceconfig: read %s: %w", path, err)
	}
	if err := validateBytes(data, path); err != nil {
		return Config{}, err
	}
	p := Default()
	if err := yaml.Unmarshal(data, &p); err != nil {
		// Defensive: schema accepted the instance but typed unmarshal
		// failed. Should not happen with a well-formed schema; surface
		// as a wrapped error rather than a panic so the call site can
		// still report something.
		return Config{}, fmt.Errorf("workspaceconfig: typed unmarshal %s: %w", path, err)
	}
	if err := validateSigningKinds(p.Signing.Kinds, path); err != nil {
		return Config{}, err
	}
	for _, r := range p.Registries {
		if err := validateSigningKinds(r.Signing.Kinds, path); err != nil {
			return Config{}, err
		}
	}
	return p, nil
}

// validateSigningKinds rejects a signing.kinds entry that is neither the "all"
// wildcard nor a known pushable artifact kind, wrapping ErrConfigInvalid so the
// failure is category-consistent with every other config-load rejection. The
// path argument names the offending file (or environment variable) so the
// operator sees which layer carried the bad value. The set of pushable kinds is
// the single one the OCI transport recognizes, so the membership check reuses
// that oracle rather than re-listing the kinds here.
func validateSigningKinds(kinds []string, path string) error {
	for _, k := range kinds {
		if k == signingKindsAll {
			continue
		}
		if _, err := oci.MediaTypeForKind(k); err != nil {
			return fmt.Errorf("workspaceconfig: %s: signing.kinds entry %q is not a signable artifact kind; use a catalog entity or plugin kind, or %q for every kind: %w", path, k, signingKindsAll, ErrConfigInvalid)
		}
	}
	return nil
}

// validateBytes runs the schema-validation phase against an already-
// read YAML document. Returns nil when the document is valid (or
// empty); returns a wrapped ErrConfigInvalid when the schema rejects
// it; returns a parse/build error for the underlying yaml / schema
// failures. The path argument is decorative — it appears in error
// messages to identify the offending file.
//
// Shared by Load and loadOverlayInto so each call site reads the
// file once and validates once, instead of paying the YAML parse
// cost twice when both validation and overlay-into-seed are needed.
func validateBytes(data []byte, path string) error {
	var probe any
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return fmt.Errorf("workspaceconfig: parse %s: %w", path, err)
	}
	if probe == nil {
		return nil
	}
	if err := checkConfigShape(probe, path); err != nil {
		return err
	}
	_, err := envelope.ValidateBytes(envelope.KindAggregaConfig, data, path)
	var envErr *envelope.Error
	if errors.As(err, &envErr) {
		f := envErr.Findings[0]
		fieldPath := f.FieldPath
		if fieldPath == "" {
			fieldPath = "(root)"
		}
		return fmt.Errorf("workspaceconfig: %s: %s: %s: %w",
			path, fieldPath, f.Message, ErrConfigInvalid)
	}
	if err != nil {
		return fmt.Errorf("workspaceconfig: validate %s: %w", path, err)
	}
	return nil
}

// checkConfigShape produces fix-it diagnostics for the two v2-layout
// mistakes the bare JSON Schema can only report generically: a null
// `workspace:` marker (the schema says "expected object, got null") and a
// key that moved out of `workspace` or was renamed at the top level (the
// schema says "additional property not allowed"). It lives in the single
// shared validate path so the CLI and daemon both surface the better
// message; an independent validator (e.g. an editor extension) still gets
// the generic schema message, which is acceptable. All returns wrap
// ErrConfigInvalid for category-consistent detection.
func checkConfigShape(probe any, path string) error {
	root, ok := probe.(map[string]any)
	if !ok {
		return nil
	}
	if ws, present := root["workspace"]; present {
		if ws == nil {
			return fmt.Errorf("workspaceconfig: %s: `workspace` must be a mapping; write `workspace: {}` to mark a workspace root with no catalog directories: %w", path, ErrConfigInvalid)
		}
		if wsMap, ok := ws.(map[string]any); ok {
			for _, moved := range []string{"default_registry", "offline", "auto_pull", "cache_dir"} {
				if _, found := wsMap[moved]; found {
					hint := moved
					if moved == "cache_dir" {
						hint = "artifact_cache_dir"
					}
					return fmt.Errorf("workspaceconfig: %s: `%s` moved to the top level; set top-level `%s`, not under `workspace`: %w", path, moved, hint, ErrConfigInvalid)
				}
			}
		}
	}
	if _, found := root["cache"]; found {
		return fmt.Errorf("workspaceconfig: %s: the `cache` block was renamed to `parse_cache`: %w", path, ErrConfigInvalid)
	}
	return nil
}
