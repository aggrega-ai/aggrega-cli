// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package codecconformance_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/codecconformance"
	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
)

// mapIteratingCodec is a deliberately nondeterministic codec: its Reassemble
// serializes a Go map, whose iteration order Go randomizes per run, so the same
// input produces byte-varying output. The harness's determinism family MUST
// catch it — that is the proof the guard bites rather than rubber-stamps.
//
// Segment carves the body into one structured unit whose tree is an object with
// enough keys that map-iteration order varies; Reassemble re-serializes that
// map directly (skipping the order-preserving model the real codecs use).
type mapIteratingCodec struct{}

func (mapIteratingCodec) Segment(name string, body []byte) ([]frp.Unit, error) {
	return []frp.Unit{{
		Token:     name,
		Bytes:     append([]byte(nil), body...),
		MediaType: "application/yaml",
		Tree:      json.RawMessage(`{"a":1,"b":2,"c":3,"d":4,"e":5,"f":6}`),
	}}, nil
}

func (mapIteratingCodec) Reassemble(name string, body []byte, segs []frp.Unit, patched []frp.PatchedUnit) ([]byte, []string, error) {
	// Decode into a plain map and re-encode by ranging it — Go map iteration
	// order is randomized, so two calls produce different byte sequences.
	m := map[string]int{}
	if err := json.Unmarshal([]byte(`{"a":1,"b":2,"c":3,"d":4,"e":5,"f":6}`), &m); err != nil {
		return nil, nil, err
	}
	var sb strings.Builder
	for k, v := range m {
		fmt.Fprintf(&sb, "%s: %d\n", k, v)
	}
	return []byte(sb.String()), nil, nil
}

// TestRun_CatchesNondeterministicCodec proves the determinism family fails a
// map-iterating codec. It asserts on CheckDeterminism's pure error directly, so
// the proof does not require observing a failing *testing.T.
func TestRun_CatchesNondeterministicCodec(t *testing.T) {
	t.Parallel()
	c := codecconformance.Case{
		Name:     "map-iterating",
		Filename: "m.yaml",
		Input:    []byte("a: 1\n"),
	}
	if err := codecconformance.CheckDeterminism(mapIteratingCodec{}, c); err == nil {
		t.Fatalf("expected CheckDeterminism to FAIL the map-iterating codec; got nil error")
	}
}

// identityCodec is a trivially-correct codec: one structured unit, byte-faithful
// reassembly. It confirms CheckDeterminism PASSES a deterministic codec, so the
// catch above is not a false positive that fails everything.
type identityCodec struct{}

func (identityCodec) Segment(name string, body []byte) ([]frp.Unit, error) {
	return []frp.Unit{{Token: name, Bytes: append([]byte(nil), body...), MediaType: "application/yaml", Tree: json.RawMessage(`{}`)}}, nil
}

func (identityCodec) Reassemble(name string, body []byte, segs []frp.Unit, patched []frp.PatchedUnit) ([]byte, []string, error) {
	return append([]byte(nil), body...), nil, nil
}

// TestCheckDeterminism_PassesDeterministicCodec is the false-positive guard.
func TestCheckDeterminism_PassesDeterministicCodec(t *testing.T) {
	t.Parallel()
	c := codecconformance.Case{Name: "identity", Filename: "i.yaml", Input: []byte("a: 1\n")}
	if err := codecconformance.CheckDeterminism(identityCodec{}, c); err != nil {
		t.Fatalf("identity codec should pass determinism; got %v", err)
	}
}
