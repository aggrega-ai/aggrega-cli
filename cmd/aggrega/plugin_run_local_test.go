// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// runLocal builds the run-local command in isolation, runs it with the given
// argument vector, and returns the guest bytes written to stdout plus the
// command's error. Usage and error output are silenced so the returned error is
// the sole signal for the failure-path assertions and stdout carries only the
// guest's own bytes on the success path.
func runLocal(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newPluginRunLocalCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), err
}

// parserEntity is a Plugin entity whose declared metadata.pluginType and export
// contract agree with the parse-go guest's describe() self-report (a parser
// advertising the parse export). It is the positive entity-form fixture: the
// entity's contract is cross-checked against the guest and the guest then runs.
const parserEntity = `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: demo-parser
  version: "1.0.0"
  pluginType: parser
spec:
  abiVersion: v1beta1
  exports:
    - name: parse
`

// parserEntityClaimsUndeclaredExport keeps the matching pluginType but claims a
// second export the guest does not advertise, so the load-time cross-check halts
// before the guest runs.
const parserEntityClaimsUndeclaredExport = `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: demo-parser
  version: "1.0.0"
  pluginType: parser
spec:
  abiVersion: v1beta1
  exports:
    - name: parse
    - name: transpile
`

// parserEntityWrongType claims a pluginType (governance) the parser guest's
// describe() does not report, so the type gate halts before the guest runs.
const parserEntityWrongType = `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: demo-parser
  version: "1.0.0"
  pluginType: governance
spec:
  abiVersion: v1beta1
  exports:
    - name: parse
`

// wantAnswer is the text the command prints for a socket export's answer: the
// answer value written as JSON indented by two spaces, markup characters as
// written, one object followed by a newline. It is built from the expected value
// itself — an independent statement of what the guest answered — rather than
// from the command's own output read back.
func wantAnswer(t *testing.T, answer any) string {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(answer); err != nil {
		t.Fatalf("write the expected answer: %v", err)
	}
	return buf.String()
}

// wantParseResponse is the parse-go guest's response for the input payload
// {"format":"demo","bytes":"aGVsbG8="}: the guest echoes the source ("hello")
// back inside a small structured tree. The parser socket carries canonical CBOR
// in both directions, so the command converts the authored JSON into that
// encoding before the guest runs, the guest answers in it, and the command
// prints that answer as JSON.
func wantParseResponse(t *testing.T) string {
	t.Helper()
	return wantAnswer(t, abi.ParseOutput{Tree: json.RawMessage(`{"format":"demo","source":"hello"}`)})
}

// parsePayload is the input the parse-go guest echoes; "aGVsbG8=" is base64 for
// "hello", the form the authored JSON carries a byte field in.
const parsePayload = `{"format":"demo","bytes":"aGVsbG8="}`

// TestRunLocal_ArtifactForm exercises the artifact form end to end against a real
// checked-in guest: no positional entity, the guest addressed by --plugin, and
// its parse export called with an inline payload. The guest's own response bytes
// reach stdout verbatim.
func TestRunLocal_ArtifactForm(t *testing.T) {
	t.Parallel()
	guest := wasmtest.EnsureFixtureBuilt(t, "parse-go.wasm")

	out, err := runLocal(t, "--plugin", guest, "--export", "parse", "--with", parsePayload)
	if err != nil {
		t.Fatalf("artifact form should run the guest and succeed; got %v", err)
	}
	if want := wantParseResponse(t); out != want {
		t.Fatalf("guest bytes = %x; want %x", out, want)
	}
}

// TestRunLocal_EntityForm proves the entity form: a Plugin entity whose declared
// pluginType and export contract agree with the guest's describe() passes the
// cross-check and the guest then runs, its bytes reaching stdout. The guest bytes
// still come from --plugin; the entity supplies only the gate.
func TestRunLocal_EntityForm(t *testing.T) {
	t.Parallel()
	guest := wasmtest.EnsureFixtureBuilt(t, "parse-go.wasm")
	entity := writeKindFixture(t, "plugin", parserEntity)

	out, err := runLocal(t, "--plugin", guest, "--export", "parse", "--with", parsePayload, entity)
	if err != nil {
		t.Fatalf("agreeing entity form should run the guest and succeed; got %v", err)
	}
	if want := wantParseResponse(t); out != want {
		t.Fatalf("guest bytes = %x; want %x", out, want)
	}
}

