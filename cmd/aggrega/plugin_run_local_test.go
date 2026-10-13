// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

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
const parserEntity = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: demo-parser
  version: "1.0.0"
  pluginType: parser
spec:
  abiVersion: v1alpha1
  exports:
    - name: parse
`

// parserEntityClaimsUndeclaredExport keeps the matching pluginType but claims a
// second export the guest does not advertise, so the load-time cross-check halts
// before the guest runs.
const parserEntityClaimsUndeclaredExport = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: demo-parser
  version: "1.0.0"
  pluginType: parser
spec:
  abiVersion: v1alpha1
  exports:
    - name: parse
    - name: transpile
`

// parserEntityWrongType claims a pluginType (governance) the parser guest's
// describe() does not report, so the type gate halts before the guest runs.
const parserEntityWrongType = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: demo-parser
  version: "1.0.0"
  pluginType: governance
spec:
  abiVersion: v1alpha1
  exports:
    - name: parse
`

// wantParseResponse is the parse-go guest's response for the input payload
// {"format":"demo","bytes":"aGVsbG8="}: the guest echoes the source ("hello")
// back inside a small structured tree. The parser socket carries canonical CBOR
// in both directions, so the command converts the authored JSON into that
// encoding before the guest runs, and the guest answers in it. The expected bytes
// are what the response shape encodes to — an independent statement of them —
// rather than the command's output decoded back.
func wantParseResponse(t *testing.T) string {
	t.Helper()
	want, err := abi.ParseOutput{Tree: json.RawMessage(`{"format":"demo","source":"hello"}`)}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode the expected parse response: %v", err)
	}
	return string(want)
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
		// The verdict roles answer in canonical CBOR, so the expected bytes are what
		// the empty verdict list encodes to — an independent statement of them.
		want, err := abi.PolicyOutput{Results: []abi.Diagnostic{}}.EncodeCBOR()
		if err != nil {
			t.Fatalf("encode the expected empty verdict list: %v", err)
		}
		if out != string(want) {
			t.Fatalf("guest bytes = %x; want the empty-results verdict %x", out, want)
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
// It is the test whose absence let the capability disappear twice. The command's
// payload channel was JSON-only by construction, so the moment a socket's wire
// moved the published invocation started failing at the guest's own decoder —
// and nothing turned red, because no test drove that export through this command.
// A test driving only text-wire exports passes whether the conversion runs or not.
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

	// The guest's response is its own encoding, written through verbatim. Decoding
	// it back would test this command against its own conversion; comparing it to
	// what the response shape encodes to is an independent statement of the bytes.
	var neutral topology.MutatorOutput
	want, err := neutral.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode the neutral response shape: %v", err)
	}
	if out != string(want) {
		t.Fatalf("guest response = %x; want the neutral result %x", out, want)
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
		"--export", wasm.ImportExport, "--with", importPayload)
	if err != nil {
		t.Fatalf("an authored JSON payload should reach the importer's binary-wire export; got %v", err)
	}

	body := "kind: Component\nmetadata:\n  name: acme-demo\n"
	want, err := abi.ImportOutput{
		Entities: []abi.EmittedEntity{{Kind: "Component", Name: "acme-demo", Mode: "draft", Body: body}},
		Files:    []abi.ScaffoldFile{{Path: "acme-demo.yaml", Content: body}},
	}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode the expected response: %v", err)
	}
	if out != string(want) {
		t.Fatalf("guest response = %x; want the drafted entity and file %x", out, want)
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
