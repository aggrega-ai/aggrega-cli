// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package renderer implements Step 5 of the Aggrega compiler pipeline:
// the Infrastructure Rendering Engine.
//
// It assembles the template context (matching template-context-v1alpha1.schema.json),
// evaluates provides with self-containment guards, pre-hydrates inject pipes,
// executes Phase A (injectMapping) and Phase B (valuesMapping), and hands the
// values dictionary to the provider engine for Kubernetes manifest generation.
package renderer
