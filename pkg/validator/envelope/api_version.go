// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package envelope

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginabi"
	aggerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
)

// ErrRetiredAPIVersion is the sentinel for a document declaring an API version
// this compiler read once and reads no longer. Each kind's schema states the one
// `apiVersion` a document of that kind may declare, so the schema refuses such a
// document as it refuses any other value; the gate reports it under this
// condition instead, because the fix is a single known line and the version to
// write there is the one the schema states.
//
// It is asked before the document is checked against its kind's schema, so a
// document declaring a retired version reports this refusal alone: every other
// finding is one about a version of the document the compiler does not read.
// A document declaring any other version the schema does not accept is a schema
// violation like any other, reported under ErrEntityEnvelope. The refusal is an
// *Error, so it carries the file and the position of the `apiVersion` line, and
// it matches ErrEntityEnvelope as well as this sentinel, so a caller gating on
// the gate's one sentinel still catches it.
func ErrRetiredAPIVersion() error { return errRetiredAPIVersion }

var errRetiredAPIVersion = aggerrors.Register("retired-api-version", aggerrors.Meta{
	ShortCode: "E0373",
	Message:   "a document declares an API version this compiler no longer reads",
	RulePlace: aggerrors.RuleOverAuthoredInput(),
})

// ErrRetiredABIVersion re-exports the condition a plugin declaring a plugin ABI
// version this compiler no longer speaks is refused under, so a caller of this
// gate matches the Plugin entity's refusal through the package that returns it.
// It yields that identity itself, registered once beside the retired versions,
// and the guest's refusal at load reports the same one: the entity and its guest
// state one ABI for one plugin, fixed the same way. Like ErrRetiredAPIVersion, the
// refusal is an *Error at the abiVersion line, asked before the schema, and it
// matches ErrEntityEnvelope as well.
func ErrRetiredABIVersion() error { return pluginabi.ErrRetiredABIVersion() }

// retiredAPIVersions are the API versions a document could once declare and this
// compiler no longer reads. A version leaves the list never: a document still
// declaring one is told which line to change for as long as the compiler can
// recognise it.
var retiredAPIVersions = []string{"aggrega.ai/v1alpha1"}

// retiredField is one field the gate holds against the values it accepted once
// and accepts no longer, and the words the refusal of such a value is reported in.
// Each retired version a document can declare is one of these, so the refusals
// share one reading of the document, one reading of the schema and one shape of
// report, and differ only in what they are about.
type retiredField struct {
	// kind limits the field to one document kind; empty, it applies to every kind
	// whose schema states the field's value.
	kind Kind
	// path is the field's key path from the document's root.
	path []string
	// retired reports whether a declared value is one the compiler no longer reads.
	retired func(string) bool
	// condition is the registered condition the refusal reports.
	condition error
	// finding and advice word the refusal's one finding and its advice, given the
	// declared value and the value the kind's schema states.
	finding func(declared, current string) string
	advice  func(current string) string
}

// retiredFields are the retired versions the gate refuses by name, in the order
// it asks: a document's API version before anything inside it.
var retiredFields = []retiredField{
	{
		path:      []string{"apiVersion"},
		retired:   func(v string) bool { return slices.Contains(retiredAPIVersions, v) },
		condition: errRetiredAPIVersion,
		finding: func(declared, current string) string {
			return fmt.Sprintf("the document declares %s, and this compiler reads %s", declared, current)
		},
		advice: func(current string) string {
			return fmt.Sprintf("change the apiVersion line to `apiVersion: %s`", current)
		},
	},
	{
		kind:      KindPlugin,
		path:      []string{"spec", "abiVersion"},
		retired:   pluginabi.IsRetired,
		condition: pluginabi.ErrRetiredABIVersion(),
		finding: func(declared, current string) string {
			return fmt.Sprintf("the Plugin declares abiVersion %s, and this compiler speaks %s", declared, current)
		},
		advice: func(current string) string {
			return fmt.Sprintf("%s; its Plugin entity then declares `abiVersion: %s`", pluginabi.Advice, current)
		},
	},
}

// refuseRetired refuses a document declaring a retired value in any of
// retiredFields, and returns nil for every other document.
//
// The refusal carries one finding, positioned at the field, naming the value the
// document declares and the value its kind's schema states, and the advice names
// what to write or do. Both values are quoted even where every value is withheld:
// the declared one is a member of a retired list and the other is the schema's
// own, so neither is a value the author supplied.
func refuseRetired(key compiledKey, path string, data []byte, instance any) error {
	for _, f := range retiredFields {
		if f.kind != "" && f.kind != key.kind {
			continue
		}
		declared, ok := stringAtPath(instance, f.path)
		if !ok || !f.retired(declared) {
			continue
		}
		current, ok := schemaValue(key.kind, f.path)
		if !ok || current == declared {
			continue
		}
		fieldPath := strings.Join(f.path, ".")
		e := &Error{
			Kind:      key.kind,
			Path:      path,
			condition: f.condition,
			advice:    f.advice(current),
			Findings:  []jsonvalidator.Finding{{FieldPath: fieldPath, Message: f.finding(declared, current)}},
		}
		if locate, ok := fieldLocator(data, path); ok {
			if loc, ok := locate(fieldPath); ok {
				e.Positions = map[string]aggerrors.SourceLocation{fieldPath: loc}
			}
		}
		return e
	}
	return nil
}

// stringAtPath reads the string a document holds at a key path, reporting false
// where any step is not a mapping or the value is not a string.
func stringAtPath(instance any, path []string) (string, bool) {
	cur := instance
	for _, k := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		cur = m[k]
	}
	s, ok := cur.(string)
	return s, ok
}

// schemaValues caches schemaValue's answers, keyed by kind and key path.
var schemaValues sync.Map

// schemaValue is the one value a kind's embedded schema accepts at a key path —
// its `const`, or an `enum` of exactly one value — read off the schema rather
// than restated, so the value a refusal tells an author to write is the one the
// schema accepts. It reports false where the schema states no such single value:
// a kind whose documents carry no such field (a lockfile has no apiVersion), or a
// field the schema leaves open, and the refusal then does not fire, leaving the
// schema's own findings to report the document.
func schemaValue(kind Kind, path []string) (string, bool) {
	cacheKey := string(kind) + "\x00" + strings.Join(path, "\x00")
	if v, ok := schemaValues.Load(cacheKey); ok {
		s := v.(string)
		return s, s != ""
	}
	value := ""
	var node map[string]any
	if raw, ok := schemaSource[kind]; ok && json.Unmarshal([]byte(raw), &node) == nil {
		for _, k := range path {
			props, _ := node["properties"].(map[string]any)
			node, _ = props[k].(map[string]any)
		}
		if c, ok := node["const"].(string); ok {
			value = c
		} else if e, ok := node["enum"].([]any); ok && len(e) == 1 {
			value, _ = e[0].(string)
		}
	}
	schemaValues.Store(cacheKey, value)
	return value, value != ""
}
