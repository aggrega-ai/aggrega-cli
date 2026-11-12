// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfncore

import (
	"fmt"
	"strings"
	"testing"
)

// These tests prove the fragment reader REFUSES a self-referential anchor
// rather than following it. The refusal is what keeps them survivable:
// nodeToValue follows alias links without tracking where it has been, so if the
// guard is removed the failure here is a stack overflow that takes the whole
// test binary down rather than a reported failure. That is the intended shape —
// a loud death is a correct signal — so do not "repair" a crash here by
// deleting the fixture. The rule itself is proven safely, without recursion, in
// pkg/anchorcycle.

func TestNormalizeFragmentRefusesRecursiveAnchor(t *testing.T) {
	t.Parallel()
	// A rendered fragment is fresh text: no earlier stage parsed it as YAML, so
	// this is the first and only place its anchors can be judged.
	_, err := NormalizeFragment([]byte("Resources: &loop\n  Nested: *loop\n"))
	if err == nil {
		t.Fatal("NormalizeFragment accepted a fragment whose anchor is aliased inside itself")
	}
	if !strings.Contains(err.Error(), "&loop") {
		t.Errorf("diagnostic does not name the anchor an author has to break; got: %v", err)
	}
}

func TestAssembleStackRefusesRecursiveAnchor(t *testing.T) {
	t.Parallel()
	_, err := AssembleStack([]NodeFragment{{
		NodeID: "n",
		Files:  [][]byte{[]byte("Resources: &loop\n  Nested: *loop\n")},
	}})
	if err == nil {
		t.Fatal("AssembleStack accepted a fragment whose anchor is aliased inside itself")
	}
	if !strings.Contains(err.Error(), "&loop") {
		t.Errorf("diagnostic does not name the anchor; got: %v", err)
	}
}

// TestNormalizeFragmentAcceptsDeepAndAliasReusingFragments is the other half:
// the refusal must be of recursion, never of depth or of ordinary alias reuse,
// which is a normal and useful CloudFormation authoring pattern.
func TestNormalizeFragmentAcceptsDeepAndAliasReusingFragments(t *testing.T) {
	t.Parallel()

	t.Run("one anchor reused from several places", func(t *testing.T) {
		t.Parallel()
		out, err := NormalizeFragment([]byte(
			"Base: &b\n  Type: AWS::S3::Bucket\nResources:\n  A: *b\n  B: *b\n  C: *b\n"))
		if err != nil {
			t.Fatalf("a fragment reusing one anchor was refused: %v", err)
		}
		// The alias must still have been followed — refusing recursion must not
		// have turned into refusing to resolve aliases at all.
		if !strings.Contains(string(out), `"A":{"Type":"AWS::S3::Bucket"}`) {
			t.Errorf("alias was not resolved to its target; got: %s", out)
		}
	})

	t.Run("nested well beyond any hand-written template", func(t *testing.T) {
		t.Parallel()
		var b strings.Builder
		b.WriteString("Resources:\n")
		const depth = 200
		for i := range depth {
			b.WriteString(strings.Repeat("  ", i+1))
			b.WriteString(fmt.Sprintf("k%d:\n", i))
		}
		b.WriteString(strings.Repeat("  ", depth+1))
		b.WriteString("leaf: v\n")

		if _, err := NormalizeFragment([]byte(b.String())); err != nil {
			t.Fatalf("a %d-deep non-recursive fragment was refused: %v", depth, err)
		}
	})
}
