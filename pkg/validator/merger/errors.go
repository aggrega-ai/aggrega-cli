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
	ShortCode: "E0219",
	Message:   "merger: cyclic extends chain",
})

// ErrMaxDepth fires when ResolveChain walks a chain that exceeds 10
// levels before terminating. Mirrors the existing validator inheritance
// walker's depth bound. Wrapped in *MergeError carrying the terminal
// URI and chain depth.
func ErrMaxDepth() error { return errMaxDepth }

var errMaxDepth = cerrors.Register("validator-merger/max-depth", cerrors.Meta{
	ShortCode: "E0220",
	Message:   "merger: extends chain exceeds maximum depth",
})

// ErrMergeInvalid fires on a structural merge failure that the per-
// keyword rules cannot resolve — for example, an empty input chain
// passed to Merge. Wrapped in *MergeError carrying a path hint and a
// human-readable detail.
func ErrMergeInvalid() error { return errMergeInvalid }

var errMergeInvalid = cerrors.Register("validator-merger/merge-invalid", cerrors.Meta{
	ShortCode: "E0221",
	Message:   "merger: invalid merge input",
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

// Error opens with the condition's registered sentence and reports the
// occurrence after it: the instance path, the merge-time detail, and the schema
// in the chain the failure came from.
//
// The sentence is read off the condition rather than written here, so the words
// a reader sees have one home. Note that these conditions register their
// sentences with a package label already in them, and that label is therefore
// printed by the sentence rather than by this composer, which writes none of
// its own. Two labels in one line is what that avoids.
func (e *MergeError) Error() string {
	values := make([]string, 0, 3)
	if e.Path != "" {
		values = append(values, "at "+e.Path)
	}
	if e.Detail != "" {
		values = append(values, e.Detail)
	}
	if e.Source != "" {
		values = append(values, "from "+e.Source)
	}
	return cerrors.ComposeMessage(e.Err, values, "")
}

func (e *MergeError) Unwrap() error { return e.Err }
