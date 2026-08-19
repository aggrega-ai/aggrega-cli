// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm_test

import (
	"errors"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// TestGuestDiagnosticSeveritiesAdmitsEveryDeclaredSeverity proves closing the set
// costs a conforming guest nothing: each severity the shared diagnostic contract
// declares passes, and so does an output that raised no diagnostic at all.
func TestGuestDiagnosticSeveritiesAdmitsEveryDeclaredSeverity(t *testing.T) {
	for _, sev := range []string{abi.SeverityHalt, abi.SeverityWarn, abi.SeverityInfo} {
		diags := []abi.Diagnostic{{Severity: sev, Message: "m"}}
		if err := wasm.GuestDiagnosticSeverities("proof://plugin", "render_node", diags); err != nil {
			t.Errorf("severity %q was refused: %v", sev, err)
		}
	}
	for _, empty := range [][]abi.Diagnostic{nil, {}} {
		if err := wasm.GuestDiagnosticSeverities("proof://plugin", "render_node", empty); err != nil {
			t.Errorf("an output with no diagnostics was refused: %v", err)
		}
	}
	// A mixed list of declared severities passes as a whole, so the check does not
	// depend on which entry it reaches first.
	mixed := []abi.Diagnostic{
		{Severity: abi.SeverityInfo, Message: "a"},
		{Severity: abi.SeverityWarn, Message: "b"},
		{Severity: abi.SeverityHalt, Message: "c"},
	}
	if err := wasm.GuestDiagnosticSeverities("proof://plugin", "render_node", mixed); err != nil {
		t.Errorf("a list of declared severities was refused: %v", err)
	}
}

// TestGuestDiagnosticSeveritiesRefusesAnUndeclaredSeverity proves the refusal is
// loud and specific: it wraps the sentinel a caller matches on, and its message
// names the plugin, the export, the offending value, and the values the contract
// declares — so an author reads which plugin sent what and what it should have
// sent instead, rather than watching a diagnostic disappear.
func TestGuestDiagnosticSeveritiesRefusesAnUndeclaredSeverity(t *testing.T) {
	for _, sev := range []string{
		"critical",       // a plausible value from another tool's vocabulary
		"error",          // the editor wire's spelling, which this wire does not use
		"warning",        // the deprecation marker's spelling, likewise
		"HALT",           // right word, wrong case
		"",               // absent, which decodes to the empty string
		" halt",          // a declared value with stray whitespace
		"halt\u0000evil", // a value carrying an embedded NUL
	} {
		diags := []abi.Diagnostic{{Severity: sev, Message: "m"}}
		err := wasm.GuestDiagnosticSeverities("oci://registry.example/policy/house-rules", "evaluate", diags)
		if err == nil {
			t.Errorf("severity %q was accepted; want a refusal", sev)
			continue
		}
		if !errors.Is(err, wasm.ErrWasmDiagnosticSeverity()) {
			t.Errorf("severity %q: err = %v, want it to wrap the undeclared-severity sentinel", sev, err)
		}
		for _, want := range []string{
			"oci://registry.example/policy/house-rules",
			`"evaluate"`,
			abi.SeverityHalt, abi.SeverityWarn, abi.SeverityInfo,
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("severity %q: message does not name %q:\n  %v", sev, want, err)
			}
		}
	}
}

// TestGuestDiagnosticSeveritiesNamesTheOffendingEntry proves the refusal points at
// the entry that broke the contract rather than at the list: a guest whose third
// diagnostic is the bad one is told so, which is what makes a long output
// debuggable.
func TestGuestDiagnosticSeveritiesNamesTheOffendingEntry(t *testing.T) {
	diags := []abi.Diagnostic{
		{Severity: abi.SeverityInfo, Message: "fine"},
		{Severity: abi.SeverityWarn, Message: "also fine"},
		{Severity: "blocker", Message: "the offender"},
	}
	err := wasm.GuestDiagnosticSeverities("proof://plugin", "lint", diags)
	if err == nil {
		t.Fatal("an undeclared severity in the third entry was accepted")
	}
	if !strings.Contains(err.Error(), `"blocker"`) {
		t.Errorf("message does not carry the offending value: %v", err)
	}
	if !strings.Contains(err.Error(), "diagnostic 2") {
		t.Errorf("message does not locate the offending entry: %v", err)
	}
}
