// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package envelope

import (
	"encoding/json"
	"fmt"
	"slices"
	"sync"

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

// retiredAPIVersions are the API versions a document could once declare and this
// compiler no longer reads. A version leaves the list never: a document still
// declaring one is told which line to change for as long as the compiler can
// recognise it.
var retiredAPIVersions = []string{"aggrega.ai/v1alpha1"}

// apiVersionPath is the field path of a document's `apiVersion` key, in the
// spelling the schema engine's findings use and yamlpos resolves.
const apiVersionPath = "apiVersion"

// readAPIVersions maps each kind whose schema states the `apiVersion` its
// documents declare to that value, read off the schema's own `const`. The schema
// is where the version a document must declare is stated, so the version the
// refusal tells an author to write is the one the schema accepts rather than a
// second copy of it. A kind whose schema declares no `apiVersion` — a lockfile, a
// workspace configuration — is absent: its documents carry no such line, and a
// document of that kind that does carry one fails its schema.
var readAPIVersions = sync.OnceValue(func() map[Kind]string {
	out := make(map[Kind]string, len(schemaSource))
	for kind, raw := range schemaSource {
		var s struct {
			Properties struct {
				APIVersion struct {
					Const *string `json:"const"`
				} `json:"apiVersion"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(raw), &s); err != nil || s.Properties.APIVersion.Const == nil {
			continue
		}
		out[kind] = *s.Properties.APIVersion.Const
	}
	return out
})

// retiredAPIVersion refuses a document whose `apiVersion` is one this compiler no
// longer reads, and returns nil for every other document.
//
// The refusal carries one finding, positioned at the `apiVersion` line, naming
// the version the document declares and the version the compiler reads, and the
// advice names the line to change and what to write there. Both versions are
// quoted even where every value is withheld: the declared one is a member of
// retiredAPIVersions and the other is the schema's own, so neither is a value the
// author supplied.
func retiredAPIVersion(key compiledKey, path string, data []byte, instance any) error {
	doc, ok := instance.(map[string]any)
	if !ok {
		return nil
	}
	declared, ok := doc[apiVersionPath].(string)
	if !ok || !slices.Contains(retiredAPIVersions, declared) {
		return nil
	}
	reads, ok := readAPIVersions()[key.kind]
	if !ok || reads == declared {
		return nil
	}
	e := &Error{
		Kind:      key.kind,
		Path:      path,
		condition: errRetiredAPIVersion,
		advice:    fmt.Sprintf("change the apiVersion line to `apiVersion: %s`", reads),
		Findings: []jsonvalidator.Finding{{
			FieldPath: apiVersionPath,
			Message:   fmt.Sprintf("the document declares %s, and this compiler reads %s", declared, reads),
		}},
	}
	if locate, ok := fieldLocator(data, path); ok {
		if loc, ok := locate(apiVersionPath); ok {
			e.Positions = map[string]aggerrors.SourceLocation{apiVersionPath: loc}
		}
	}
	return e
}
