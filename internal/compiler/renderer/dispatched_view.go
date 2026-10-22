// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// assemblyGroupFacts is the seam the presentation below answers this stage's
// facts through.
//
// It is a declared seam rather than a handful of method bodies reaching into the
// result for the reason the assembled-output seam is one: the render already
// retains what every fact here reads, all of it recorded at the point the last
// node was sorted into its group — the group attribution, what each group's
// elected engine declared, and what each group's contributors declared they
// derived — so a presentation deriving any of it again would give one fact two
// producers, free to disagree with nothing comparing them. Naming the seam is
// what makes the delegation visible as delegation, and it is the shape a guard
// derives this stage's split from.
type assemblyGroupFacts interface {
	// AssemblyGroupProviderSet are the providers that assemble at one evaluated
	// assembly path, in group order.
	AssemblyGroupProviderSet(assemblyPath string) []string

	// AssemblyGroupBoundarySet are the boundaries whose components fed one
	// assembly group, in the order the group first heard from each.
	AssemblyGroupBoundarySet(assemblyPath string, provider string) []string

	// AssemblyGroupProviderAssembly is how the provider elected to assemble one
	// dispatch group declares it combines the group's per-node output.
	AssemblyGroupProviderAssembly(assemblyPath string, provider string) string

	// AssemblyGroupAssemblerProvided reports whether the plugin serving that
	// elected provider offers the assembly its claim names.
	AssemblyGroupAssemblerProvided(assemblyPath string, provider string) bool

	// AssemblyGroupAssemblyNameAnnotation is the boundary annotation that elected
	// provider names the group's assembly from, and whether it requires the
	// group's boundaries to agree on the value.
	AssemblyGroupAssemblyNameAnnotation(assemblyPath string, provider string) (string, bool)

	// AssemblyGroupBoundaryAssemblyNames is what each boundary feeding the group
	// resolved that annotation to, keyed by boundary in group order.
	AssemblyGroupBoundaryAssemblyNames(assemblyPath string, provider string) types.OrderedMap[string]

	// AssemblyGroupDerivedNames are the names the group's contributors declared
	// they derived inside their own output, in space, name and contributor order.
	AssemblyGroupDerivedNames(assemblyPath string, provider string) []constraint.DerivedNameDeclaration

	// AssemblyGroupBoundaryPathDeclarations are what each contribution's boundary
	// path declaration evaluated to, in the order the group first received each.
	AssemblyGroupBoundaryPathDeclarations(assemblyPath string, provider string) []constraint.BoundaryPathDeclaration

	// AssemblyGroupContributorDirectories are the contributors the group places
	// under a directory of their own, each with its segment.
	AssemblyGroupContributorDirectories(assemblyPath string, provider string) []constraint.ContributorDirectory
}

// assemblyGroups answers those facts from the render's own group attribution.
//
// It holds the result rather than a copy of what it reads. The attribution, the
// election and the derived-name record are each written once, when every node has
// been sorted into its group and before any group assembles, and none is written
// to afterwards; the output paths the same attribution records later gain are a
// different field and no fact here reads them.
//
// No accessor re-derives anything: each delegates to the reader the result itself
// holds. The set-valued answers are walks of the attribution in the order the
// attribution holds, which is assembly-path order and then provider order, so they
// answer the same sequence for identical input; the election answers are lookups
// keyed by the group, and the one among them that is a keyed set carries the
// attribution's own boundary order rather than an order of its own.
type assemblyGroups struct {
	result *RenderResult
}

// AssemblyGroupProviderSet returns the providers that assemble at one evaluated
// assembly path, in group order.
//
// It is the result's own reader verbatim. A path no group assembled at answers
// with nothing, which is the same answer this stage gives for any coordinate the
// attribution does not record.
func (a assemblyGroups) AssemblyGroupProviderSet(assemblyPath string) []string {
	return a.result.assemblyProviders(assemblyPath)
}

// AssemblyGroupBoundarySet returns the boundaries whose components fed one
// assembly group, in the order the group first heard from each.
//
// It is the result's own reader verbatim, for the reason the fact above is: one
// walk, so the coordinate a rule is invoked over and the value it reads cannot be
// derived two ways.
func (a assemblyGroups) AssemblyGroupBoundarySet(assemblyPath string, provider string) []string {
	return a.result.assemblyBoundaries(assemblyPath, provider)
}

// AssemblyGroupProviderAssembly returns how the provider elected to assemble one
// dispatch group declares it combines the group's per-node output, empty when it
// declares no assembly claim.
//
// It is the result's own reader verbatim, for the reason the facts above are: the
// election is recorded once, and reading it in one place is what keeps the claim a
// rule reads from being derived a second way.
func (a assemblyGroups) AssemblyGroupProviderAssembly(
	assemblyPath string, provider string,
) string {
	return a.result.groupDeclaredAssembly(assemblyPath, provider)
}

// AssemblyGroupAssemblerProvided reports whether the plugin serving the provider
// elected to assemble one dispatch group offers the assembly that provider's claim
// names.
//
// It is the result's own reader verbatim. A group nothing was elected for answers
// false, which is the same answer a group whose plugin offers no assembly gets:
// neither has an assembly it can perform.
func (a assemblyGroups) AssemblyGroupAssemblerProvided(
	assemblyPath string, provider string,
) bool {
	return a.result.groupAssemblerProvided(assemblyPath, provider)
}