// TestRunLocal_EntityForm_ContractMismatch proves the entity-form cross-check is
// a loud halt: an entity claiming an export the guest does not declare halts the
// load before the guest runs, wrapping ErrWasmContractMismatch.
func TestRunLocal_EntityForm_ContractMismatch(t *testing.T) {
	t.Parallel()
	guest := wasmtest.EnsureFixtureBuilt(t, "parse-go.wasm")
	entity := writeKindFixture(t, "plugin", parserEntityClaimsUndeclaredExport)

	_, err := runLocal(t, "--plugin", guest, "--export", "parse", "--with", parsePayload, entity)
	if !errors.Is(err, wasm.ErrWasmContractMismatch()) {
		t.Fatalf("a claim the guest does not declare should halt with ErrWasmContractMismatch; got %v", err)
	}
}

// TestRunLocal_EntityForm_PluginTypeMismatch proves a diverging metadata.pluginType
// halts the load, wrapping ErrWasmPluginTypeMismatch: the entity claims policy
// where the guest reports parser.
func TestRunLocal_EntityForm_PluginTypeMismatch(t *testing.T) {
	t.Parallel()
	guest := wasmtest.EnsureFixtureBuilt(t, "parse-go.wasm")
	entity := writeKindFixture(t, "plugin", parserEntityWrongType)

	_, err := runLocal(t, "--plugin", guest, "--export", "parse", "--with", parsePayload, entity)
	if !errors.Is(err, wasm.ErrWasmPluginTypeMismatch()) {
		t.Fatalf("a diverging pluginType should halt with ErrWasmPluginTypeMismatch; got %v", err)
	}
}

// TestRunLocal_EntityForm_RejectsType proves --type cannot be combined with the
// entity form, because the entity's metadata.pluginType is the single source of
// the type expectation.
func TestRunLocal_EntityForm_RejectsType(t *testing.T) {
	t.Parallel()
	guest := wasmtest.EnsureFixtureBuilt(t, "parse-go.wasm")
	entity := writeKindFixture(t, "plugin", parserEntity)

	_, err := runLocal(t, "--plugin", guest, "--export", "parse", "--type", "parser", entity)
	if err == nil {
		t.Fatalf("--type with an entity argument should be rejected")
	}
	if !strings.Contains(err.Error(), "--type cannot be combined") {
		t.Fatalf("rejection should name the --type/entity conflict; got %v", err)
	}
}

// TestRunLocal_PayloadVariants proves the three --with forms resolve to the same
// bytes the guest receives: an inline JSON value and a @file reference to the
// same JSON produce identical guest output, and an omitted --with defaults to the
// empty object {} — exercised against a guest that returns bytes on {}.
func TestRunLocal_PayloadVariants(t *testing.T) {
	t.Parallel()

	t.Run("inline and @file deliver identical bytes", func(t *testing.T) {
		t.Parallel()
		guest := wasmtest.EnsureFixtureBuilt(t, "parse-go.wasm")

		inlineOut, err := runLocal(t, "--plugin", guest, "--export", "parse", "--with", parsePayload)
		if err != nil {
			t.Fatalf("inline payload run failed: %v", err)
		}

		payloadFile := filepath.Join(t.TempDir(), "payload.json")
		if err := os.WriteFile(payloadFile, []byte(parsePayload), 0o644); err != nil {
			t.Fatalf("write payload file: %v", err)
		}
		fileOut, err := runLocal(t, "--plugin", guest, "--export", "parse", "--with", "@"+payloadFile)
		if err != nil {
			t.Fatalf("@file payload run failed: %v", err)
		}

		if inlineOut != fileOut {
			t.Fatalf("inline and @file payloads produced different bytes:\ninline: %x\n@file:  %x", inlineOut, fileOut)
		}
		if want := wantParseResponse(t); inlineOut != want {
			t.Fatalf("guest bytes = %x; want %x", inlineOut, want)
		}
	})

	t.Run("omitted --with defaults to empty object", func(t *testing.T) {
		t.Parallel()
		guest := wasmtest.EnsureFixtureBuilt(t, "policy-allow-go.wasm")

		out, err := runLocal(t, "--plugin", guest, "--export", "verdict_ir")
		if err != nil {
			t.Fatalf("default-{} run against a guest that returns bytes on {} failed: %v", err)
		}
		// The verdict roles answer in canonical CBOR, and the command prints the
		// answer as JSON: here the empty verdict list.
		if want := wantAnswer(t, abi.PolicyOutput{Results: []abi.Diagnostic{}}); out != want {
			t.Fatalf("printed answer =\n%s\nwant the empty-results verdict\n%s", out, want)
		}
	})
}

