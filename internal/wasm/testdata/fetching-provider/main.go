// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Fixture fetching-provider plugin for the Aggrega Wasm runtime, built with the
// standard Go toolchain for GOOS=wasip1.
//
// It is a provider guest whose render REQUESTS AN EXTERNAL INPUT, which is the
// one shape that makes the render-time fetch seam observable in emitted bytes.
// Every other provider fixture renders without asking for external content, so
// under all of them a surface that attaches no seam and a surface that attaches
// one emit identical output — which is exactly why a surface could go without a
// seam and no comparison could see it.
//
// The render emits one file whose bytes are the fetched content under a fixed
// prefix. A fetch that fails emits the failure text instead of halting, and that
// choice is what makes the fixture discriminating rather than merely red: a
// surface with no seam renders the not-available envelope's message, a surface
// with a frozen seam and a pinned reference renders the content, and a surface
// with a frozen seam and no pin renders the refusal. Three distinguishable
// outputs, all of them successful renders, so a byte comparison tells them apart
// instead of one of them simply erroring.
//
// It emits a SECOND file carrying this guest's own revision stamp, and that file
// is not decoration. A parity test over a shared fixture proves the two sides
// AGREE, never that both RAN: a guest that compiles and is never invoked reports
// nothing at all, and the comparison then certifies something that never
// happened. The stamp only reaches an emitted byte if this render executed, so
// the host asserting it against its own independently-declared copy is what makes
// "the guest ran, at the revision the host was written against" observable rather
// than assumed.
//
// The wire payloads are hand-written rather than taken from the plugin kit, on
// the same terms as the other guests under this directory: the guest is built
// with GOWORK=off against its own go.mod, so it shares no module with the host
// tests that drive it. That includes the fetch reply, which carries canonical
// CBOR: the guest reads the one small map it needs with its own reader rather
// than with the generated decoder.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o fetching-provider.wasm ./
package main

import (
	"encoding/json"
	"errors"
	"fmt"

	pdk "github.com/extism/go-pdk"
)

const (
	// capability is the provider capability a Recipe names to route a node here.
	capability = "fetching"

	// externalInputURL is the reference every render of this fixture requests.
	//
	// 🔴 DELIBERATE DUPLICATE, recorded so it is not "unified" away: the host-side
	// test that drives this guest declares the same URL, and it must, because a
	// Wasm guest built with GOWORK=off against its own module shares no Go
	// constant with the host that loads it. The duplication is a language-boundary
	// one, not an oversight. Anything that changes it here changes it there, and a
	// mismatch shows up immediately as a declined reference rather than silently.
	externalInputURL = "https://fixtures.invalid/external-input.txt"

	// outputPath is the single file the render emits. The .txt extension keeps the
	// host inferring a plain-text format, so nothing downstream tries to parse the
	// fetched bytes as a structured document.
	outputPath = "external-input.txt"

	// contentPrefix precedes the fetch outcome in the emitted file, so the emitted
	// bytes are never empty and a reader of a diff can see which half moved.
	contentPrefix = "external-input:"

	// revisionPath is the second file the render emits, carrying nothing but the
	// stamp below. It is a file of its own rather than another line in the first
	// one so that the seam's outcome and the guest's identity move independently:
	// a change to either is readable in a diff without dragging the other with it.
	revisionPath = "fixture-revision.txt"

	// fixtureRevision identifies the contract THIS guest presents to the host test
	// that drives it — the files it emits, their names, the shape of their bytes,
	// and the encoding of the reply it reads them from. Bump it in the same edit
	// that changes any of those.
	//
	// 🔴 DELIBERATE DUPLICATE, on the same terms as externalInputURL above and
	// recorded so it is not "unified" away: the host test declares its own copy of
	// this string and asserts the emitted stamp equals it. The duplication IS the
	// mechanism — a cross-check that read one shared constant could not disagree
	// with itself, and disagreement is the whole signal. A guest edited without the
	// host (or a stale .wasm served from the fixture cache) therefore fails loudly
	// here instead of quietly certifying a comparison against a contract that has
	// moved.
	fixtureRevision = "fetching-provider/2"
)

// providerDescriptor is the fixed describe() self-report. The external-input
// host function is declared on the render export because the host cross-checks
// a guest's declarations against the functions it actually imports, and refuses
// a guest that imports one it did not declare.
const providerDescriptor = `{` +
	`"abi_version":"v1alpha1",` +
	`"plugin_type":"provider",` +
	`"capabilities":["` + capability + `"],` +
	`"description":"Renders one file whose bytes are an external input fetched through the host.",` +
	`"exports":[` +
	`{"name":"render_node","input_pattern":"b",` +
	`"host_functions":["aggrega_fetch_external_input"],` +
	`"description":"emits the fetched external input and this guest's revision stamp"}` +
	`],` +
	`"contract":{"input_kinds":{"synthesized":true},"boundary_assembly":"none",` +
	`"reusable_instance":true}` +
	`}`

// renderedFile mirrors the wire shape of one returned output file. Content is
// []byte so the standard encoder base64-encodes it, which is the encoding the
// wire declares for those bytes.
type renderedFile struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
}

