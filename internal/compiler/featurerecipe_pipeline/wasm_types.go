// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package featurerecipe_pipeline

import "github.com/aggrega-ai/aggrega-cli/pkg/types"

// The document-root scope names where a patch or an emit lands: own (the node's
// own rendered documents) or host (the documents of the component a
// component-scope FeatureRecipe is attached to). It is the root half of an
// operation's address; the key half is the emitted-filename key within that root.
//
// The two values are single-sourced from types.Scope* (untyped-const conversion),
// so the pipeline's wire scope vocabulary cannot drift from the Plugin entity's
// scope vocabulary — they are equal by construction. The schema-level enums
// (plugin-v1beta1 and wasm-frp-step-v1beta1) remain intentionally separate
// surfaces; unifying those is a wire change and is out of scope for the Go-side
// single-sourcing. The document-root keys the wire carries ("own"/"host") reach
// the payload as JSON struct tags the compiler cannot const-fold against these
// values; a parity test holds them equivalent instead.
//
// They are untyped so they assign to the plain string the wire shapes carry. A
// distinct named type here would be a spelling the generated codec cannot place,
// and it would carry the value opaquely rather than as a plain string — which is
// what the plugin side reads.
const (
	// DocumentScopeOwn targets the node's own rendered documents.
	DocumentScopeOwn = types.ScopeOwn

	// DocumentScopeHost targets the documents of the component a
	// component-scope FeatureRecipe is attached to. A host-scoped
	// operation from a boundary-scope step has no host documents to act
	// on; the runtime halts such an operation.
	DocumentScopeHost = types.ScopeHost
)