// TestRunLocal_MalformedInlinePayload proves a malformed inline --with value is
// rejected before the guest loads: the resolved payload is validated as JSON up
// front.
func TestRunLocal_MalformedInlinePayload(t *testing.T) {
	t.Parallel()
	guest := wasmtest.EnsureFixtureBuilt(t, "parse-go.wasm")

	out, err := runLocal(t, "--plugin", guest, "--export", "parse", "--with", "{")
	if err == nil {
		t.Fatalf("a malformed inline payload should be rejected")
	}
	if !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("rejection should name the invalid JSON; got %v", err)
	}
	if out != "" {
		t.Fatalf("no guest bytes should be written when the payload is rejected before load; got %q", out)
	}
}

// TestRunLocal_TypeGate proves the artifact-form --type gate: a matching --type
// runs the guest, and a mismatched --type halts the load wrapping
// ErrWasmPluginTypeMismatch.
func TestRunLocal_TypeGate(t *testing.T) {
	t.Parallel()

	t.Run("matching type runs", func(t *testing.T) {
		t.Parallel()
		guest := wasmtest.EnsureFixtureBuilt(t, "parse-go.wasm")
		out, err := runLocal(t, "--plugin", guest, "--export", "parse", "--type", "parser", "--with", parsePayload)
		if err != nil {
			t.Fatalf("a matching --type should run the guest; got %v", err)
		}
		if want := wantParseResponse(t); out != want {
			t.Fatalf("guest bytes = %x; want %x", out, want)
		}
	})

	t.Run("mismatched type halts", func(t *testing.T) {
		t.Parallel()
		guest := wasmtest.EnsureFixtureBuilt(t, "parse-go.wasm")
		_, err := runLocal(t, "--plugin", guest, "--export", "parse", "--type", "governance", "--with", parsePayload)
		if !errors.Is(err, wasm.ErrWasmPluginTypeMismatch()) {
			t.Fatalf("a mismatched --type should halt with ErrWasmPluginTypeMismatch; got %v", err)
		}
	})
}

// TestRunLocal_ExportNotDeclared proves an --export the guest does not advertise
// halts with a message that enumerates the guest's declared exports.
func TestRunLocal_ExportNotDeclared(t *testing.T) {
	t.Parallel()
	guest := wasmtest.EnsureFixtureBuilt(t, "parse-go.wasm")

	_, err := runLocal(t, "--plugin", guest, "--export", "bogus")
	if err == nil {
		t.Fatalf("an undeclared export should halt")
	}
	msg := err.Error()
	if !strings.Contains(msg, "does not declare export") || !strings.Contains(msg, "bogus") {
		t.Fatalf("error should name the missing export; got %v", err)
	}
	if !strings.Contains(msg, "parse") {
		t.Fatalf("error should enumerate the guest's declared exports (parse); got %v", err)
	}
}

