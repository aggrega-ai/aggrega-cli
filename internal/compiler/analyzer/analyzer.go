// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"context"
	"fmt"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// AnalysisResult is the bundled output of [Analyze]. The three fields
// carry the artifacts produced by Step 4's two label-matching classes
// and its precalculated_paths / policy-input assembly:
//
//   - Errors — every selector and appliesTo violation discovered, plus
//     any failed policy-socket verdict appended by the pipeline, one
//     [AnalysisError] per violation. Build flow gates on
//     len(Errors) > 0; see the package doc Pipeline Integration
//     subsection for the gating semantics.
//   - Paths — the cycle-safe DFS enumeration of acyclic paths through
//     the IR graph; see [calculatePaths] for the contract.
//   - PolicyInput — the policy socket payload (always
//     non-nil); see [PolicyInput] for the ABI shape. The
//     pipeline marshals it to a configured policy plugin at Step 4.
type AnalysisResult struct {
	Errors      []AnalysisError
	Warnings    []string
	Paths       []PrecalculatedPath
	PolicyInput *PolicyInput
}

// AnalysisErrorKind classifies an [AnalysisError] so [AnalysisError.Error]
// composes the right message and consumers can branch beyond the
// Socket-presence heuristic. The zero value is the edge-shaped class
// (selector or appliesTo, distinguished by Socket presence) and preserves
// the original two-class behavior; [AnalysisErrorPolicy] marks a
// policy-socket verdict.
type AnalysisErrorKind string

const (
	// AnalysisErrorEdge is the zero-value, edge-shaped class: a selector or
	// appliesTo violation whose message [AnalysisError.Error] composes from
	// the Source/Target/Socket fields.
	AnalysisErrorEdge AnalysisErrorKind = ""

	// AnalysisErrorPolicy marks a Step-4 policy verdict whose
	// Message is already a complete, plugin-supplied diagnostic.
	// [AnalysisError.Error] returns that Message verbatim rather than
	// composing it from the edge fields, which a policy verdict does not
	// carry.
	AnalysisErrorPolicy AnalysisErrorKind = "policy"
)

// AnalysisError is one violation discovered during Step 4. The struct
// is shaped for edge-style violations (the selector check) and reused
// for per-Feature-attachment violations (the appliesTo check) and for
// policy-socket verdicts; the fields carry slightly different semantics
// across the classes:
//
//   - Selector violations (from validateSelectors): Source is the
//     source nodeID of the violating edge, Target is the target
//     nodeID, Socket is the socket name, Selector is the selector
//     string that failed to match (or whose parse failed), Message
//     is a human-readable diagnostic.
//   - AppliesTo violations (from validateAppliesTo): Source is the
//     target nodeID of the Feature attachment, Target is the Feature
//     instance name, Socket is empty, Selector is [formatAppliesTo]'s
//     summary of the constraint set, Message is a human-readable
//     diagnostic.
//   - Policy verdicts (Kind [AnalysisErrorPolicy]): Message is the
//     complete diagnostic the policy plugin supplied (a rule
//     failure), and the edge fields (Source/Target/Socket/Selector) are
//     empty.
//
// Callers that branch on the edge classes distinguish them by inspecting
// Socket: a non-empty Socket signals a selector violation; an empty
// Socket signals an appliesTo violation. A policy verdict carries the
// explicit Kind [AnalysisErrorPolicy].
type AnalysisError struct {
	// Kind classifies the violation. The zero value ([AnalysisErrorEdge])
	// is an edge-shaped selector/appliesTo violation; [AnalysisErrorPolicy]
	// marks a policy-socket verdict whose Message is already complete.
	Kind     AnalysisErrorKind
	Source   string
	Target   string
	Socket   string
	Selector string
	Message  string
	// OriginFile records the absolute filesystem path of the source file
	// where the violating authoring lives. Populated by the validate
	// passes from the IR graph's per-node SourceLocation when available;
	// stays empty when the violation has no recoverable single-file
	// origin (e.g., violations involving catalog entities loaded from
	// multiple per-version files). Consumers that filter per-file
	// diagnostics (the daemon's Tier-4 validation) drop entries whose
	// OriginFile does not match the requested file; consumers that
	// surface all diagnostics ignore the field.
	OriginFile string
	// Field is a mapping-style dotted locator naming the authoring site
	// of the violation within OriginFile, e.g.
	// ".spec.components.<name>.uses.<socket>" for a selector violation or
	// ".spec.components.<name>.features.<instance>" for an appliesTo
	// violation. It mirrors the resolver's locator shape so a consumer
	// can walk the parsed source document to a sub-file position. Empty
	// when the violation has no recoverable single-key locator; consumers
	// fall back to file-level positioning. The locator addresses a
	// top-level component, so subsystem-nested components (authored under
	// .spec.subsystems.*) yield an empty Field rather than a path that
	// names the wrong authoring site.
	Field string
	// Sentinel is the domain sentinel this violation wraps. ToCompilerError
	// surfaces it as the CompilerError's inner error so callers can detect
	// the violation class with errors.Is at the pipeline boundary. The
	// selector and appliesTo emission sites set it to ErrSelectorViolation.
	Sentinel error
}

