// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package selector implements Kubernetes-style label matching for Aggrega's
// cross-node type-safety check (static analysis's `usesSchema` selector
// evaluation against edge destinations) and Feature attachment targeting
// (`appliesTo` / `appliesToAnyOf` against node labels).
// The same engine serves both: changing label-matching semantics here affects
// both code paths.
//
// Selector strings are carried in the synthesised `usesSchema` JSON Schema
// via `x-aggrega-selector:` extension keywords for traceability —
// inspection tooling can see why a constraint exists from the schema layer
// alone — while the actual label matching runs in native Go where
// Kubernetes-style selector semantics are well-defined and fast.
//
// Two API styles cover the two authoring surfaces:
//
//   - String selectors: MatchString / Parse — used by `usesSchema.selector`
//     entries, the `x-aggrega-selector:` extension keywords they synthesise
//     into, Feature `appliesTo`, and the string-form items of
//     `appliesToAnyOf`. Syntax follows Kubernetes label-selector
//     conventions: comma-separated requirements (AND-combined), with `=`,
//     `!=`, `in (...)`, `notin (...)`, bare key (Exists), and `!key`
//     (DoesNotExist) — the grammar `kubectl get -l` accepts.
//
//   - Map and disjunction matchers: MatchLabels / MatchAnyOf — MatchLabels
//     evaluates a flat key-value map (equality-only AND), the shape of an
//     `appliesToAnyOf` flat-map item; MatchAnyOf composes the OR layer over
//     `[]SelectorTerm` items (`pkg/types/feature.go`), dispatching each item
//     to MatchString or MatchLabels per its arm.
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
