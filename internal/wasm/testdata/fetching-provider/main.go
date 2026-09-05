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
// tests that drive it.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o fetching-provider.wasm ./
package main

import (
	"encoding/base64"
	"encoding/json"

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
	// that drives it — the files it emits, their names, and the shape of their
	// bytes. Bump it in the same edit that changes any of those.
	//
	// 🔴 DELIBERATE DUPLICATE, on the same terms as externalInputURL above and
	// recorded so it is not "unified" away: the host test declares its own copy of
	// this string and asserts the emitted stamp equals it. The duplication IS the
	// mechanism — a cross-check that read one shared constant could not disagree
	// with itself, and disagreement is the whole signal. A guest edited without the
	// host (or a stale .wasm served from the fixture cache) therefore fails loudly
	// here instead of quietly certifying a comparison against a contract that has
	// moved.
	fixtureRevision = "fetching-provider/1"
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
// memory: either base64 content or an error message, never both.
type fetchEnvelope struct {
	Content *string `json:"content"`
	Error   string  `json:"error"`
}

// aggregaFetchExternalInput is the host-imported external-input fetch binding.
// Signature: (url_ptr i64) -> envelope_ptr i64. Offset 0 means the host
// delivered no envelope at all.
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

	var env fetchEnvelope
	if err := json.Unmarshal(envBytes, &env); err != nil {
		return "undecodable-envelope: " + err.Error()
	}
	if env.Error != "" {
		return env.Error
	}
	if env.Content == nil {
		return "empty-envelope"
	}
	payload, err := base64.StdEncoding.DecodeString(*env.Content)
	if err != nil {
		return "undecodable-content: " + err.Error()
	}
	return string(payload)
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
