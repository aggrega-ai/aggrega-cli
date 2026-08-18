// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// AnalysisResult is the bundled output of [Analyze]. Its four fields
// carry the artifacts produced by this stage's attachment-requirement checks,
// its advisory lints, and its
// precalculated_paths / policy-input assembly:
//
//   - Errors — every attachment-requirement violation discovered here, plus
//     every violation a rule declared over the finalised graph raised, and
//     any failed policy-socket verdict — the last two appended by whatever
//     holds the presentation those rules read — one [AnalysisError] per
//     violation.
//     Build flow gates on len(Errors) > 0; see the package doc Pipeline
//     Integration subsection for the gating semantics.
//   - Warnings — one advisory string per finding from the lints, which
//     never reach the Errors gate: a warning does not make the emitted
//     output wrong, so a result carrying warnings and no errors
//     proceeds.
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
// composes the right message and consumers can branch on the class rather than
// on which fields happen to be filled. The zero value is the edge-shaped class,
// whose message is composed from the Source/Target/Socket fields;
// [AnalysisErrorPolicy] marks a policy-socket verdict.
type AnalysisErrorKind string

const (
	// AnalysisErrorEdge is the zero-value, edge-shaped class, whose message
	// [AnalysisError.Error] composes from the Source/Target/Socket fields.
	//
	// No reading populates it. Every violation this package produces names its
	// class explicitly, and every rule declared over a later stage reports
	// through [ErrorsFromObjections] as [AnalysisErrorLocated]; a value in this
	// class is therefore one a caller constructed itself. It remains the zero
	// value of the kind, so a caller that fills the fields below and names no
	// kind still gets the composed shape.
	AnalysisErrorEdge AnalysisErrorKind = ""

	// AnalysisErrorPolicy marks a Step-4 policy verdict whose
	// Message is already a complete, plugin-supplied diagnostic.
	// [AnalysisError.Error] returns that Message verbatim rather than
	// composing it from the edge fields, which a policy verdict does not
	// carry.
	AnalysisErrorPolicy AnalysisErrorKind = "policy"

	// AnalysisErrorAttachmentRequirement marks an attachment requirement the
	// node does not satisfy: a declared required attachment type that no
	// attachment satisfies, a mandate whose declaration is itself malformed,
	// or a declared slot marked required that no attachment binds. The kind
	// classifies the message SHAPE — the violation is per-node rather than
	// per-edge, so [AnalysisError.Error] prefixes the node and returns the
	// rest of the diagnostic verbatim instead of composing it from the edge
	// fields. Which rule failed is carried by the Sentinel, not by the kind.
	AnalysisErrorAttachmentRequirement AnalysisErrorKind = "attachment-requirement"

	// AnalysisErrorLocated marks a violation whose Message is already a
	// complete diagnostic and whose authoring site is carried on Field and
	// OriginFile rather than composed from the edge fields.
	//
	// It is the shape a rule reporting through [ErrorsFromObjections] takes.
	// Such a rule names its subject by a locator, which positions the finding
	// on the offending authoring key; the edge fields would have to be
	// re-derived to be filled, and a value re-derived here could disagree with
	// the one the rule read. So they stay empty and [AnalysisError.Error]
	// returns the Message verbatim.
	AnalysisErrorLocated AnalysisErrorKind = "located"
)