// TestRunLocal_ExitStatusSuccess proves a clean guest run surfaces as command
// success (no error). The guest exits zero, so the command returns nil.
//
// The complementary case — a clean non-zero guest exit surfacing as an
// exitCodeError — is not exercised end to end: no checked-in guest reachable
// through the build-profile load path this command uses produces a clean
// non-zero exit (the reference guests return zero on success and trap on
// failure, and a streaming CLI guest is refused before describe()). The
// non-zero-exit propagation is covered at the command-tree boundary by the
// installed-plugin exit-code tests.
func TestRunLocal_ExitStatusSuccess(t *testing.T) {
	t.Parallel()
	guest := wasmtest.EnsureFixtureBuilt(t, "policy-allow-go.wasm")

	if _, err := runLocal(t, "--plugin", guest, "--export", "verdict_ir"); err != nil {
		t.Fatalf("a clean guest run should yield command success; got %v", err)
	}
}

// labelPayload is the authored payload for a label-transform guest: the shape
// its export reads, written as JSON exactly as an operator writes it. It is the
// fixture the published plugin-kit walkthroughs hand this command.
const labelPayload = `{"compilation_context":{},"graph":{}}`

// TestRunLocal_BinaryWireExport drives an export whose wire is NOT text through
// this command, end to end against a real guest.
//
// The command's payload channel takes JSON, while a socket on the binary wire
// reads its payload in that encoding, so an invocation that skipped the
// conversion would fail at the guest's own decoder. A test driving only exports
// the command passes through unchanged passes whether the conversion runs or
// not, which is why this one drives an export the command converts.
//
// What it pins: the authored payload is JSON, the command converts it into the
// encoding the export reads, and the guest decodes it and returns its own
// response. A guest handed the wrong encoding fails inside its decoder, so the
// clean run is itself the proof that the conversion happened.
func TestRunLocal_BinaryWireExport(t *testing.T) {
	t.Parallel()
	guest := wasmtest.EnsureFixtureBuilt(t, "mutator-go.wasm")

	out, err := runLocal(t, "--plugin", guest, "--type", "governance",
		"--export", "mutate", "--with", labelPayload)
	if err != nil {
		t.Fatalf("an authored JSON payload should reach a binary-wire export; got %v", err)
	}
	if out == "" {
		t.Fatal("the guest wrote no response bytes")
	}

	// The guest answers in its socket's encoding and the command prints that
	// answer as JSON. The expectation is the neutral answer value written out,
	// an independent statement of it rather than the command's output read back.
	if want := wantAnswer(t, topology.MutatorOutput{}); out != want {
		t.Fatalf("printed answer =\n%s\nwant the neutral result\n%s", out, want)
	}
}

// importPayload is the authored payload for an importer guest: the import input
// written as JSON, its source bytes base64-encoded ("hello world") and its
// settings an ordinary JSON object.
const importPayload = `{"format":"demo","bytes":"aGVsbG8gd29ybGQ=","settings":{"target_namespace":"acme"}}`

// TestRunLocal_ImporterExport drives an importer's export through this command
// end to end against a real guest. The importer's export is fixed by its plugin
// type rather than defaulted by a governance role, and its socket carries the
// binary encoding in both directions, so it is the case the conversion has to
// reach beyond the governance roles: a guest handed the authored JSON fails inside
// its own decoder.
//
// The demo importer echoes its format and its settings' namespace into one drafted
// entity and its file, so its response shows it read what the command converted.
// That response is compared with what the response shape encodes to, an
// independent statement of the bytes, rather than decoded back.
func TestRunLocal_ImporterExport(t *testing.T) {
	t.Parallel()
	guest := wasmtest.EnsureFixtureBuilt(t, "import-go.wasm")

	out, err := runLocal(t, "--plugin", guest, "--type", "importer",
		"--export", wasm.ImportExport.Name(), "--with", importPayload)
	if err != nil {
		t.Fatalf("an authored JSON payload should reach the importer's binary-wire export; got %v", err)
	}

	body := "kind: Component\nmetadata:\n  name: acme-demo\n"
	want := wantAnswer(t, abi.ImportOutput{
		Entities: []abi.EmittedEntity{{Kind: "Component", Name: "acme-demo", Mode: "draft", Body: body}},
		Files:    []abi.ScaffoldFile{{Path: "acme-demo.yaml", Content: body}},
	})
	if out != want {
		t.Fatalf("printed answer =\n%s\nwant the drafted entity and file\n%s", out, want)
	}
}

