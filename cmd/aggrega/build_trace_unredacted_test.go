// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestBuildRejectsUnredactedTraceWithoutTrace pins the loud guardrail on the
// flag pair. --trace-unredacted with no trace to write is a request the build
// cannot honour, and the one outcome it must not have is silence: an operator
// who asked to see withheld values and got a build that ignored the request has
// nothing to tell them the values are still withheld.
//
// The halt is asserted on the dispatch rather than on the cobra command, because
// the dispatch is where the check lives and therefore what every caller —
// including a future one that does not read the flags — is bound by.
func TestBuildRejectsUnredactedTraceWithoutTrace(t *testing.T) {
	var diag, out bytes.Buffer
	err := dispatchBuildWithDiag(resolveTempWorkspace(t, tempWorkspaceOptions{}), "release.yaml", nil, false,
		buildDiagOptions{quiet: true},
		buildRunOptions{traceUnredacted: true}, &diag, &out)
	if err == nil {
		t.Fatal("--trace-unredacted without --trace must halt; the build proceeded")
	}
	for _, want := range []string{"--trace-unredacted", "--trace"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the halt must name %s so the operator can act on it; got %q", want, err)
		}
	}
	// The halt precedes every side effect, which is what makes it safe to run
	// against a declared workspace holding no release file at all: nothing was
	// loaded, rendered or written before it fired.
	if diag.Len() != 0 || out.Len() != 0 {
		t.Errorf("the halt must fire before any output; diag=%q out=%q", diag.String(), out.String())
	}
}

// TestBuildWithTraceDoesNotHitTheFlagPairingHalt is the paired control. Without
// it, a check that rejected --trace-unredacted unconditionally would satisfy the
// test above. The build here fails for an unrelated reason — the declared
// workspace holds no release file — and the assertion is only that the failure is
// NOT the pairing one.
func TestBuildWithTraceDoesNotHitTheFlagPairingHalt(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	var diag, out bytes.Buffer
	err := dispatchBuildWithDiag(resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), "release.yaml", nil, true,
		buildDiagOptions{quiet: true},
		buildRunOptions{traceUnredacted: true}, &diag, &out)
	if err == nil {
		t.Fatal("a build against an empty directory must fail for its own reasons; got nil")
	}
	if strings.Contains(err.Error(), "--trace-unredacted needs --trace") {
		t.Errorf("the pairing halt fired with --trace on; it must gate only the unpaired case: %v", err)
	}
}