// AnalysisError is one violation discovered during Step 4. The struct
// is shaped for edge-style violations and reused for per-node violations,
// for policy-socket verdicts, and for violations a declared rule
// raised; the fields carry slightly different semantics across the classes:
//
//   - Attachment-requirement violations (Kind
//     [AnalysisErrorAttachmentRequirement]): Source is the nodeID the
//     requirement was evaluated against, Target is the mandate's label or
//     the slot's name, Field and OriginFile position the finding, and
//     Socket and Selector are empty.
//   - Policy verdicts (Kind [AnalysisErrorPolicy]): Message is the
//     complete diagnostic the policy plugin supplied (a rule
//     failure), and the edge fields (Source/Target/Socket/Selector) are
//     empty.
//   - Violations a declared rule raised (Kind [AnalysisErrorLocated]):
//     Message is the complete diagnostic, Field names the authoring key
//     the finding is about, OriginFile names the document holding it, and
//     the edge fields are empty. It is the shape every label-matching rule
//     reports in — a socket's declared label constraint, the applicability a
//     Feature declares over what it may attach to, and the constraint an
//     attachment slot declares over what may fill it.
//
// Kind is what distinguishes the classes, and it is the only thing that
// does. Socket presence is not a classifier: a violation about one
// connection can arrive with the edge fields empty and its coordinates on
// Field instead, so a caller reading Socket to decide what it is holding
// would misread exactly that class. Socket and Selector are populated by no
// reading at all; they stay on the struct for the composed shape
// [AnalysisErrorEdge] describes.
type AnalysisError struct {
	// Kind classifies the violation. The zero value ([AnalysisErrorEdge])
	// is an edge-shaped violation composed from the fields below;
	// [AnalysisErrorPolicy] marks a policy-socket verdict whose Message is
	// already complete; [AnalysisErrorLocated] marks one a declared rule
	// raised.
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
	// ".spec.components.<name>.features.<attachment>" for a violation about
	// one attachment. It mirrors the resolver's locator shape so a consumer
	// can walk the parsed source document to a sub-file position. Empty
	// when the violation has no recoverable single-key locator; consumers
	// fall back to file-level positioning.
	//
	// How deep the locator reaches depends on which reading produced the
	// violation. A hand-assembled locator in this package addresses a
	// top-level component only, so a component the author declared inside a
	// subsystem yields an empty Field rather than a path naming the wrong
	// authoring site. A locator a declared rule's subject carries names the
	// subsystem the author declared, level by level, and then the component,
	// so it positions such a violation rather than declining to.
	Field string
	// Sentinel is the domain sentinel this violation wraps. ToCompilerError
	// surfaces it as the CompilerError's inner error so callers can detect
	// the violation class with errors.Is at the pipeline boundary. The rule
	// about a socket's declared label constraint and the rule about a
	// Feature's applicability both set it to ErrSelectorViolation, which is
	// why matching on it alone does not tell those two apart — the locator
	// does. The rule about an attachment slot's constraint carries its own
	// sentinel, so identity separates it from the other two.
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

// Error formats the violation as a single human-readable string. A policy
// verdict (Kind [AnalysisErrorPolicy]) returns its Message verbatim — the
// plugin already supplied a complete diagnostic — and so does a located
// violation (Kind [AnalysisErrorLocated]), whose message a rule already
// composed and whose site is carried on Field rather than in the text. A
// presence-mandate violation
// (Kind [AnalysisErrorAttachmentRequirement]) reads as "<node>: <message>",
// naming the node the mandate was evaluated against. An edge-shaped violation
// carrying a socket reads as "<source> -> <target> (<socket>): <message>"; one
// without a socket reads as "<source> attaches Feature <target>: <message>".
// Those two shapes are the only ones Socket presence tells apart, and both of
// them carry the zero-value kind; every other class is identified by its kind.
//
// No reading produces either edge-shaped composition — see
// [AnalysisErrorEdge] — so both branches serve a value a caller built. They are
// the composition that value's fields describe, which is why they remain the
// fall-through rather than an error.
func (e AnalysisError) Error() string {
	if e.Kind == AnalysisErrorPolicy || e.Kind == AnalysisErrorLocated {
		return e.Message
	}
	if e.Kind == AnalysisErrorAttachmentRequirement {
		return fmt.Sprintf("%s: %s", e.Source, e.Message)
	}
	if e.Socket != "" {
		return fmt.Sprintf("%s -> %s (%s): %s", e.Source, e.Target, e.Socket, e.Message)
	}
	return fmt.Sprintf("%s attaches Feature %s: %s", e.Source, e.Target, e.Message)
}

// ToCompilerError converts the analyzer violation into a *cerrors.CompilerError,
// choosing the category from the violation's Kind. A violation about one
// connection's declared label constraint, one about the applicability a Feature
// declares, and one about the constraint an attachment slot declares over what
// may fill it are all authoring-time problems on the developer's
// Aggregate — the developer authored the uses-edge or the Feature attachment
// that fails it — so they route
// to cerrors.DeveloperError. A policy verdict (Kind [AnalysisErrorPolicy]) is a
// governance rejection from an operator-attached policy plugin, not a
// developer authoring mistake, so it routes to cerrors.PolicyError; the
// remediation is to satisfy the enforced policy or reconsider its attachment,
// not to edit the Aggregate.
//
// The source pointer is read from whichever field the violation's class
// actually populates, and the two classes populate different ones. A violation
// a declared rule raised (Kind [AnalysisErrorLocated]) carries the document its
// finding is about on OriginFile and no node ID at all, so the pointer names
// that document. Every other class carries the violating node's ID on Source
// and that is the most-specific locator those readings produce, so the pointer
// names it. Reading one field for both would leave one class pointing nowhere,
// and the class it left empty would be the one carrying a real path. A policy
// verdict populates neither, so its pointer is message-only.
//
// Finer-grained positions within the document — line and column — are not
// composed here. The authoring key a violation is about is carried on Field,
// and turning a key into a line needs the parsed document, which a consumer
// walking source holds and this conversion does not.
//
// The CompilerError wraps e.Sentinel as its inner error, so a violation
// class is detectable with errors.Is at the pipeline boundary (the build
// error joins these CompilerErrors and errors.Is traverses the chain).
// The socket-constraint and Feature-applicability rules carry
// ErrSelectorViolation, the slot-fill rule carries
// ErrSlotSelectorUnsatisfied, and policy verdicts carry ErrPolicyViolation.
// When Sentinel is nil the inner chain is empty.
func (e *AnalysisError) ToCompilerError() *cerrors.CompilerError {
	category := cerrors.DeveloperError
	switch {
	case e.Kind == AnalysisErrorPolicy:
		category = cerrors.PolicyError
	case errors.Is(e.Sentinel, ErrMalformedAttachmentRequirement()):
		// The broken declaration is on the Component catalog entity, so the
		// remediation belongs to whoever authors the catalog — unlike an
		// unsatisfied mandate, which the Aggregate's author resolves by
		// attaching the feature.
		category = cerrors.CatalogError
	}
	pointer := e.Source
	if e.Kind == AnalysisErrorLocated {
		pointer = e.OriginFile
	}
	return cerrors.New(
		category,
		cerrors.SourceLocation{File: pointer},
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
//  1. Presence-mandate errors from validateRequiredAttachmentTypes and
//     unfilled-required-slot errors from validateRequiredAttachmentSlots (both
//     appended to the same Errors slice).
//  2. Precalculated paths from calculatePaths.
//  3. The PolicyInput payload from assemblePolicyInput.
//
// Three violation classes a reader will look for here are absent, and they are
// absent for the same reason the advisory below is. The label constraint a
// dependency socket declares over what may satisfy it is declared as
// [EdgeTargetSatisfiesSocketSelector]; the applicability a Feature declares over
// what it may attach to is declared as
// [AttachmentSatisfiesFeatureApplicability]; and the label constraint an
// attachment slot declares over what may fill it is declared as
// [SlotFillSatisfiesSlotSelector]. All three are evaluated over the
// presentation of the finalised graph, which this function is not handed and
// does not build — a presentation has one producer, and building a second one
// here would give one fact two answers. Their violations are appended to the
// same Errors slice by whatever holds that presentation, so a caller reading
// that slice still reads one stream and the gate below still sees them.
//
// It populates no advisory warnings, and the omission is where a reader is
// most likely to expect one. The annotation-override advisory is declared as
// [OverriddenAnnotationAdvisory] and is invoked over that same presentation. So
// AnalysisResult.Warnings is empty from here and is filled by whatever holds
// it; a caller reading it still reads one stream.
//
// Build flow gates on len(result.Errors) > 0 before invoking the render.
// AnalysisResult.Paths and AnalysisResult.PolicyInput are computed
// unconditionally and neither reaches that gate, and no warning reaches it
// either: a warning is advisory, so a result carrying warnings and no errors
// proceeds and the warnings are surfaced to the operator alongside the build.
// See the package doc Pipeline Integration subsection for their downstream
// consumption status.
func Analyze(graph *types.IRGraph, catalogLookup resolver.CatalogLookup) *AnalysisResult {
	// Background context never cancels, so the discarded error is always
	// nil; callers that participate in cancellation use AnalyzeContext.
	result, _ := AnalyzeContext(context.Background(), graph, catalogLookup)
	return result
}

// AnalyzeContext is Analyze with cooperative cancellation:
// validateRequiredAttachmentTypes and validateRequiredAttachmentSlots each check
// ctx.Err() per node — every validator the sequence runs, so none of them can
// hold a cancelled caller — so
// a long analysis over a large graph can be interrupted (e.g. by the daemon's
// shutdown drain). It returns a non-nil error only when ctx is cancelled
// mid-analysis; analyzer violations remain in the result's Errors slice as
// structured data, never as the function-level error. Analyze is the
// background-context convenience wrapper.
func AnalyzeContext(ctx context.Context, graph *types.IRGraph, catalogLookup resolver.CatalogLookup) (*AnalysisResult, error) {
	// Presence mandates — each requiredType annotation a node's Component
	// declares is satisfied by some attachment of that type.
	errs, err := validateRequiredAttachmentTypes(ctx, graph)
	if err != nil {
		return nil, err
	}

	// Required attachment slots — each slot a node's Component declares
	// required is bound by some attachment. A separate check from the presence
	// mandates above: a slot names one binding point, a mandate names a type at
	// any name.
	slotFillErrs, err := validateRequiredAttachmentSlots(ctx, graph, catalogLookup)
	if err != nil {
		return nil, err
	}
	errs = append(errs, slotFillErrs...)

	// DFS path calculation.
	paths := calculatePaths(graph)

	// Assemble policy input.
	policyInput := assemblePolicyInput(graph, paths)

	return &AnalysisResult{
		Errors:      errs,
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
