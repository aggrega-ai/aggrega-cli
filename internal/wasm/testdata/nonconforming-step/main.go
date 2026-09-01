// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Fixture nonconforming-step plugin for the Aggrega Wasm runtime, built with the
// standard Go toolchain for GOOS=wasip1.
//
// It is a pipeline-step guest that returns a side effect the plugin contract
// does not admit, so a host refusal of a guest contract violation can be driven
// and the diagnostic it prints can be read. Which violation it commits is chosen
// by the step's own `with` block, under the key `violation`:
//
//   - "emit-file-absent" returns a side effect declaring the emit_file kind and
//     carrying no file, so the arm that reads a file has nothing to read.
//   - "kind-undefined" returns a side effect declaring a kind the contract does
//     not define, so no arm matches it at all.
//   - "patch-own" returns a CONFORMING patch against the own-scope document it
//     was handed. It is not a violation: it exists so a patch reaches the
//     reassembly half of a codec's contract, which is where a codec whose carve
//     and reassembly disagree about tokens is caught. Nothing reassembles a file
//     no step patched.
//
// The first two are separate violations rather than two spellings of one, and
// that is why the selection exists: the host raises a different condition for
// each, and a fixture able to commit only the first could not drive the second.
//
// It hand-writes its output bytes instead of using the generated ABI codec, and
// that is the point rather than a shortcut. The codec is built from the schema
// so a guest cannot express a payload the contract forbids — which is exactly
// what this guest has to express. A payload assembled by hand is the only way to
// send the host something the contract rules out.
//
// Any other value of `violation`, its absence included, returns a conforming
// side effect. That keeps the fixture a positive control as well as a negative
// one: a run that produced no halt can be told apart from a run whose guest
// never got as far as returning anything.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o nonconforming-step.wasm ./
package main

import (
	"encoding/json"
	"sort"

	pdk "github.com/extism/go-pdk"
)

// stepDescriptor is the fixed describe() self-report: a pipeline-step plugin
// whose single step export patches nothing and is declared compatible with the
// helm provider, which is the provider the tests that drive it render through.
const stepDescriptor = `{` +
	`"abi_version":"v1alpha1",` +
	`"plugin_type":"pipeline-step",` +
	`"capabilities":["helm","helm-chart"],` +
	`"description":"Returns a side effect the pipeline contract does not admit, to drive a host refusal.",` +
	`"exports":[{"name":"step","input_pattern":"b","description":"returns one deliberately non-conforming side effect"}]` +
	`}`

// stepInput is the sliver of the step payload this guest reads: its own `with`
// block, which names the violation to commit, and the own-scope document set,
// which the patch mode below needs in order to address a document by the key it
// was actually given.
//
// A unit is decoded as an empty struct because only the KEY matters here: the
// guest learns which documents exist and addresses one of them, exactly as a
// conforming step does, and never assumes a key it was not handed.
type stepInput struct {
	StepInput map[string]any `json:"step_input"`
	Documents struct {
		Own map[string]struct{} `json:"own"`
	} `json:"documents"`
}

// decodeStep decodes the sliver of the payload this guest reads, and returns the
// violation named in the step's `with` block alongside the lowest-sorted
// own-scope document key.
//
// A non-string violation reads as absent: this guest is choosing between fixed
// behaviours, and coercing an arbitrary value into that choice would make a
// mis-authored fixture look like a deliberate one. The key is sorted rather than
// taken from map order so the guest addresses the same document on every run,
// which is what keeps its output byte-identical across runs of identical input.
func decodeStep(raw []byte) (violation, ownKey string) {
	var in stepInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", ""
	}
	if v, ok := in.StepInput["violation"].(string); ok {
		violation = v
	}
	keys := make([]string, 0, len(in.Documents.Own))
	for k := range in.Documents.Own {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		ownKey = keys[0]
	}
	return violation, ownKey
}

// patchOwnOutput returns a conforming own-scope PATCH addressing the document at
// key. The operation itself is unremarkable and is meant to be: this mode exists
// so a PATCH reaches the reassembly half of a codec's contract, and the codec is
// what is under test there, not the operation.
func patchOwnOutput(key string) string {
	op := map[string]any{
		"scope": "own",
		"key":   key,
		"op":    "add",
		"path":  "/metadata/annotations",
		"value": map[string]string{"nonconforming.step": "patched"},
	}
	raw, err := json.Marshal(map[string]any{"patches": []any{op}})
	if err != nil {
		return `{}`
	}
	return string(raw)
}

// The three outputs, written out in full rather than composed, so each is
// readable as the exact bytes the host receives.
const (
	// emitFileAbsentOutput declares a kind the contract DOES define and omits
	// the file member that kind's arm reads.
	emitFileAbsentOutput = `{"side_effects":[{"kind":"emit_file","scope":"own"}]}`

	// kindUndefinedOutput declares a kind the contract does not define at all.
	// The kind is an open string on the wire, which is what lets a later kind
	// extend the contract; this value is one no arm matches.
	kindUndefinedOutput = `{"side_effects":[{"kind":"emit_nothing_at_all","scope":"own"}]}`

	// conformingOutput is the positive control: a well-formed emit_file side
	// effect the host accepts.
	conformingOutput = `{"side_effects":[{"kind":"emit_file","scope":"own",` +
		`"file":{"path":"nonconforming-step-control.yaml",` +
		`"content":"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: control\n"}}]}`
)

//go:wasmexport describe
func describe() int32 {
	pdk.OutputString(stepDescriptor)
	return 0
}

//go:wasmexport step
func step() int32 {
	violation, ownKey := decodeStep(pdk.Input())
	switch violation {
	case "emit-file-absent":
		pdk.OutputString(emitFileAbsentOutput)
	case "kind-undefined":
		pdk.OutputString(kindUndefinedOutput)
	case "patch-own":
		// A conforming PATCH. It is here so the reassembly half of a codec's
		// contract is reached at all: nothing reassembles a file no step
		// patched, so a codec whose carve and reassembly disagree about tokens
		// cannot be caught without a patch first.
		pdk.OutputString(patchOwnOutput(ownKey))
	default:
		pdk.OutputString(conformingOutput)
	}
	return 0
}

func main() {}
