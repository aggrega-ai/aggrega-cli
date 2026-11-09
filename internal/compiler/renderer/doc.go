// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package renderer implements Step 5 of the Aggrega compiler pipeline:
// the Infrastructure Rendering Engine.
//
// It assembles the template context (matching template-context-v1beta1.schema.json),
// evaluates provides with self-containment guards, pre-hydrates inject pipes,
// and executes Phase A (injectMapping) and Phase B (valuesMapping), including
// Phase B.6 dispatch for boundary-scope FeatureRecipes. Per-node Provider output
// is routed to per-(evaluated-path, provider) BoundaryAssembler invocations
// keyed on the contributing boundary's evaluated directory and subDirectory;
// two boundaries that evaluate to the same (path, provider) tuple share an
// assembler and produce one merged assembled output.
package renderer
