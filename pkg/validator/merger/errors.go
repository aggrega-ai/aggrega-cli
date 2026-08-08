// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package merger

import cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"

// ErrCyclicExtension fires when ResolveChain detects a cycle in the
// extends chain — the visited-set sees the same URI twice. Wrapped in
// *MergeError carrying the offending URI and the partial chain that
// led to the cycle.
func ErrCyclicExtension() error { return errCyclicExtension }

var errCyclicExtension = cerrors.Register("validator-merger/cyclic-extension", cerrors.Meta{
	Message: "merger: cyclic extends chain",
})

// ErrMaxDepth fires when ResolveChain walks a chain that exceeds 10
// levels before terminating. Mirrors the existing validator inheritance
// walker's depth bound. Wrapped in *MergeError carrying the terminal
// URI and chain depth.
func ErrMaxDepth() error { return errMaxDepth }

var errMaxDepth = cerrors.Register("validator-merger/max-depth", cerrors.Meta{
	Message: "merger: extends chain exceeds maximum depth",
})

// ErrMergeInvalid fires on a structural merge failure that the per-
// keyword rules cannot resolve — for example, an empty input chain
// passed to Merge. Wrapped in *MergeError carrying a path hint and a
// human-readable detail.
func ErrMergeInvalid() error { return errMergeInvalid }

var errMergeInvalid = cerrors.Register("validator-merger/merge-invalid", cerrors.Meta{
	Message: "merger: invalid merge input",
})

// MergeError wraps a sentinel with merge-time context. Path names the
// instance-relative location of the failure (empty at root-level
// failures); Source names the file:line of the schema in the chain
// that contributed to the failure (empty when the cause is structural
// rather than schema-specific); Detail is a human-readable message.
type MergeError struct {
	Path   string
	Source string
	Detail string
	Err    error
}

func (e *MergeError) Error() string {
	out := "merger: "
	if e.Path != "" {
		out += "at " + e.Path + ": "
	}
	out += e.Detail
	if e.Source != "" {
		out += " (from " + e.Source + ")"
	}
	return out
}

func (e *MergeError) Unwrap() error { return e.Err }
