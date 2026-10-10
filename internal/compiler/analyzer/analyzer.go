// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"context"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// AnalysisResult is the bundled output of [Analyze]. Two of its fields carry
// what this stage derives and two carry what is reported about it:
//
//   - Paths — the cycle-safe DFS enumeration of acyclic paths through
//     the IR graph; see [calculatePaths] for the contract.
//   - PolicyInput — the policy socket payload (always
//     non-nil); see [PolicyInput] for the ABI shape. The
//     pipeline marshals it to a configured policy plugin.
//   - Errors — one [AnalysisError] per violation: every violation a rule
//     declared over the finalised graph raised, plus any failed policy-socket
//     verdict. Both are appended by whatever holds the presentation those
//     rules read, so this slice leaves [Analyze] empty and is filled before a
//     caller reads it.
//     Build flow gates on len(Errors) > 0; see the package doc Pipeline
//     Integration subsection for the gating semantics.
//   - Warnings — one structured advisory per finding an advisory rule raised,
//     appended by the same holder and never reaching the Errors gate: a
//     warning does not make the emitted output wrong, so a result carrying
//     warnings and no errors proceeds.
//
// Warnings carries the diagnostic shape rather than a plain sentence, and that
// is the load-bearing part of it. An advisory rule names the authoring key its
// finding is about, and a plain sentence has nowhere to put that key but inside
// the text — so a consumer holding the parsed document could not walk to the
// finding without unpicking a composed sentence, and the position the rule
// computed was destroyed one layer before any channel saw it. Each entry
// therefore carries the displayed sentence on Message and the authoring key on
// the location, independently: a terminal prints the sentence, and a surface
// drawing the finding on its key reads the location. Both halves come from the
// same entry, so neither surface has to reconstruct what the other shows.
type AnalysisResult struct {
	Errors      []AnalysisError
	Warnings    []abi.Diagnostic
	Paths       []PrecalculatedPath
	PolicyInput *PolicyInput
}

// AnalysisErrorKind classifies an [AnalysisError] so a consumer branches on the
// class rather than on which fields happen to be filled.
//
// Two classes are produced, and the difference between them is where the
// finding's authoring site is: a rule over a compilation product carries one and
// a policy verdict does not. Nothing else distinguishes them, because the
// message is complete in both.
//
// The zero value names no class. It is not a class in its own right: a value
// arriving without a kind is one a caller built without saying what it holds, so
// it is treated as carrying no recoverable authoring site rather than as a
// shape of its own.
type AnalysisErrorKind string

const (
	// AnalysisErrorPolicy marks a policy-socket verdict whose Message is
	// already a complete, plugin-supplied diagnostic. A verdict is about the
	// graph as a whole rather than about one authoring key, so it carries no
	// locator and no origin file.
	AnalysisErrorPolicy AnalysisErrorKind = "policy"

	// AnalysisErrorLocated marks a violation whose Message is already a
	// complete diagnostic and whose authoring site is carried on Field and
	// OriginFile.
	//
	// It is the shape a rule reporting through [ErrorsFromObjections] takes,
	// and it is the only shape a rule reports in here. Such a rule names its
	// subject by a locator, which positions the finding on the offending
	// authoring key, so the site travels as structured fields rather than
	// inside the text — which is what lets a consumer walking the source
	// document place the finding without parsing a sentence.
	AnalysisErrorLocated AnalysisErrorKind = "located"
)

