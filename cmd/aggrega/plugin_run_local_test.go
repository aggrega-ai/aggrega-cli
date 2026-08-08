// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

// wantParseTree is the parse-go guest's response for the input payload
// {"format":"demo","bytes":"aGVsbG8="} — the guest echoes the base64-decoded
// source ("hello") back inside a small structured tree.
const wantParseTree = `{"tree":{"format":"demo","source":"hello"}}`

// parsePayload is the input the parse-go guest echoes; "aGVsbG8=" is base64 for
// "hello".
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
	if got := strings.TrimSpace(out); got != wantParseTree {
		t.Fatalf("guest bytes = %q; want %q", got, wantParseTree)
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
	if got := strings.TrimSpace(out); got != wantParseTree {
		t.Fatalf("guest bytes = %q; want %q", got, wantParseTree)
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
			t.Fatalf("inline and @file payloads produced different bytes:\ninline: %q\n@file:  %q", inlineOut, fileOut)
		}
		if got := strings.TrimSpace(inlineOut); got != wantParseTree {
			t.Fatalf("guest bytes = %q; want %q", got, wantParseTree)
		}
	})

	t.Run("omitted --with defaults to empty object", func(t *testing.T) {
		t.Parallel()
		guest := wasmtest.EnsureFixtureBuilt(t, "policy-allow-go.wasm")

		out, err := runLocal(t, "--plugin", guest, "--export", "validate")
		if err != nil {
			t.Fatalf("default-{} run against a guest that returns bytes on {} failed: %v", err)
		}
		if got := strings.TrimSpace(out); got != `{"results":[]}` {
			t.Fatalf("guest bytes = %q; want the empty-results verdict", got)
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
		if got := strings.TrimSpace(out); got != wantParseTree {
			t.Fatalf("guest bytes = %q; want %q", got, wantParseTree)
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

	if _, err := runLocal(t, "--plugin", guest, "--export", "validate"); err != nil {
		t.Fatalf("a clean guest run should yield command success; got %v", err)
	}
}
