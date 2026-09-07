// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"strings"
	"testing"
)

// importFixture is a Server over a scratch workspace with the machine-wide layer
// redirected inside the scratch tree.
//
// It reuses the registry fixture rather than standing up a second one, for the
// reason that fixture states: the redirect is what keeps a run from reading the
// configuration this machine happens to carry. No importer is wired, which is
// deliberate — every refusal asserted here is raised before an importer is
// resolved, so a fixture that stood one up would prove less about the ordering.
func importFixture(t *testing.T) *Server {
	t.Helper()
	srv, _, _ := registryWriteFixture(t, "{}\n")
	return srv
}

// TestImportRefusesARequestNamingNoOutputDirectory drives the required-field
// refusals on aggrega/import, on THIS surface's spellings.
//
// outputDir is a required request field rather than one this surface resolves a
// default for, and the reporting row is the one worth stating: reporting writes
// nothing, which makes it the case most easily mistaken for one that needs no
// destination. It still needs it, because each drafted file names the path it is
// FOR and that path is relative to the directory the request named — so a report
// without it would answer with drafts belonging to nowhere. The sibling
// generation method carries the same contract for the same reason.
//
// The refusals name request fields and never a command-line flag: a caller here
// cannot act on a flag it has no way to send.
func TestImportRefusesARequestNamingNoOutputDirectory(t *testing.T) {
	t.Parallel()
	srv := importFixture(t)

	for _, tc := range []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"no output directory", map[string]any{"source": "flow.json"}, "outputDir is required"},
		{"reporting still needs an output directory",
			map[string]any{"source": "flow.json", "report": true}, "outputDir is required"},
		{"reporting with a format still needs an output directory",
			map[string]any{"source": "flow.json", "format": "demo", "report": true}, "outputDir is required"},
		{"no source", map[string]any{"outputDir": "drafts"}, "source is required"},
		{"neither", map[string]any{}, "source is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := callDaemonMethod(t, srv, "aggrega/import", tc.params)
			if resp.Error == nil {
				t.Fatal("aggrega/import accepted a request missing a required field")
			}
			if resp.Error.Code != jsonRPCInvalidParams {
				t.Errorf("refusal code = %d; want %d, the invalid-params code every "+
					"missing-field refusal here answers with", resp.Error.Code, jsonRPCInvalidParams)
			}
			if !strings.Contains(resp.Error.Message, tc.want) {
				t.Errorf("refusal = %q; want it to contain %q", resp.Error.Message, tc.want)
			}
			if strings.Contains(resp.Error.Message, "--output-dir") {
				t.Errorf("the refusal names a command-line flag a request cannot send: %q", resp.Error.Message)
			}
		})
	}
}

// TestImportPassesTheRequiredFieldGateWhenBothFieldsAreSent is the positive
// control for the refusals above, and it is what keeps them from passing on a
// handler that refuses everything.
//
// It asserts only that execution reached PAST the required-field gate, which is
// this test's subject: the fixture wires no plugin resolver, so the request
// stops at that wiring rather than at its own parameters, and it stops with a
// different code and a message naming something other than a missing field.
// What the write itself produces is proven where the write lives — against the
// real importer guest — rather than here.
func TestImportPassesTheRequiredFieldGateWhenBothFieldsAreSent(t *testing.T) {
	t.Parallel()
	srv := importFixture(t)

	resp := callDaemonMethod(t, srv, "aggrega/import", map[string]any{
		"source":    "flow.json",
		"outputDir": "drafts",
	})
	if resp.Error == nil {
		t.Fatal("the fixture wires no importer, so this request cannot have succeeded")
	}
	if resp.Error.Code == jsonRPCInvalidParams {
		t.Fatalf("a request carrying both required fields was refused as invalid params: %q", resp.Error.Message)
	}
	if strings.Contains(resp.Error.Message, "is required") {
		t.Fatalf("a request carrying both required fields drew a missing-field refusal: %q", resp.Error.Message)
	}
}