// AnalysisError is one violation this stage reports. Its Message is always a
// complete diagnostic, and the remaining fields say where the finding is, which
// is the only thing that differs across the classes:
//
//   - Violations a declared rule raised (Kind [AnalysisErrorLocated]):
//     Field names the authoring key the finding is about and OriginFile names
//     the document holding it. It is the shape every rule over the finalised
//     graph reports in — a socket's declared label constraint, the
//     applicability a Feature declares over what it may attach to, the
//     constraint an attachment slot declares over what may fill it, the
//     requirement that a slot declared required be bound at all, and the
//     presence mandate a component type declares.
//   - Policy verdicts (Kind [AnalysisErrorPolicy]): the complete diagnostic the
//     policy plugin supplied, with neither field populated — a verdict is about
//     the graph rather than about one authoring key.
//
// Kind is what distinguishes the classes and it is the only thing that does.
// The struct carries no coordinate fields of its own, and that is deliberate
// rather than an omission: a rule names its subject by a locator, so filling a
// node identifier or a socket name beside it would mean re-deriving coordinates
// the rule did not supply, and a re-derived coordinate can disagree with what
// the rule actually read.
type AnalysisError struct {
	// Kind classifies the violation. [AnalysisErrorLocated] marks one a
	// declared rule raised, carrying its authoring site on the two fields
	// below; [AnalysisErrorPolicy] marks a policy-socket verdict, which
	// carries neither. The zero value names no class and is treated as
	// carrying no recoverable site.
	Kind    AnalysisErrorKind
	Message string
	// OriginFile records the source file where the violating authoring lives,
	// in the form the compilation names that document by — relative to the
	// workspace root wherever the run was given one. A
	// rule's subject supplies it from the document its coordinate was authored
	// in; it stays empty when the violation has no recoverable single-file
	// origin. Consumers that filter
	// per-file diagnostics (the daemon's editor-facing validation) drop
	// entries whose OriginFile does not match the requested file; consumers
	// that surface all diagnostics ignore the field.
	OriginFile string
	// Field is a mapping-style dotted locator naming the authoring site
	// of the violation within OriginFile, e.g.
	// ".spec.components.<name>.features.<attachment>" for a violation about
	// one attachment. It mirrors the resolver's locator shape so a consumer
	// can walk the parsed source document to a sub-file position. Empty
	// when the violation has no recoverable single-key locator; consumers
	// fall back to file-level positioning.
	//
	// A locator a declared rule's subject carries names the subsystem the
	// author declared, level by level, and then the component, so a violation
	// about a component the author nested inside a subsystem is positioned
	// rather than declined.
	Field string
	// Document is which authoring document the violation was written in — one of
	// the two the constraint framework names, constraint.AuthoringDocumentAggregate
	// or constraint.AuthoringDocumentRelease — taken from the site the rule's
	// subject was read at, and empty where that site records none. It is what
	// ToCompilerError files the violation under, so a fault the operator wrote on
	// the Release reports as the operator's and one the developer wrote as the
	// developer's, whichever surface converts it.
	Document string
	// Sentinel is the domain sentinel this violation wraps. ToCompilerError
	// surfaces it as the CompilerError's inner error so callers can detect
	// the violation class with errors.Is at the pipeline boundary. The rule
	// about a socket's declared label constraint and the rule about a
	// Feature's applicability both set it to ErrSelectorViolation, which is
	// why matching on it alone does not tell those two apart — the locator
	// does. Each of the two rules about an attachment slot carries its own
	// sentinel — one for a fill that fails the slot's constraint, one for a
	// required slot nothing binds — so identity separates them from the pair
	// above and from each other, and the rule about a presence mandate a
	// component type declares carries a third.
	Sentinel error
}

// Error returns the violation's Message.
//
// It composes nothing, because there is nothing left to compose from: a policy
// plugin supplies a complete diagnostic, and a rule's message is rendered once
// at the conversion every reporting boundary shares. Where a finding is comes
// back on Field and OriginFile rather than in the text, so a consumer places it
// by walking the source document rather than by parsing a sentence — and a
// surface that renders only the text is reading exactly what the rule said.
func (e AnalysisError) Error() string {
	return e.Message
}

// ToCompilerError converts the violation into a *cerrors.CompilerError,
// choosing the category from the violation's Kind and, for a located one, from
// the document it was written in — see [AnalysisError.category].
//
// The source pointer names where a finding is, which only the located class
// knows: a rule's subject carries the file its coordinate was authored in and
// the authoring key within it, so both travel on the pointer — the file as the
// path a reader opens and the locator as the key to look at once open. A policy
// verdict is about the graph rather than a document, so its pointer is
// message-only.
//
// The locator is put on the pointer rather than folded into the message, and
// that is what keeps two findings apart: a rule names the declaring type in its
// text rather than the instance, so two components of one type failing one rule
// carry the same message about the same document and the locator is the only
// thing that differs. It also stays on Field, unchanged, for a consumer that
// walks the parsed document to a range instead of reading a sentence.
//
// Finer-grained positions within the document — line and column — are not
// composed here, because turning a key into a line needs the parsed document,
// which a consumer walking source holds and this conversion does not.
//
// The CompilerError wraps e.Sentinel as its inner error, so a violation
// class is detectable with errors.Is at the pipeline boundary (the build
// error joins these CompilerErrors and errors.Is traverses the chain).
// The socket-constraint and Feature-applicability rules carry
// ErrSelectorViolation, the slot-fill rule carries
// ErrSlotSelectorUnsatisfied, the required-slot rule carries
// ErrRequiredSlotUnfilled, the presence-mandate rule carries
// ErrMissingRequiredFeature, and policy verdicts carry ErrPolicyViolation.
// When Sentinel is nil the inner chain is empty.
func (e *AnalysisError) ToCompilerError() *cerrors.CompilerError {
	category := e.category()
	var pointer cerrors.SourceLocation
	if e.Kind == AnalysisErrorLocated {
		pointer = cerrors.SourceLocation{File: e.OriginFile, Locator: e.Field}
	}
	return cerrors.Unidentified(
		category,
		pointer,
		e.Error(),
		nil,
		e.Sentinel,
	)
}