// stepPayload is the authored payload for a pipeline step: the step input written
// as JSON, carrying one own-scope Deployment the step content-selects.
const stepPayload = `{"documents":{"own":{"deploy.yaml":{"token":"deploy.yaml","media_type":"application/yaml",` +
	`"tree":{"kind":"Deployment","metadata":{"name":"web"}}}}},"step_index":0}`

// TestRunLocal_StepExport drives a pipeline step's export through this command end
// to end against a real guest. The step's socket carries the binary encoding in
// both directions, so a guest handed the authored JSON fails inside its own
// decoder: the clean run shows the command converted it.
//
// The Go kit's step example patches the Deployment it was handed and emits a
// ConfigMap into its own set, so its response shows it read what the command
// converted. That response is compared with what the response shape encodes to,
// an independent statement of the bytes, rather than decoded back.
func TestRunLocal_StepExport(t *testing.T) {
	t.Parallel()
	guest := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")

	out, err := runLocal(t, "--plugin", guest, "--type", "pipeline-step",
		"--export", wasm.StepExport.Name(), "--with", stepPayload)
	if err != nil {
		t.Fatalf("an authored JSON payload should reach the step's binary-wire export; got %v", err)
	}

	want := wantAnswer(t, abi.FRPStepOutput{
		Patches: []abi.JSONPatchOp{{
			Scope: "own", Key: "deploy.yaml", Op: "add",
			Path: "/metadata/annotations/aggrega.ai~1pipeline-patched", Value: "true",
		}},
		SideEffects: []abi.SideEffect{{
			Kind: "emit_file", Scope: "own",
			File: &abi.SideEffectFile{
				Path:    "pipeline-configmap.yaml",
				Content: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: pipeline-emitted\ndata:\n  source: frp-step-go\n",
			},
		}},
	})
	if out != want {
		t.Fatalf("printed answer =\n%s\nwant the patch and the emitted file\n%s", out, want)
	}
}

// TestRunLocal_BinaryWireExportRejectsMalformedPayload proves the authoring
// surface is still checked as JSON for a binary-wire export: the payload is
// refused up front, by the same message every other export gets, rather than
// reaching the guest as bytes it cannot read.
func TestRunLocal_BinaryWireExportRejectsMalformedPayload(t *testing.T) {
	t.Parallel()
	guest := wasmtest.EnsureFixtureBuilt(t, "mutator-go.wasm")

	_, err := runLocal(t, "--plugin", guest, "--type", "governance",
		"--export", "mutate", "--with", `{"compilation_context":`)
	if err == nil {
		t.Fatal("a malformed authored payload should be refused before the guest runs")
	}
	if !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("refusal should name the authoring surface; got %v", err)
	}
}

// TestRunLocal_LoadsUnderTheDescribeMemoryCeiling proves the ceiling run-local
// loads a guest under: the one a describe uses, not a build's default. The parse
// guest is rewritten to declare a minimum memory above the build default and
// within the describe ceiling, and run-local still runs it and prints its answer;
// the same guest declaring a minimum above the describe ceiling is refused, so the
// ceiling is that one and not an unbounded load.
func TestRunLocal_LoadsUnderTheDescribeMemoryCeiling(t *testing.T) {
	t.Parallel()
	original, err := os.ReadFile(wasmtest.EnsureFixtureBuilt(t, "parse-go.wasm"))
	if err != nil {
		t.Fatalf("read the parse guest: %v", err)
	}

	aboveDefault := wasm.DefaultMaxMemoryPages + 64
	if aboveDefault > wasm.DescribeMaxMemoryPages {
		t.Fatalf("no room between the build default (%d pages) and the describe ceiling (%d pages) for this test",
			wasm.DefaultMaxMemoryPages, wasm.DescribeMaxMemoryPages)
	}

	t.Run("above the build default and within the describe ceiling, it runs", func(t *testing.T) {
		t.Parallel()
		guest := guestDeclaringMemory(t, original, aboveDefault)
		out, err := runLocal(t, "--plugin", guest, "--export", "parse", "--with", parsePayload)
		if err != nil {
			t.Fatalf("a guest declaring %d memory pages should load under the describe ceiling of %d; got %v",
				aboveDefault, wasm.DescribeMaxMemoryPages, err)
		}
		if want := wantParseResponse(t); out != want {
			t.Fatalf("guest bytes = %x; want %x", out, want)
		}
	})

	t.Run("above the describe ceiling, it is refused", func(t *testing.T) {
		t.Parallel()
		guest := guestDeclaringMemory(t, original, wasm.DescribeMaxMemoryPages+1)
		_, err := runLocal(t, "--plugin", guest, "--export", "parse", "--with", parsePayload)
		if err == nil {
			t.Fatalf("a guest declaring more memory than the describe ceiling of %d pages should be refused",
				wasm.DescribeMaxMemoryPages)
		}
		if !strings.Contains(err.Error(), strconv.FormatUint(uint64(wasm.DescribeMaxMemoryPages), 10)) {
			t.Fatalf("the refusal should name the describe ceiling of %d pages; got %v", wasm.DescribeMaxMemoryPages, err)
		}
	})
}

