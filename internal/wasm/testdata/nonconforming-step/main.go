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
// The step socket carries canonical CBOR in both directions, so the guest reads
// its input and writes its output in that encoding. It hand-writes both instead
// of using the generated ABI codec, and that is the point rather than a shortcut.
// The codec is built from the schema so a guest cannot express a payload the
// contract forbids — which is exactly what this guest has to express. A payload
// assembled by hand is the only way to send the host something the contract
// rules out.
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
	"errors"
	"sort"

	pdk "github.com/extism/go-pdk"
)

// stepDescriptor is the fixed describe() self-report: a pipeline-step plugin
// whose single step export patches nothing and is declared compatible with the
// helm provider, which is the provider the tests that drive it render through.
const stepDescriptor = `{` +
	`"abi_version":"v1beta1",` +
	`"plugin_type":"pipeline-step",` +
	`"capabilities":["helm","helm-chart"],` +
	`"description":"Returns a side effect the pipeline contract does not admit, to drive a host refusal.",` +
	`"exports":[{"name":"step","input_pattern":"b","description":"returns one deliberately non-conforming side effect"}]` +
	`}`

// errMalformed reports input bytes this guest's reader cannot follow.
var errMalformed = errors.New("malformed step input")

// reader walks the definite-length CBOR the host writes. It reads only the
// shapes this guest needs — maps, arrays, text and byte strings, and integers —
// and skips any other item whole.
type reader struct {
	data []byte
	pos  int
}

// head reads one item's major type and argument.
func (r *reader) head() (major byte, arg uint64, err error) {
	if r.pos >= len(r.data) {
		return 0, 0, errMalformed
	}
	b := r.data[r.pos]
	r.pos++
	major, info := b>>5, b&0x1f
	if info < 24 {
		return major, uint64(info), nil
	}
	size := 0
	switch info {
	case 24:
		size = 1
	case 25:
		size = 2
	case 26:
		size = 4
	case 27:
		size = 8
	default:
		return 0, 0, errMalformed
	}
	if r.pos+size > len(r.data) {
		return 0, 0, errMalformed
	}
	for i := 0; i < size; i++ {
		arg = arg<<8 | uint64(r.data[r.pos+i])
	}
	r.pos += size
	return major, arg, nil
}

