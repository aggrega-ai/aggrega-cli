// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package codecconformance

import (
	"errors"
	"fmt"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
)

// RFC6902Cases returns op-apply cases covering the RFC-6902 operations a
// node-span codec surgically realises — replace of a scalar value, add of a
// new mapping member, remove of a mapping member — plus the two negative
// guardrails: an absent path and an absent token. They hold a codec's apply
// (the stock applier the codec borrows) to the same RFC-6902 bar the core's
// apply engine is held to, expressed as codec cases so a provider override is
// held to it too.
//
// The bodies are plain single-document YAML so any YAML-shaped codec — the
// stock codec, or a provider override that delegates non-template files to it —
// produces one structured unit the ops address. The corpus author appends
// these to a codec's own corpus.
func RFC6902Cases() []Case {
	const filename = "rfc6902.yaml"
	return []Case{
		{
			Name:      "replace-scalar-value",
			Filename:  filename,
			Input:     []byte("replicas: 1\nimage: nginx\n"),
			Ops:       []frp.JSONPatchOp{{Op: "replace", Path: "/replicas", Value: float64(3)}},
			WantBytes: []byte("replicas: 3\nimage: nginx\n"),
		},
		{
			Name:      "add-mapping-member",
			Filename:  filename,
			Input:     []byte("spec:\n  replicas: 1\n"),
			Ops:       []frp.JSONPatchOp{{Op: "add", Path: "/spec/paused", Value: true}},
			WantBytes: []byte("spec:\n  replicas: 1\n  paused: true\n"),
		},
		{
			Name:      "remove-mapping-member",
			Filename:  filename,
			Input:     []byte("keep: a\ndrop: b\n"),
			Ops:       []frp.JSONPatchOp{{Op: "remove", Path: "/drop"}},
			WantBytes: []byte("keep: a\n"),
		},
		{
			Name:     "absent-path-halts",
			Filename: filename,
			Input:    []byte("present: 1\n"),
			Ops:      []frp.JSONPatchOp{{Op: "replace", Path: "/missing", Value: "x"}},
			WantHalt: true,
		},
	}
}

// CheckAbsentTokenHalts asserts a codec rejects a Reassemble whose PatchedUnit
// names a token the segmentation never produced. This guardrail lives in
// Reassemble (a patched token must match a produced unit), and exercising it
// requires hand-building a PatchedUnit with a bogus token rather than a normal
// op-apply case, so it is a standalone check Run-callers invoke directly.
//
// It returns a non-nil error if the codec does NOT reject the bogus token —
// i.e. the assertion is "this must halt".
func CheckAbsentTokenHalts(codec frp.Codec) error {
	const filename = "absent-token.yaml"
	body := []byte("a: 1\n")
	units, err := codec.Segment(filename, body)
	if err != nil {
		return fmt.Errorf("segment: %w", err)
	}
	bogus := []frp.PatchedUnit{{
		Token: "no-such-token#999",
		Tree:  []byte(`{"a":2}`),
		Ops:   []frp.JSONPatchOp{{Op: "replace", Path: "/a", Value: float64(2)}},
	}}
	if _, _, err := codec.Reassemble(filename, body, units, bogus); err == nil {
		return errors.New("expected Reassemble to reject a PatchedUnit naming an absent token; got success")
	}
	return nil
}
