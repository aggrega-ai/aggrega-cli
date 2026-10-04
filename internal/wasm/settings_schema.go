// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// settingsSchemaResourceID is the resource identifier a plugin's declared
// settings schema compiles under. It is an in-memory identity only — the schema
// is supplied inline by the guest's describe(), never resolved from a URL — so
// it needs no network or filesystem meaning.
const settingsSchemaResourceID = "mem://settings-schema.json"

// compileSettingsSchema compiles a plugin-declared settings schema (raw JSON
// Schema bytes) into a validator. A malformed schema document surfaces as an
// error — including one the compiler would otherwise raise as a panic — rather
// than crashing the load. It is the single compile path both the load-time
// shape check ([PluginDescriptor.validateSettingsSchema]) and the dispatch-time
// settings validation ([PluginDescriptor.ValidateSettings]) use, so the two
// never diverge on how a guest-declared schema is interpreted.
func compileSettingsSchema(raw json.RawMessage) (schema *jsonschema.Schema, err error) {
	defer func() {
		if r := recover(); r != nil {
			schema = nil
			err = fmt.Errorf("compile settings schema: %v", r)
		}
	}()

	doc, uerr := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if uerr != nil {
		return nil, uerr
	}
	compiler := jsonschema.NewCompiler()
	if aerr := compiler.AddResource(settingsSchemaResourceID, doc); aerr != nil {
		return nil, aerr
	}
	return compiler.Compile(settingsSchemaResourceID)
}

// validateSettingsSchema shape-checks the descriptor's declared settings schema:
// when present, it must compile as a JSON Schema. A malformed schema fails the
// load wrapping [ErrWasmManifestRejected] and naming pluginIdentity. This is
// shape validation only — the gate confirms the schema is well-formed; it does
// not check the schema against any settings value here. A descriptor that
// declares no schema passes with no work.
func (d PluginDescriptor) validateSettingsSchema(pluginIdentity string) error {
	if len(d.SettingsSchema) == 0 {
		return nil
	}
	if _, err := compileSettingsSchema(d.SettingsSchema); err != nil {
		return fmt.Errorf("plugin %q declares a settings_schema that is not a valid JSON Schema: %v: %w",
			pluginIdentity, err, ErrWasmManifestRejected())
	}
	return nil
}

// ValidateValueAgainstSchema validates value against a JSON Schema document. An
// empty (nil or zero-length) schema declares no contract and accepts any value,
// returning nil — so a surface that opts out of typing keeps free-form
// pass-through behaviour. When a schema is present, value is validated against
// it and a value that does not conform returns an error whose message names the
// offending key and the schema's expectation.
//
// value is round-tripped through JSON before validation so it reaches the schema
// library in the canonical JSON form the library expects, independent of the
// concrete Go type the caller holds — a map, a struct, or an ordered map all
// marshal to the same JSON value, and the validator never sees the Go wrapper.
// A caller holding a Go nil marshals to JSON null, so a schema that requires
// content halts a caller that supplies none.
//
// This is the single compile-and-validate home for generic tree-vs-JSON-Schema
// checks; every typed surface delegates here rather than compiling and
// validating independently. The host runs generic JSON Schema validation only;
// it does not interpret what the value means.
func ValidateValueAgainstSchema(schema json.RawMessage, value any) error {
	if len(schema) == 0 {
		return nil
	}
	compiled, err := compileSettingsSchema(schema)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	canonical, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	return compiled.Validate(canonical)
}

// WithheldValidationFailure describes a failure ValidateValueAgainstSchema
// returned without reproducing anything of the value it refused: each position
// in the validated value that failed, and the schema keyword that refused it,
// read from the failure's structure rather than from its sentence, which quotes
// the value. It reports false for an error that is not a validation failure — a
// schema that does not compile, a value that does not encode — which carries no
// such structure to read.
//
// The positions are the value's own keys, so a key a template produced from data
// appears as produced. The descriptions are sorted, so the same failure reads the
// same way on every run whatever order the library collected them in.
func WithheldValidationFailure(err error) (string, bool) {
	var failure *jsonschema.ValidationError
	if !errors.As(err, &failure) {
		return "", false
	}
	seen := map[string]bool{}
	var described []string
	var collect func(*jsonschema.ValidationError)
	collect = func(v *jsonschema.ValidationError) {
		if len(v.Causes) > 0 {
			for _, cause := range v.Causes {
				collect(cause)
			}
			return
		}
		keyword := "the schema"
		if v.ErrorKind != nil {
			if path := v.ErrorKind.KeywordPath(); len(path) > 0 {
				keyword = strings.Join(path, "/")
			}
		}
		line := fmt.Sprintf("at '/%s': %s", strings.Join(v.InstanceLocation, "/"), keyword)
		if !seen[line] {
			seen[line] = true
			described = append(described, line)
		}
	}
	collect(failure)
	sort.Strings(described)
	return strings.Join(described, "; "), true
}

// ValidateSettings validates an operator-supplied settings value against the
// descriptor's declared settings schema. A descriptor that declares no schema
// accepts any settings (free-form pass-through) and returns nil, so a plugin
// that opts out of typing its settings keeps today's behaviour unchanged. When
// a schema is declared, the settings value is validated against it and a value
// that does not conform returns an error whose message names the offending key
// and the schema's expectation; the caller wraps that with its own build-fault
// sentinel and the plugin's identity.
//
// The value validated is the operator's settings object, or JSON null when the
// attachment carries no settings — so a schema that requires settings halts a
// build that supplies none. It delegates to [ValidateValueAgainstSchema], the
// single compile-and-validate home, so the settings path and every other typed
// surface interpret a guest-declared schema identically.
func (d PluginDescriptor) ValidateSettings(settings map[string]any) error {
	return ValidateValueAgainstSchema(d.SettingsSchema, settings)
}