// str reads a text or byte string of the given major type.
func (r *reader) str(want byte) ([]byte, error) {
	major, n, err := r.head()
	if err != nil || major != want || uint64(len(r.data)-r.pos) < n {
		return nil, errMalformed
	}
	s := r.data[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return s, nil
}

// skip passes over one whole item.
func (r *reader) skip() error {
	major, arg, err := r.head()
	if err != nil {
		return err
	}
	switch major {
	case 2, 3:
		if uint64(len(r.data)-r.pos) < arg {
			return errMalformed
		}
		r.pos += int(arg)
	case 4:
		for i := uint64(0); i < arg; i++ {
			if err := r.skip(); err != nil {
				return err
			}
		}
	case 5:
		for i := uint64(0); i < 2*arg; i++ {
			if err := r.skip(); err != nil {
				return err
			}
		}
	}
	return nil
}

// entries reads an order-preserving map — an array of [key, value] pairs — and
// calls each with its key, leaving the reader at the entry's value.
func (r *reader) entries(each func(key string) error) error {
	major, n, err := r.head()
	if err != nil || major != 4 {
		return errMalformed
	}
	for i := uint64(0); i < n; i++ {
		if major, pair, err := r.head(); err != nil || major != 4 || pair != 2 {
			return errMalformed
		}
		key, err := r.str(3)
		if err != nil {
			return err
		}
		if err := each(string(key)); err != nil {
			return err
		}
	}
	return nil
}

// fields reads a map keyed by text and calls each with its key, leaving the
// reader at the field's value.
func (r *reader) fields(each func(key string) error) error {
	major, n, err := r.head()
	if err != nil || major != 5 {
		return errMalformed
	}
	for i := uint64(0); i < n; i++ {
		key, err := r.str(3)
		if err != nil {
			return err
		}
		if err := each(string(key)); err != nil {
			return err
		}
	}
	return nil
}

// decodeStep decodes the sliver of the payload this guest reads — its own `with`
// block, which names the violation to commit, and the own-scope document set,
// which the patch mode below needs in order to address a document by the key it
// was actually given — and returns the violation alongside the lowest-sorted
// own-scope document key.
//
// Only a document's KEY matters here: the guest learns which documents exist and
// addresses one of them, exactly as a conforming step does, and never assumes a
// key it was not handed.
//
// A non-string violation reads as absent: this guest is choosing between fixed
// behaviours, and coercing an arbitrary value into that choice would make a
// mis-authored fixture look like a deliberate one. The key is sorted rather than
// taken from the order it arrived in so the guest addresses the same document on
// every run, which is what keeps its output byte-identical across runs of
// identical input.
func decodeStep(raw []byte) (violation, ownKey string) {
	r := &reader{data: raw}
	var keys []string
	err := r.fields(func(field string) error {
		switch field {
		case "step_input":
			return r.entries(func(key string) error {
				value, err := r.str(2)
				if err != nil {
					return err
				}
				if key == "violation" {
					var v any
					if json.Unmarshal(value, &v) == nil {
						violation, _ = v.(string)
					}
				}
				return nil
			})
		case "documents":
			return r.fields(func(root string) error {
				if root != "own" {
					return r.skip()
				}
				return r.entries(func(key string) error {
					keys = append(keys, key)
					return r.skip()
				})
			})
		default:
			return r.skip()
		}
	})
	if err != nil {
		return "", ""
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		ownKey = keys[0]
	}
	return violation, ownKey
}

// writer appends canonical CBOR: definite lengths, shortest heads, and each
// map's keys written by the caller in canonical order — the shorter key first,
// and keys of one length in byte order.
type writer []byte

func (w *writer) head(major byte, n int) {
	switch {
	case n < 24:
		*w = append(*w, major<<5|byte(n))
	case n < 1<<8:
		*w = append(*w, major<<5|24, byte(n))
	case n < 1<<16:
		*w = append(*w, major<<5|25, byte(n>>8), byte(n))
	default:
		*w = append(*w, major<<5|26, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
}

func (w *writer) text(s string) {
	w.head(3, len(s))
	*w = append(*w, s...)
}

func (w *writer) bytes(b []byte) {
	w.head(2, len(b))
	*w = append(*w, b...)
}

// sideEffects writes an output carrying one side effect: kind and scope always,
// and the file only where path is set.
func sideEffects(kind, path, content string) []byte {
	var w writer
	w.head(5, 1)
	w.text("side_effects")
	w.head(4, 1)
	if path == "" {
		w.head(5, 2)
	} else {
		w.head(5, 3)
		w.text("file")
		w.head(5, 2)
		w.text("path")
		w.text(path)
		w.text("content")
		w.text(content)
	}
	w.text("kind")
	w.text(kind)
	w.text("scope")
	w.text("own")
	return w
}

// patchOwnOutput returns a conforming own-scope PATCH addressing the document at
// key. The operation itself is unremarkable and is meant to be: this mode exists
// so a PATCH reaches the reassembly half of a codec's contract, and the codec is
// what is under test there, not the operation. The operand rides as the bytes of
// its JSON document, as every patch value does.
func patchOwnOutput(key string) []byte {
	var w writer
	w.head(5, 1)
	w.text("patches")
	w.head(4, 1)
	w.head(5, 5)
	w.text("op")
	w.text("add")
	w.text("key")
	w.text(key)
	w.text("path")
	w.text("/metadata/annotations")
	w.text("scope")
	w.text("own")
	w.text("value")
	w.bytes([]byte(`{"nonconforming.step":"patched"}`))
	return w
}

// The three side-effect outputs, each named for the exact output the host
// receives.
var (
	// emitFileAbsentOutput declares a kind the contract DOES define and omits
	// the file member that kind's arm reads.
	emitFileAbsentOutput = sideEffects("emit_file", "", "")

	// kindUndefinedOutput declares a kind the contract does not define at all.
	// The kind is an open string on the wire, which is what lets a later kind
	// extend the contract; this value is one no arm matches.
	kindUndefinedOutput = sideEffects("emit_nothing_at_all", "", "")

	// conformingOutput is the positive control: a well-formed emit_file side
	// effect the host accepts.
	conformingOutput = sideEffects("emit_file", "nonconforming-step-control.yaml",
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: control\n")
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
		pdk.Output(emitFileAbsentOutput)
	case "kind-undefined":
		pdk.Output(kindUndefinedOutput)
	case "patch-own":
		// A conforming PATCH. It is here so the reassembly half of a codec's
		// contract is reached at all: nothing reassembles a file no step
		// patched, so a codec whose carve and reassembly disagree about tokens
		// cannot be caught without a patch first.
		pdk.Output(patchOwnOutput(ownKey))
	default:
		pdk.Output(conformingOutput)
	}
	return 0
}

func main() {}
