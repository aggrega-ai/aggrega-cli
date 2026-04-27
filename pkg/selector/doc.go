// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package selector implements Kubernetes-style label matching for Aggrega's
// cross-node type-safety check (Step 4 static analyzer's `usesSchema` selector
// evaluation against edge destinations) and Feature attachment targeting
// (`appliesTo` / `appliesToExpressions` / `appliesToAnyOf` against node labels).
// The same engine serves both: changing label-matching semantics here affects
// both code paths.
//
// Selector strings are carried in the synthesised `usesSchema` CUE as
// `@selector()` attribute tags for traceability — inspection tooling can
// see why a constraint exists from the CUE layer alone — while the actual
// label matching runs in native Go where Kubernetes-style selector
// semantics are well-defined and fast.
//
// Two API styles cover the two authoring surfaces:
//
//   - String selectors: MatchString / Parse — used by `usesSchema.selector`
//     entries and the CUE `@selector()` attribute tags they synthesise into.
//     Syntax follows Kubernetes label-selector conventions: comma-separated
//     requirements (AND-combined), with `=`, `!=`, `in (...)`, `notin (...)`,
//     bare key (Exists), and `!key` (DoesNotExist).
//
//   - Structured selectors: MatchLabels / MatchExpressions / MatchAnyOf —
//     used by Feature `appliesTo*` fields, which are typed as
//     `OrderedMap[string]`, `[]LabelSelectorRequirement`, and
//     `[]SelectorTerm` in `pkg/types/feature.go`. The structured form mirrors
//     the Kubernetes API LabelSelector shape so Feature catalog YAML reads
//     identically to the K8s convention K8s-experienced authors already know.
//
// The engine's operators are a closed set of six (Operator: Equals, NotEquals,
// In, NotIn, Exists, DoesNotExist) matching K8s label-selector semantics
// exactly. The package does not support regex matching, substring matching, or
// numeric comparisons — kept intentionally narrow to match the
// well-defined K8s label-selector semantics.
//
// Empty / nil selectors match everything across every entry point. This is the
// "no constraint declared" case the analyzer relies on to skip evaluation
// cleanly: a `usesSchema` entry without a selector string passes; a Feature
// without `appliesTo*` fields applies to all nodes.
package selector