// guestDeclaringMemory writes a copy of the module wasmBytes whose one defined
// memory declares minPages as its minimum, and returns the copy's path. The
// memory section is re-encoded rather than patched in place, because the minimum
// is a variable-length integer and a larger value can need more bytes. A maximum
// the module declared is kept, raised to minPages where it is lower, so the
// rewritten module stays valid.
func guestDeclaringMemory(t *testing.T, wasmBytes []byte, minPages uint32) string {
	t.Helper()
	const memorySectionID = 5
	if len(wasmBytes) < 8 {
		t.Fatalf("not a WebAssembly module: %d bytes", len(wasmBytes))
	}
	out := append([]byte(nil), wasmBytes[:8]...)
	found := false
	for i := 8; i < len(wasmBytes); {
		id := wasmBytes[i]
		size, n := readULEB(t, wasmBytes[i+1:])
		body := wasmBytes[i+1+n : i+1+n+int(size)]
		next := i + 1 + n + int(size)
		if id != memorySectionID {
			out = append(out, wasmBytes[i:next]...)
			i = next
			continue
		}
		count, n := readULEB(t, body)
		if count != 1 {
			t.Fatalf("the guest defines %d memories; this rewrite handles exactly one", count)
		}
		flags := body[n]
		_, m := readULEB(t, body[n+1:])
		section := appendULEB([]byte{1, flags}, uint64(minPages))
		switch flags {
		case 0x00:
		case 0x01:
			maxPages, _ := readULEB(t, body[n+1+m:])
			if maxPages < uint64(minPages) {
				maxPages = uint64(minPages)
			}
			section = appendULEB(section, maxPages)
		default:
			t.Fatalf("the guest's memory carries limit flags %#x; this rewrite handles only a plain minimum, with or without a maximum", flags)
		}
		out = append(out, memorySectionID)
		out = appendULEB(out, uint64(len(section)))
		out = append(out, section...)
		found = true
		i = next
	}
	if !found {
		t.Fatal("the guest defines no memory to rewrite")
	}
	path := filepath.Join(t.TempDir(), "guest.wasm")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write the rewritten guest: %v", err)
	}
	return path
}

// readULEB decodes one unsigned LEB128 integer from the start of b, returning the
// value and the number of bytes it occupied.
func readULEB(t *testing.T, b []byte) (uint64, int) {
	t.Helper()
	var v uint64
	for i, shift := 0, uint(0); i < len(b) && shift < 64; i, shift = i+1, shift+7 {
		v |= uint64(b[i]&0x7f) << shift
		if b[i]&0x80 == 0 {
			return v, i + 1
		}
	}
	t.Fatal("truncated LEB128 integer in the guest module")
	return 0, 0
}

// appendULEB appends v to b as an unsigned LEB128 integer.
func appendULEB(b []byte, v uint64) []byte {
	for {
		c := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b = append(b, c|0x80)
			continue
		}
		return append(b, c)
	}
}
