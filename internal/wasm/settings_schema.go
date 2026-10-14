// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
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
// never diverge on how a guest-declared schema is interpreted. A schema the
// metaschema refuses is reported with its causes in the fixed order
// [orderFailure] gives them.
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
	schema, err = compiler.Compile(settingsSchemaResourceID)
	orderFailure(err)
	return schema, err
}

// orderFailure puts every list of causes in a schema failure into one fixed
// order, at every depth, and lists the keys of an additionalProperties refusal in
// sorted order. The schema library collects both while ranging over Go maps —
// the keys of the object being validated, and keyword maps of the schema such as
// patternProperties and dependentRequired — so without this the same failure
// lists its causes in a different order from one call to the next, and the
// sentence it renders changes with them. A failure holding one cause at every
// depth, and naming at most one undeclared key, renders exactly as the library
// wrote it.
//
// err is a validation failure, a schema the metaschema refused, or anything
// else, which is left as it is. The failure is ordered in place and stays the
// library's own type, so [errors.As] finds it as before.
func orderFailure(err error) {
	var invalid *jsonschema.SchemaValidationError
	if errors.As(err, &invalid) {
		err = invalid.Err
	}
	var failure *jsonschema.ValidationError
	if errors.As(err, &failure) {
		orderCauses(failure)
	}
}

// orderCauses orders failure's causes depth first, so each cause is compared by
// the sentence it renders once its own causes are in order. The order is, in
// turn: the position in the value the cause refers to, so a value comes before
// what it contains; the schema keyword that refused it, by the schema document,
// then the keyword's position in that document; and last the cause's rendered
// text, which separates causes the library records at one position for one
// keyword — the property names a propertyNames schema refuses carry no position
// of their own. A sort, not a deduplication: every cause the library reported is
// kept.
func orderCauses(failure *jsonschema.ValidationError) {
	if undeclared, ok := failure.ErrorKind.(*kind.AdditionalProperties); ok {
		sort.Strings(undeclared.Properties)
	}
	for _, cause := range failure.Causes {
		orderCauses(cause)
	}
	if len(failure.Causes) < 2 {
		return
	}
	keyed := make([]keyedCause, len(failure.Causes))
	for i, cause := range failure.Causes {
		keyed[i] = keyOfCause(cause)
	}
	slices.SortStableFunc(keyed, compareCauses)
	for i := range keyed {
		failure.Causes[i] = keyed[i].cause
	}
}

// keyedCause is one cause and the parts of it the order compares, read once.
type keyedCause struct {
	cause    *jsonschema.ValidationError
	position []string // the position in the value, one token per step
	document string   // the schema document holding the refusing keyword
	keyword  []string // the keyword's position in that document, then its own path
	text     string   // the cause as it renders, its own causes included
}

func keyOfCause(cause *jsonschema.ValidationError) keyedCause {
	document, fragment, _ := strings.Cut(cause.SchemaURL, "#")
	var keyword []string
	if fragment = strings.TrimPrefix(fragment, "/"); fragment != "" {
		keyword = strings.Split(fragment, "/")
	}
	k := keyedCause{cause: cause, position: cause.InstanceLocation, document: document}
	if cause.ErrorKind != nil {
		k.keyword = append(keyword, cause.ErrorKind.KeywordPath()...)
		k.text = cause.Error()
	} else {
		k.keyword = keyword
	}
	return k
}

func compareCauses(a, b keyedCause) int {
	if c := comparePositions(a.position, b.position); c != 0 {
		return c
	}
	if c := strings.Compare(a.document, b.document); c != 0 {
		return c
	}
	if c := comparePositions(a.keyword, b.keyword); c != 0 {
		return c
	}
	return strings.Compare(a.text, b.text)
}

// comparePositions compares two positions token by token; one that runs out
// first sorts first, so a position comes before every position inside it.
func comparePositions(a, b []string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := compareTokens(a[i], b[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a), len(b))
}

// compareTokens compares two tokens of a position. A token made only of digits —
// an array index, or a subschema's index under allOf, anyOf, oneOf or
// prefixItems — sorts before any other token, and two of them compare as
// numbers, so /2 comes before /10 just as the library already reports the items
// of an array and the branches of a combinator. Any other two tokens compare as
// strings.
func compareTokens(a, b string) int {
	aIndex, bIndex := isIndexToken(a), isIndexToken(b)
	switch {
	case aIndex && bIndex:
		if c := cmp.Compare(len(a), len(b)); c != 0 {
			return c
		}
	case aIndex:
		return -1
	case bIndex:
		return 1
	}
	return strings.Compare(a, b)
}

func isIndexToken(token string) bool {
	if token == "" {
		return false
	}
	for i := 0; i < len(token); i++ {
		if token[i] < '0' || token[i] > '9' {
			return false
		}
	}
	return true
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
// A failure lists its causes in one fixed order at every depth — by the position
// in the value each refers to, then by the schema keyword that refused it — so
// the same value refused by the same schema reads the same on every run, whatever
// order the schema library collected the causes in ([orderFailure]). A refused
// value returns the library's own *jsonschema.ValidationError, ordered in place;
// a schema that does not compile returns its compile failure, ordered the same
// way.
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
	err = compiled.Validate(canonical)
	orderFailure(err)
	return err
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
