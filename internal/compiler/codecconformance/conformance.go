// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package codecconformance

import (
	"bytes"
	"fmt"
	"testing"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
)

// determinismRuns is how many times the determinism family re-runs one case.
// A map-iteration leak surfaces probabilistically, so the count is high enough
// to make a single clean run improbable while keeping the suite fast.
const determinismRuns = 8

// Case is one corpus entry the harness exercises against a codec. A case
// carries the body to segment, how to address a unit for the op-apply family,
// and the expectation for the patched output.
//
// Tokens are products the codec assigns at runtime (the emitted filename, or
// "<filename>#<ordinal>" for a multi-document file), so a case cannot name a
// target token directly. It instead supplies TargetSel, a predicate over the
// produced units; the default selector (when TargetSel is nil) picks the first
// structured unit. Filename is a distinct field from Input because a codec's
// token shape depends on the name it segments under, so a case controls it to
// exercise both the single-document (bare name) and multi-document
// ("name#ordinal") token forms.
//
// Ops are the RFC-6902 operations applied to the selected unit's tree-view via
// the stock applier. An empty Ops makes the case round-trip and determinism
// only (no patch is applied). WantBytes, when non-nil, is the exact expected
// Reassemble output after the ops apply — asserted byte-for-byte. WantHalt
// marks a case that MUST fail: segmenting an uncarvable body, or applying ops
// to an opaque unit, an absent token, or an absent path. When WantHalt is set
// WantBytes is ignored.
type Case struct {
	Name     string
	Filename string
	Input    []byte

	TargetSel func(units []frp.Unit) (idx int, ok bool)
	Ops       []frp.JSONPatchOp

	WantBytes []byte
	WantHalt  bool
}

// Run exercises codec against every case in corpus, running the three property
// families as subtests. It is the single entry point both the core codec tests
// and a provider override's tests call.
//
// Each property family is implemented as a pure check function returning an
// error; Run adapts the error to the testing surface. This factoring lets a
// codec author assert that a deliberately nondeterministic codec FAILS a
// property (see CheckDeterminism) without needing to observe a failing
// *testing.T.
func Run(t *testing.T, codec frp.Codec, corpus []Case) {
	t.Helper()
	for _, c := range corpus {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			t.Run("determinism", func(t *testing.T) {
				if c.WantHalt {
					t.Skip("halt case: no deterministic output to compare")
				}
				if err := CheckDeterminism(codec, c); err != nil {
					t.Error(err)
				}
			})
			t.Run("round-trip+isolation", func(t *testing.T) {
				if c.WantHalt {
					t.Skip("halt case: round-trip not asserted")
				}
				if err := CheckRoundTripIsolation(codec, c); err != nil {
					t.Error(err)
				}
			})
			t.Run("op-apply", func(t *testing.T) {
				if err := CheckOpApply(codec, c); err != nil {
					t.Error(err)
				}
			})
		})
	}
}

// CheckDeterminism runs the full Segment → apply → Reassemble sequence
// determinismRuns times and reports a non-nil error unless every run produced
// byte-identical output. It is the map-order-leak catcher: a codec whose
// Reassemble serializes a Go map (randomized iteration order) fails here.
//
// A case with no Ops runs Segment → Reassemble(no patches) repeatedly; a case
// with Ops applies them to the selected unit each run.
func CheckDeterminism(codec frp.Codec, c Case) error {
	var first []byte
	for i := 0; i < determinismRuns; i++ {
		out, err := segmentApplyReassemble(codec, c)
		if err != nil {
			return fmt.Errorf("run %d: %w", i, err)
		}
		if i == 0 {
			first = out
			continue
		}
		if !bytes.Equal(out, first) {
			return fmt.Errorf("non-deterministic output: run %d differs from run 0\n run 0: %q\n run %d: %q", i, first, i, out)
		}
	}
	return nil
}

