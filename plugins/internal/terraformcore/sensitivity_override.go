// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package terraformcore

import (
	"bytes"
	"encoding/json"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// OverrideFileName is the output filename of the sensitivity override the
// terraform guest appends beside a vendored module. Terraform merges a file
// named `override` or ending in `_override` into the configuration it sits
// with, so this name is what makes the file an override rather than a second
// declaration of the same variables — a file named anything else is loaded as
// its own configuration and the duplicate variable declarations halt the build.
//
// The `.tf.json` extension selects Terraform's JSON configuration syntax, which
// is why the file can be built with a deterministic JSON emission and needs no
// HCL writer.
//
// The name carries no directory component, which is the placement rule and not
// an incidental spelling: Terraform reads a module's configuration from one
// directory without descending, so an override placed anywhere but beside the
// declaration it overrides is ignored without complaint. Emitting it as a bare
// name lands it where the module's own files land, which is where the `variable`
// blocks it overrides are declared.
const OverrideFileName = "aggrega_override.tf.json"

// IndentJSONDocument is the deterministic JSON emission an appended terraform
// JSON file passes through: two-space indent and a trailing newline. It
// reformats whitespace only — the caller's key and value order survive
// untouched, which is what keeps an emitted file byte-identical across runs of
// identical input.
//
// It takes already-compact bytes rather than a value to encode so that the
// caller keeps ownership of its own ordering: [SensitivityOverride] marshals its
// variable names in the order the render wire fixed, and this step must not
// disturb that. Splitting the two also keeps the indent and the trailing newline
// in one place, so a second appended JSON document cannot drift away from the
// first.
func IndentJSONDocument(compact []byte) ([]byte, error) {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, compact, "", "  "); err != nil {
		return nil, err
	}
	pretty.WriteByte('\n')
	return pretty.Bytes(), nil
}

// SensitivityOverride builds the appended override document that carries
// Terraform's own `sensitive` marker for each module input the render payload
// marks as carrying declared-sensitive material. It returns nil, nil when
// nothing is marked, so a node with no marking gains no file.
//
// The marking is READ, never derived. sensitiveRenderedKeys is the set the host
// computed by joining a node's sensitivity declarations to the identities its
// render produced; this routine inspects no variable name, no key pattern, and
// nothing in the module's own text. Its whole contribution is to translate a
// declared fact into the target's own primitive.
//
// The emitted set is the intersection of that marking with values — the keys
// this render is writing as module inputs — and the intersection is load-bearing
// in both directions:
//
//   - An override entry for a variable the configuration does not declare is an
//     error ("no base variable declaration to override"), so emitting beyond the
//     inputs being written would break a build rather than mark it. A marking
//     travels for whatever shape its payload carries, and only a payload
//     carrying values yields keys that name module inputs.
//   - Walking values rather than the marking is also what makes the output
//     deterministic without a sort: values arrives in the order the render wire
//     fixed, while the marking is addressed as a lookup and carries no order of
//     its own.
//
// The document is Terraform's JSON configuration syntax — a `variable` block per
// marked input, each carrying `sensitive: true` and nothing else — so it
// overrides only the sensitivity of an existing declaration and leaves that
// declaration's type, default and description as the module authored them.
func SensitivityOverride(values types.OrderedMap[any], sensitiveRenderedKeys map[string]string) ([]byte, error) {
	if len(sensitiveRenderedKeys) == 0 {
		return nil, nil
	}

	marked := types.NewOrderedMap[any]()
	for key := range values.All() {
		if _, ok := sensitiveRenderedKeys[key]; !ok {
			continue
		}
		attrs := types.NewOrderedMap[any]()
		attrs.Set("sensitive", true)
		marked.Set(key, *attrs)
	}
	if marked.Len() == 0 {
		return nil, nil
	}

	doc := types.NewOrderedMap[any]()
	doc.Set("variable", *marked)

	compact, err := doc.MarshalJSON()
	if err != nil {
		return nil, err
	}
	return IndentJSONDocument(compact)
}
