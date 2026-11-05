// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// TestBuildAdvisories_CarriesSensitivityNotConveyed proves the advisory reaches
// the assistant-facing tool protocol as a payload entry rather than stopping at
// a terminal. A caller here has no terminal at all, so a finding that did not
// ride the payload is a finding that was not made.
//
// It asserts the marshalled JSON rather than the Go value, because what a caller
// receives is the bytes: an entry whose class code were dropped at the wire
// boundary would satisfy a field-level assertion and still leave the caller
// unable to match on the class.
func TestBuildAdvisories_CarriesSensitivityNotConveyed(t *testing.T) {
	t.Parallel()

	d := rendercore.SensitivityNotConveyedDiagnostic("sens-mapped", "values.dbPassword")
	d.Location = &renderer.DiagnosticLocation{Instance: "mapped", Kind: "helm"}

	got := buildAdvisories([]renderer.Diagnostic{d})
	if len(got) != 1 {
		t.Fatalf("mapped %d advisories; want 1", len(got))
	}
	if got[0].Code != renderer.SensitivityNotConveyedCode {
		t.Errorf("payload code = %q; want %q — a caller matches on the class by this field",
			got[0].Code, renderer.SensitivityNotConveyedCode)
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	wire := string(encoded)
	for _, want := range []string{
		`"code":"sensitivity-not-conveyed"`,
		"values.dbPassword",
		"sens-mapped",
		"valuesMapping",
		// the node and the target, which the one formatter puts on the message
		`node \"mapped\" (provider \"helm\")`,
	} {
		if !strings.Contains(wire, want) {
			t.Errorf("payload bytes do not carry %s:\n%s", want, wire)
		}
	}
	// The control on the assertion above: an empty advisory list maps to nothing
	// at all, so the presence assertions are reading a mapping that ran rather
	// than a field that is always populated.
	if buildAdvisories(nil) != nil {
		t.Error("an empty advisory list must map to no payload entries")
	}
}