// lastSegment returns the authored local component key from a flattened
// node ID. Root-tier node IDs are the authored key already; sub-aggregate
// node IDs are prefix-qualified (prefix + "." + name), so the authored key
// is the segment after the final ".".
func lastSegment(nodeID string) string {
	if i := strings.LastIndex(nodeID, "."); i >= 0 {
		return nodeID[i+1:]
	}
	return nodeID
}

// selectorFieldPath builds the mapping-style YAML locator for a selector
// violation — the uses-socket key on the source component. Returns ""
// for a subsystem-nested component (non-empty subsystemPath): its
// authored site is .spec.subsystems.*, not .spec.components.*, so a
// top-level locator would name the wrong path; an empty Field routes the
// consumer to file-level positioning instead.
func selectorFieldPath(nodeID, socket, subsystemPath string) string {
	if subsystemPath != "" {
		return ""
	}
	return fmt.Sprintf(".spec.components.%s.uses.%s", lastSegment(nodeID), socket)
}

// appliesToFieldPath builds the mapping-style YAML locator for an appliesTo
// violation — the Feature-instance key under the attaching component.
// Returns "" for a subsystem-nested component for the same reason as
// selectorFieldPath.
func appliesToFieldPath(nodeID, instance, subsystemPath string) string {
	if subsystemPath != "" {
		return ""
	}
	return fmt.Sprintf(".spec.components.%s.features.%s", lastSegment(nodeID), instance)
}

// Error formats the violation as a single human-readable string. A policy
// verdict (Kind [AnalysisErrorPolicy]) returns its Message verbatim — the
// plugin already supplied a complete diagnostic. Selector violations (Socket
// non-empty) read as "<source> -> <target> (<socket>): <message>"; appliesTo
// violations (Socket empty) read as
// "<source> attaches Feature <target>: <message>" since the appliesTo check's
// Source-and-Target reuse semantics carry different meaning. The edge shapes
// are distinguished by Socket presence per the package's documented convention.
func (e AnalysisError) Error() string {
	if e.Kind == AnalysisErrorPolicy {
		return e.Message
	}
	if e.Socket != "" {
		return fmt.Sprintf("%s -> %s (%s): %s", e.Source, e.Target, e.Socket, e.Message)
	}
	return fmt.Sprintf("%s attaches Feature %s: %s", e.Source, e.Target, e.Message)
}

// ToCompilerError converts the analyzer violation into a *cerrors.CompilerError,
// choosing the category from the violation's Kind. Selector and appliesTo
// violations (the edge-shaped kinds) are authoring-time problems on the
// developer's Aggregate — the developer authored the uses-edge or Feature
// attachment that fails the selector or appliesTo constraint — so they route
// to cerrors.DeveloperError. A policy verdict (Kind [AnalysisErrorPolicy]) is a
// governance rejection from an operator-attached policy plugin, not a
// developer authoring mistake, so it routes to cerrors.PolicyError; the
// remediation is to satisfy the enforced policy or reconsider its attachment,
// not to edit the Aggregate.
//
// Source location maps the violating-node ID into SourceLocation.File. The
// node ID is the most-specific authoring-locator available at the analyzer
// stage — finer-grained source positions (file/line/column) require the
// resolver to thread file-path metadata onto IRNode, which is a separate
// future improvement. A policy verdict carries no edge fields, so its Source
// is empty and the pointer is message-only.
//
// The CompilerError wraps e.Sentinel as its inner error, so a violation
// class is detectable with errors.Is at the pipeline boundary (the build
// error joins these CompilerErrors and errors.Is traverses the chain).
// Selector and appliesTo violations carry ErrSelectorViolation; policy
// verdicts carry ErrPolicyViolation. When Sentinel is nil the inner chain is
// empty, matching the prior behavior.
func (e *AnalysisError) ToCompilerError() *cerrors.CompilerError {
	category := cerrors.DeveloperError
	if e.Kind == AnalysisErrorPolicy {
		category = cerrors.PolicyError
	}
	return cerrors.New(
		category,
		cerrors.SourceLocation{File: e.Source},
		e.Error(),
		nil,
		e.Sentinel,
	)
}

