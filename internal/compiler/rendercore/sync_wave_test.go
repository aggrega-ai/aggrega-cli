// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package rendercore

import (
	"reflect"
	"testing"
)

func TestWavePredecessorsChainsOnlyTheImmediatelyPrecedingPopulatedWave(t *testing.T) {
	got := WavePredecessors([]int{7, -5, 0, 0, 7})
	want := [][]int{{2, 3}, nil, {1}, {1}, {2, 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WavePredecessors = %v, want %v", got, want)
	}
}

func TestWavePredecessorsOfOneWaveIsNil(t *testing.T) {
	for _, waves := range [][]int{{3, 3}, {}, nil} {
		if got := WavePredecessors(waves); got != nil {
			t.Errorf("WavePredecessors(%v) = %v, want nil", waves, got)
		}
	}
}

func TestWavePredecessorsKeepsCallerOrder(t *testing.T) {
	got := WavePredecessors([]int{1, 0, 1, 0, 0})
	want := [][]int{{1, 3, 4}, nil, {1, 3, 4}, nil, nil}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WavePredecessors = %v, want %v", got, want)
	}
	got = WavePredecessors([]int{0, 0, 0, 1})
	want = [][]int{nil, nil, nil, {0, 1, 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WavePredecessors = %v, want %v", got, want)
	}
}

func TestProvenanceMetadataKeyValue(t *testing.T) {
	if ProvenanceMetadataKey != "aggrega.ai/provenance" {
		t.Fatalf("ProvenanceMetadataKey = %q", ProvenanceMetadataKey)
	}
}
