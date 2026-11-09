// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"strings"
	"testing"
)

// TestValidateRefusesARetiredAPIVersion drives the command at the real
// entrypoint over one Component, drafted by the drafting command and filled, in
// two copies that differ in their `apiVersion` line alone.
//
// The copy declaring the version this compiler no longer reads is refused under
// its own short code, naming the version it declares, the version the compiler
// reads, the position of the `apiVersion` line and the line to write; and it is
// refused for that alone, not for any finding of its kind's schema. The copy
// declaring the version the compiler reads validates, so the refusal is the
// line's and nothing else's.
func TestValidateRefusesARetiredAPIVersion(t *testing.T) {
	t.Parallel()
	bin := buildServeBinary(t)
	ws := t.TempDir()

	current := fillLine(t,
		fillLine(t, draftWith(t, bin, ws, "component", "my-service"), `  version: ""`, `  version: "0.1.0"`),
		`    component.aggrega.ai/type: ""`, `    component.aggrega.ai/type: "server"`)
	retired := fillLine(t, current, "apiVersion: aggrega.ai/v1beta1\n", "apiVersion: aggrega.ai/v1alpha1\n")

	out, err := runIsolated(t, bin, ws, "validate", writeInto(t, ws, "retired.yaml", retired))
	if err == nil {
		t.Fatalf("a document declaring aggrega.ai/v1alpha1 was accepted:\n%s", out)
	}
	for _, want := range []string{
		"E0373",
		"a document declares an API version this compiler no longer reads",
		"1:1: apiVersion: the document declares aggrega.ai/v1alpha1, and this compiler reads aggrega.ai/v1beta1",
		"change the apiVersion line to `apiVersion: aggrega.ai/v1beta1`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "E0215") {
		t.Errorf("the refusal reports the generic schema condition beside its own:\n%s", out)
	}

	if out, err := runIsolated(t, bin, ws, "validate", writeInto(t, ws, "current.yaml", current)); err != nil {
		t.Errorf("the same document declaring aggrega.ai/v1beta1 is refused, so the refusal above is not the "+
			"apiVersion line's: %v\n%s", err, out)
	}
}