// renderNodeOutput is the sliver of the render reply this guest returns: the
// files alone. Every other field is optional and absent.
type renderNodeOutput struct {
	Files []renderedFile `json:"files"`
}

// fetchEnvelope is the reply the external-input host function writes into guest
// memory: the fetched bytes on success, or an error message and its code on
// failure. Content is omitted when the payload is empty, so a reply carrying no
// failure field is a success whatever it carries.
type fetchEnvelope struct {
	Content []byte
	Error   string
	Code    string
}

// aggregaFetchExternalInput is the host-imported external-input fetch binding.
// Signature: (url_ptr i64) -> envelope_ptr i64. The URL is UTF-8 text; the
// reply is one canonical-CBOR map. Offset 0 means the host delivered no
// envelope at all.
//
//go:wasmimport extism:host/user aggrega_fetch_external_input
func aggregaFetchExternalInput(urlPtr uint64) uint64

// fetchExternalInput asks the host for the fixture's external input and returns
// either its bytes or the text of the failure. It returns text rather than an
// error because the render carries on either way: the whole point of the fixture
// is that the OUTCOME reaches the emitted bytes, so a caller comparing renders
// can see which outcome each surface produced.
func fetchExternalInput(url string) string {
	mem := pdk.AllocateString(url)
	defer mem.Free()

	offset := aggregaFetchExternalInput(uint64(mem.Offset()))
	if offset == 0 {
		return "no-envelope"
	}
	envMem := pdk.FindMemory(offset)
	envBytes := envMem.ReadBytes()
	envMem.Free()

	env, err := decodeFetchEnvelope(envBytes)
	if err != nil {
		return "undecodable-envelope: " + err.Error()
	}
	if env.Error != "" {
		return env.Error
	}
	if env.Code != "" {
		return "failure: " + env.Code
	}
	return string(env.Content)
}

// decodeFetchEnvelope reads the fetch reply: a definite-length CBOR map whose
// keys are text strings and whose values are the content as a byte string and
// the two failure fields as text strings. A key it does not know is refused
// rather than skipped, because a reply this reader half-understood would render
// an outcome nobody chose.
func decodeFetchEnvelope(b []byte) (fetchEnvelope, error) {
	var env fetchEnvelope
	r := cborReader{data: b}
	n, err := r.head(5)
	if err != nil {
		return env, err
	}
	for i := uint64(0); i < n; i++ {
		key, err := r.str(3)
		if err != nil {
			return env, err
		}
		switch string(key) {
		case "content":
			v, err := r.str(2)
			if err != nil {
				return env, err
			}
			env.Content = v
		case "error":
			v, err := r.str(3)
			if err != nil {
				return env, err
			}
			env.Error = string(v)
		case "hostfunc_error_code":
			v, err := r.str(3)
			if err != nil {
				return env, err
			}
			env.Code = string(v)
		default:
			return env, fmt.Errorf("unexpected key %q", key)
		}
	}
	if r.pos != len(r.data) {
		return env, errors.New("trailing bytes after the reply")
	}
	return env, nil
}

// cborReader reads the few CBOR items the fetch reply is made of.
type cborReader struct {
	data []byte
	pos  int
}

// head reads one item head of the given major type and returns its argument:
// a length for a string, a pair count for a map. Only definite lengths occur on
// this wire.
func (r *cborReader) head(major byte) (uint64, error) {
	if r.pos >= len(r.data) {
		return 0, errors.New("truncated reply")
	}
	ib := r.data[r.pos]
	r.pos++
	if ib>>5 != major {
		return 0, fmt.Errorf("major type %d where %d was expected", ib>>5, major)
	}
	info := ib & 0x1f
	var width int
	switch {
	case info < 24:
		return uint64(info), nil
	case info == 24:
		width = 1
	case info == 25:
		width = 2
	case info == 26:
		width = 4
	case info == 27:
		width = 8
	default:
		return 0, fmt.Errorf("unsupported length encoding %d", info)
	}
	if r.pos+width > len(r.data) {
		return 0, errors.New("truncated reply")
	}
	var v uint64
	for _, c := range r.data[r.pos : r.pos+width] {
		v = v<<8 | uint64(c)
	}
	r.pos += width
	return v, nil
}

// str reads one byte string (major type 2) or text string (major type 3) and
// returns its bytes.
func (r *cborReader) str(major byte) ([]byte, error) {
	n, err := r.head(major)
	if err != nil {
		return nil, err
	}
	if n > uint64(len(r.data)-r.pos) {
		return nil, errors.New("truncated reply")
	}
	v := r.data[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return v, nil
}

//go:wasmexport describe
func describe() int32 {
	pdk.OutputString(providerDescriptor)
	return 0
}

//go:wasmexport render_node
func render_node() int32 {
	out := renderNodeOutput{Files: []renderedFile{
		{
			Path:    outputPath,
			Content: []byte(contentPrefix + fetchExternalInput(externalInputURL)),
		},
		{
			Path:    revisionPath,
			Content: []byte(fixtureRevision),
		},
	}}
	raw, err := json.Marshal(out)
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(raw)
	return 0
}

func main() {}
