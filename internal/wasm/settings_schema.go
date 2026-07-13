// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"bytes"
	"encoding/json"
	"fmt"

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
			pluginIdentity, err, ErrWasmManifestRejected)
	}
	return nil
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
// build that supplies none. The settings are round-tripped through JSON before
// validation so the value is in the canonical form the schema library expects,
// independent of how the config layer decoded the authored YAML. The host runs
// generic JSON Schema validation only; it does not interpret what the settings
// mean.
func (d PluginDescriptor) ValidateSettings(settings map[string]any) error {
	if len(d.SettingsSchema) == 0 {
		return nil
	}
	schema, err := compileSettingsSchema(d.SettingsSchema)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	return schema.Validate(value)
}