// sourceFileFor returns the SourceFile recorded on the IR node named
// nodeID. Returns "" when the graph carries no node by that name (a
// defensive case that shouldn't surface in production — every analyzer
// emission site is gated upstream by a successful Nodes.Get for the
// same nodeID) or when the node was authored without recoverable
// provenance (synthetic test IR). Empty results route through the
// daemon's backfill, preserving today's "surface to validated file"
// semantics for unknown-provenance entries.
func sourceFileFor(graph *types.IRGraph, nodeID string) string {
	if graph == nil {
		return ""
	}
	node, ok := graph.Spec.Nodes.Get(nodeID)
	if !ok {
		return ""
	}
	return node.SourceFile
}

// PrecalculatedPath is one acyclic path through the IR graph,
// enumerated by [calculatePaths]'s cycle-safe DFS:
//
//   - PathID — gap-free monotonic identifier in DFS-emission order
//     (path_01, path_02, …) across the entire output, not numbered
//     per start node.
//   - Nodes — the node-ID sequence from the start node to a terminal
//     or cycle-blocked node, length always > 1 (single-node and
//     dead-end starts are not emitted).
//   - AttachedFeatures — the alphabetically sorted union of feature
//     types attached to any node along the path. Set-membership
//     semantics: a feature appears once even if multiple nodes
//     carry it. Empty when no node along the path carries any
//     Feature attachment.
type PrecalculatedPath struct {
	PathID           string   `json:"path_id"`
	Nodes            []string `json:"nodes"`
	AttachedFeatures []string `json:"attached_features"`
}

// Analyze executes Step 4 of the compiler pipeline. It returns a
// non-nil [AnalysisResult] populated with:
//
//  1. Selector-violation errors from validateSelectors.
//  2. AppliesTo-violation errors from validateAppliesTo (appended
//     to the same Errors slice).
//  3. Precalculated paths from calculatePaths.
//  4. The PolicyInput payload from assemblePolicyInput.
//
// Build flow gates on len(result.Errors) > 0 before invoking Step 5.
// AnalysisResult.Paths and AnalysisResult.PolicyInput are computed
// unconditionally; see the package doc Pipeline Integration
// subsection for their downstream consumption status.
func Analyze(graph *types.IRGraph, catalogLookup resolver.CatalogLookup) *AnalysisResult {
	// Background context never cancels, so the discarded error is always
	// nil; callers that participate in cancellation use AnalyzeContext.
	result, _ := AnalyzeContext(context.Background(), graph, catalogLookup)
	return result
}

// AnalyzeContext is Analyze with cooperative cancellation: validateSelectors
// and validateAppliesTo check ctx.Err() per iteration so a long Step-4 pass
// over a large graph can be interrupted (e.g. by the daemon's shutdown
// drain). It returns a non-nil error only when ctx is cancelled mid-pass;
// analyzer violations remain in the result's Errors slice as structured
// data, never as the function-level error. Analyze is the background-context
// convenience wrapper.
func AnalyzeContext(ctx context.Context, graph *types.IRGraph, catalogLookup resolver.CatalogLookup) (*AnalysisResult, error) {
	// 1. Selector validation (usesSchema selector rules)
	errs, err := validateSelectors(ctx, graph, catalogLookup)
	if err != nil {
		return nil, err
	}

	// 2. Feature appliesTo validation
	appliesErrs, err := validateAppliesTo(ctx, graph, catalogLookup)
	if err != nil {
		return nil, err
	}
	errs = append(errs, appliesErrs...)

	// 2b. secretsSchema slot selector validation (secret-kind constraint + fill satisfaction)
	secretErrs, err := validateSecretSlotSelectors(ctx, graph, catalogLookup)
	if err != nil {
		return nil, err
	}
	errs = append(errs, secretErrs...)

	// 3. DFS path calculation
	paths := calculatePaths(graph)

	// 4. Assemble policy input
	policyInput := assemblePolicyInput(graph, paths)

	// 5. Advisory lints (non-fatal warnings).
	warnings := lintExplicitOverridesAnnotation(graph)

	return &AnalysisResult{
		Errors:      errs,
		Warnings:    warnings,
		Paths:       paths,
		PolicyInput: policyInput,
	}, nil
}

// assemblePolicyInput assembles the [PolicyInput] ABI payload from the
// IR graph. The policy socket sees the full provider-agnostic IR graph
// (single-sourced from ir-v1alpha1.schema.json) rather than a narrow
// projection, so the graph is forwarded whole — the plugin reasons over the same
// node, edge, and attachment detail the compiler holds. The graph carries its
// own author-order determinism through the OrderedMap types it is built from;
// no per-field narrowing or re-keying happens here. PrecalculatedPaths carries
// the analyzer's enumerated routing paths alongside.
func assemblePolicyInput(graph *types.IRGraph, paths []PrecalculatedPath) *PolicyInput {
	return &PolicyInput{
		Graph:              *graph,
		PrecalculatedPaths: paths,
	}
}