// category answers which authoring surface the violation is filed under.
//
// A policy verdict (Kind [AnalysisErrorPolicy]) is a governance rejection from
// an operator-attached policy plugin rather than an authoring mistake in either
// document, so it is filed under the policy surface: the remedy is to satisfy the
// enforced policy or reconsider its attachment, not to edit a document.
//
// Any other violation is filed under the document it was written in, through
// [constraint.AuthoringDocumentCategory] — the one reading of which surface a
// finding written in an authoring document belongs to. So a fault in what the
// operator wrote on the Release reads as the operator's and a fault in what the
// developer wrote in the Aggregate as the developer's, whichever stage of the
// build noticed it, and a violation whose site recorded no document is filed under
// the developer's.
func (e *AnalysisError) category() cerrors.Category {
	if e.Kind == AnalysisErrorPolicy {
		return cerrors.PolicyError
	}
	return constraint.AuthoringDocumentCategory(e.Document)
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

// Analyze derives what the finalised graph makes available to the surfaces
// downstream of it. It returns a non-nil [AnalysisResult] populated with:
//
//  1. Precalculated paths from [calculatePaths].
//  2. The PolicyInput payload from assemblePolicyInput.
//
// It evaluates no rule, and that is the shape rather than a gap. Every rule
// whose facts are complete at this point is declared as a value the framework
// invokes — the label constraint a dependency socket declares over what may
// satisfy it is [EdgeTargetSatisfiesSocketSelector], the applicability a Feature
// declares over what it may attach to is
// [AttachmentSatisfiesFeatureApplicability], the label constraint an attachment
// slot declares over what may fill it is [SlotFillSatisfiesSlotSelector], the
// requirement that a slot declared required be bound at all is
// [RequiredAttachmentSlotBound], the presence mandate a component type declares
// is [RequiredAttachmentTypeAttached], and the annotation-override advisory is
// [OverriddenAnnotationAdvisory]. Each is evaluated over the presentation of the
// finalised graph, which this function is not handed and does not build — a
// presentation has one producer, and building a second one here would give one
// fact two answers.
//
// So AnalysisResult.Errors and AnalysisResult.Warnings are both empty from here
// and are filled by whatever holds that presentation. A caller reading either
// still reads one stream, and the gate below still sees what a rule raised.
//
// Build flow gates on len(result.Errors) > 0 before invoking the render.
// AnalysisResult.Paths and AnalysisResult.PolicyInput are computed
// unconditionally and neither reaches that gate, and no warning reaches it
// either: a warning is advisory, so a result carrying warnings and no errors
// proceeds and the warnings are surfaced to the operator alongside the build.
// See the package doc Pipeline Integration subsection for their downstream
// consumption status.
func Analyze(graph *types.IRGraph) *AnalysisResult {
	// Background context never cancels, so the discarded error is always
	// nil; callers that participate in cancellation use AnalyzeContext.
	result, _ := AnalyzeContext(context.Background(), graph)
	return result
}

// AnalyzeContext is Analyze with cooperative cancellation: the path enumeration
// reads ctx.Err() once per start node, so a long derivation over a large graph
// can be interrupted (e.g. by the daemon's shutdown drain). It returns a non-nil
// error only when ctx is cancelled mid-derivation, and a cancelled derivation
// returns no result at all rather than a truncated one — the policy payload is
// assembled from the whole enumeration, so a partial one would describe a graph
// the compiler did not see. Analyze is the background-context convenience
// wrapper.
//
// Enumeration is what carries the signal because enumeration is what takes the
// time: it walks from every node in the graph, and the per-start-node cap bounds
// each walk without bounding how many there are.
func AnalyzeContext(ctx context.Context, graph *types.IRGraph) (*AnalysisResult, error) {
	paths, err := calculatePaths(ctx, graph)
	if err != nil {
		return nil, err
	}

	policyInput := assemblePolicyInput(graph, paths)

	return &AnalysisResult{
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