// CheckRoundTripIsolation asserts the two paired codec invariants: reassembling
// an unpatched segmentation reproduces the body byte-for-byte, and patching one
// unit leaves every OTHER unit's original byte-range present verbatim in the
// output.
func CheckRoundTripIsolation(codec frp.Codec, c Case) error {
	units, err := codec.Segment(c.Filename, c.Input)
	if err != nil {
		return fmt.Errorf("segment: %w", err)
	}

	// Round-trip: no patches → byte-identical body.
	rt, _, err := codec.Reassemble(c.Filename, c.Input, units, nil)
	if err != nil {
		return fmt.Errorf("reassemble (no patches): %w", err)
	}
	if !bytes.Equal(rt, c.Input) {
		return fmt.Errorf("round-trip not byte-identical\n orig: %q\n  got: %q", c.Input, rt)
	}

	// Patch-isolation: only meaningful when a case carries ops and the corpus
	// holds more than one unit. After patching the selected unit, every other
	// unit's original bytes must survive verbatim.
	if len(c.Ops) == 0 || len(units) < 2 {
		return nil
	}
	idx, ok := selectTarget(units, c.TargetSel)
	if !ok {
		return nil // no structured target; op-apply family reports the mismatch
	}
	patched, err := applyOps(codec, c, units, idx)
	if err != nil {
		return fmt.Errorf("apply ops: %w", err)
	}
	out, _, err := codec.Reassemble(c.Filename, c.Input, units, patched)
	if err != nil {
		return fmt.Errorf("reassemble (patched): %w", err)
	}
	for j, u := range units {
		if j == idx {
			continue
		}
		orig := unitOriginalBytes(c.Input, u)
		if len(orig) == 0 {
			continue
		}
		if !bytes.Contains(out, orig) {
			return fmt.Errorf("patch-isolation: unit %d bytes %q not preserved verbatim after patching unit %d\n output: %q", j, orig, idx, out)
		}
	}
	return nil
}

// CheckOpApply asserts the op-apply contract for a case. For a WantHalt case it
// requires the segment-apply-reassemble path to error (an opaque target, an
// absent token, or an absent path). For a case with WantBytes it requires the
// patched Reassemble output to equal WantBytes byte-for-byte.
func CheckOpApply(codec frp.Codec, c Case) error {
	out, err := segmentApplyReassemble(codec, c)
	if c.WantHalt {
		if err == nil {
			return fmt.Errorf("expected a halt; got success with output %q", out)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("unexpected halt: %w", err)
	}
	if c.WantBytes != nil && !bytes.Equal(out, c.WantBytes) {
		return fmt.Errorf("patched output mismatch\n want: %q\n  got: %q", c.WantBytes, out)
	}
	return nil
}

// segmentApplyReassemble runs the full pipeline a guest step drives for one
// case: Segment, select the target unit, apply the ops to its tree via the
// stock applier, then Reassemble. A case with no Ops short-circuits to
// Reassemble with no patches (the round-trip path). It returns the first error
// — Segment failure, an absent/opaque target, an apply failure, or a Reassemble
// failure — so a WantHalt case can assert on it.
func segmentApplyReassemble(codec frp.Codec, c Case) ([]byte, error) {
	units, err := codec.Segment(c.Filename, c.Input)
	if err != nil {
		return nil, fmt.Errorf("segment: %w", err)
	}
	if len(c.Ops) == 0 {
		out, _, err := codec.Reassemble(c.Filename, c.Input, units, nil)
		return out, err
	}
	idx, ok := selectTarget(units, c.TargetSel)
	if !ok {
		return nil, fmt.Errorf("no target unit selected (corpus expected a structured unit)")
	}
	patched, err := applyOps(codec, c, units, idx)
	if err != nil {
		return nil, err
	}
	out, _, err := codec.Reassemble(c.Filename, c.Input, units, patched)
	return out, err
}

// applyOps applies a case's ops to the unit at idx via the stock applier and
// returns the one-element PatchedUnit slice Reassemble consumes. Applying ops to
// an opaque unit (nil tree) errors in the applier — the codec-side half of the
// not-structured guardrail (the walk-level halt is asserted in the renderer).
func applyOps(codec frp.Codec, c Case, units []frp.Unit, idx int) ([]frp.PatchedUnit, error) {
	u := units[idx]
	newTree, err := frp.StockApplier{}.Apply(u.Tree, c.Ops)
	if err != nil {
		return nil, fmt.Errorf("stock apply on unit %q: %w", u.Token, err)
	}
	return []frp.PatchedUnit{{Token: u.Token, Tree: newTree, Ops: c.Ops}}, nil
}

// selectTarget picks the unit a case's ops address. A nil selector defaults to
// the first structured unit; ok is false when no unit matches.
func selectTarget(units []frp.Unit, sel func(units []frp.Unit) (int, bool)) (int, bool) {
	if sel != nil {
		return sel(units)
	}
	for i, u := range units {
		if u.Structured() {
			return i, true
		}
	}
	return 0, false
}

// unitOriginalBytes returns the unit's reassembly-source bytes. A codec sets
// Unit.Bytes to the original byte-range, which is what patch-isolation checks
// survives verbatim when a sibling is patched.
func unitOriginalBytes(_ []byte, u frp.Unit) []byte {
	return u.Bytes
}