// AssemblyGroupAssemblyNameAnnotation returns the boundary annotation the provider
// elected to assemble one dispatch group names the assembly from, and whether that
// provider requires the group's boundaries to agree on the value.
//
// It is the result's own reader verbatim, for the reason the election's other
// answers are: what the elected engine declared is recorded once, and reading it in
// one place is what keeps a rule's input from being derived a second way.
func (a assemblyGroups) AssemblyGroupAssemblyNameAnnotation(
	assemblyPath string, provider string,
) (string, bool) {
	return a.result.groupAssemblyNameAnnotation(assemblyPath, provider)
}

// AssemblyGroupBoundaryAssemblyNames returns what each boundary feeding one
// dispatch group resolved that annotation to, keyed by boundary in the order the
// group first heard from each.
//
// It is the result's own reader verbatim. The order is the attribution's own, so
// this and the group's boundary set answer the same sequence for identical input
// without either being derived from the other.
func (a assemblyGroups) AssemblyGroupBoundaryAssemblyNames(
	assemblyPath string, provider string,
) types.OrderedMap[string] {
	return a.result.groupBoundaryAssemblyNames(assemblyPath, provider)
}

// AssemblyGroupDerivedNames returns the names the contributors to one dispatch
// group declared they derived inside their own output, in key-space order and then
// name order and then contributor order.
//
// It is the result's own reader verbatim, for the reason the answers above are:
// the declarations are accumulated once as the contributors render, and reading
// them in one place is what keeps a rule's input from being derived a second way.
func (a assemblyGroups) AssemblyGroupDerivedNames(
	assemblyPath string, provider string,
) []constraint.DerivedNameDeclaration {
	return a.result.groupDerivedNames(assemblyPath, provider)
}

// AssemblyGroupBoundaryPathDeclarations returns what each contribution's boundary
// path declaration evaluated to for one dispatch group, in the order the group
// first received each.
//
// It is the result's own reader verbatim, for the reason the answers above are:
// the declarations are recorded once, as each contribution is sorted into its
// group, and reading them in one place is what keeps a rule's input from being
// derived a second way.
func (a assemblyGroups) AssemblyGroupBoundaryPathDeclarations(
	assemblyPath string, provider string,
) []constraint.BoundaryPathDeclaration {
	return a.result.groupBoundaryPathDeclarations(assemblyPath, provider)
}

// AssemblyGroupContributorDirectories returns the contributors one dispatch group
// places under a directory of their own, each with the segment it is named by.
//
// It is the result's own reader verbatim. The record is taken from the layout the
// group's finalization reads and the composition it writes with, so the
// directories a rule reads here are the ones the build would create.
func (a assemblyGroups) AssemblyGroupContributorDirectories(
	assemblyPath string, provider string,
) []constraint.ContributorDirectory {
	return a.result.groupContributorDirectories(assemblyPath, provider)
}

// dispatchedView presents, as named facts, how a build's rendered nodes were
// sorted into the groups they will be assembled in: the providers that met at
// each evaluated assembly path, and the boundaries that fed each group.
//
// It satisfies [constraint.DispatchedView], and the compiler holds it to that
// contract at the bottom of this file rather than a comment claiming it.
//
// # The earlier stage is embedded, not re-projected
//
// A stage's view contains the stage it extends, so every fact complete before the
// grouping was known is readable here. Those facts are answered by the
// presentation of what each node's own render emitted, embedded and delegated to,
// rather than derived again: one fact has one producer, and two producers of a
// fact are free to disagree with nothing comparing them. That presentation in
// turn embeds the one below it, so a rule reading a fact from an earlier stage
// through this value reaches the same answer that stage's own presentation
// gives.
//
// # Why this stage exists at all rather than being folded into the one after it
//
// The grouping is complete at a point the assembly has not reached: every node
// has rendered and every boundary has been sorted into the group its own path
// declaration evaluated to, and no group has merged anything yet. A rule about
// which providers met at one path is answerable there and its remedy is an edit
// to a boundary's own path declaration — so placing it a stage later would run it
// after the compiler had done work the answer was always going to discard, and
// would read an attribution whose file coordinates had already been written past
// each other.
type dispatchedView struct {
	constraint.EmittedView
	assemblyGroupFacts
}

// newDispatchedView presents how a render grouped its nodes for assembly, with
// the facts complete earlier answered by the presentation of what each node's own
// render emitted.
//
// It is unexported because the render is the one thing that builds it: the
// grouping it reads is the render's own working state, so a caller outside this
// package has nothing to construct it from and the bypass does not compile.
//
// It returns nil when the earlier presentation or the group seam is absent. A
// presentation missing either would answer a whole class of facts from nothing,
// and a presentation that answers emptily is worse than an absent one, because a
// rule reading it cannot tell an empty answer from a real one.
func newDispatchedView(
	earlier constraint.EmittedView,
	groups assemblyGroupFacts,
) constraint.DispatchedView {
	if earlier == nil || groups == nil {
		return nil
	}
	return &dispatchedView{EmittedView: earlier, assemblyGroupFacts: groups}
}

var _ constraint.DispatchedView = (*dispatchedView)(nil)
