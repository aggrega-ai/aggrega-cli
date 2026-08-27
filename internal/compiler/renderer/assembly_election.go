// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// resolvedBoundaryAnnotations is each boundary's annotations as the cascade left
// them, and nothing at all when the render was given no resolution.
//
// It is a read of what the render was handed rather than a computation: a
// boundary's annotations are resolved before the render begins, so the value here
// is the one every other reader of that cascade sees and the group's names are not
// a second resolution of it.
func resolvedBoundaryAnnotations(
	resolved *resolver.ResolveResult,
) map[string]types.OrderedMap[string] {
	if resolved == nil {
		return nil
	}
	return resolved.BoundaryAnnotations
}

// assemblyElection is what one dispatch group's elected engine says about
// combining the group's per-node output, whether it can, and what it names the
// result from.
//
// The assembly claim and the offer are independent and the pair is the whole
// point of holding them together: an engine states what becomes of the group's
// output, and the plugin serving it either offers that assembly or offers
// per-node rendering alone. A group whose claim and offer disagree is the one an
// author has to hear about, and neither half alone shows it.
//
// The naming is held beside them rather than derived later for the same reason
// both of those are: it is a function of which engine the group elected, and the
// candidates that election ran over are the render's own working state. Once the
// render has moved on, the elected engine is no longer reachable to ask.
type assemblyElection struct {
	// declared is the assembly the elected provider claims, empty when it claims
	// none and empty for a group no provider was elected for — a group fed only by
	// boundary-scope contributions. Both leave the group with no assembly to
	// perform, which is one state rather than two.
	declared string

	// provided reports whether the elected engine can carry out an assembly. It is
	// false for a group with no elected engine, on the same reading: nothing to
	// assemble with.
	provided bool

	// nameAnnotation is the boundary annotation the elected engine names the
	// group's assembly from, empty when it names none and empty for a group with
	// no elected engine. Both leave the group's name outside anything a boundary
	// authored.
	nameAnnotation string

	// nameMustAgree reports whether the elected engine requires every boundary
	// feeding the group to resolve nameAnnotation to one value. It travels beside
	// the annotation because neither means anything alone: an engine naming its
	// assembly from the first value it finds states an annotation and no
	// requirement.
	nameMustAgree bool

	// names is each contributing boundary's resolved value for nameAnnotation,
	// keyed by boundary in the order the group first heard from each. It is empty
	// where nameAnnotation is, because there is then no annotation to resolve; a
	// boundary present with an empty value resolved the annotation to nothing,
	// which is a different state from being absent.
	names types.OrderedMap[string]
}

// assemblyElections is the render's retained record of what each dispatch group's
// elected engine claimed and what it could do.
//
// It is a retention rather than a read of the render's working state because that
// state is a loop-local: the candidate engines a group collected are dropped when
// the render returns, and the presentation of the grouping outlives it. A reader
// asking what a group's elected engine claimed has this and nothing else.
//
// It is keyed by the group rather than by a node deliberately. A group may hold
// nodes whose providers resolve at different versions, and exactly one of them is
// elected to assemble for the whole group, so the claim that governs the group is
// not a function of any node in it.
type assemblyElections struct {
	// byGroup is looked up by group and never walked, so it holds no order to
	// keep: every reader arrives with a path and a provider in hand.
	byGroup map[dispatchKey]assemblyElection
}

// newAssemblyElections records what each group's elected engine claimed, what it
// could do and what it names its assembly from, at the point every contributor
// has been sorted into its group.
//
// It is taken here because this is the first point the election is settled and the
// last point at which acting on it still saves work: a group's candidate engines
// are complete once the node walk has ended, and the election is a function of
// those candidates alone, so nothing further in the render can change what a group
// would elect.
//
// A group whose candidates are empty records an absent claim, no assembly and no
// naming, so every question a rule asks has an answer for every group rather than
// for only the ones that elected something.
//
// boundaryAnnotations is each boundary's annotations as the cascade resolved them,
// which is an input to the render rather than anything the render computes — so
// the resolved value read here is the same one every other reader of that cascade
// sees, and the group's names are not a second resolution of it.
func newAssemblyElections(
	groups map[dispatchKey]*pendingDispatch,
	keys []dispatchKey,
	boundaryAnnotations map[string]types.OrderedMap[string],
) *assemblyElections {
	out := &assemblyElections{byGroup: make(map[dispatchKey]assemblyElection, len(keys))}
	for _, key := range keys {
		elected := groupAssemblerEngine(groups[key])
		_, provided := elected.(BoundaryAssembler)
		annotation, mustAgree := assemblyNameAnnotationOf(elected)
		out.byGroup[key] = assemblyElection{
			declared:       boundaryAssemblyOf(elected),
			provided:       provided,
			nameAnnotation: annotation,
			nameMustAgree:  mustAgree,
			names: groupAssemblyNames(
				annotation, groups[key], boundaryAnnotations,
			),
		}
	}
	return out
}

// groupAssemblyNames resolves annotation for each boundary feeding one group, in
// the order the group first heard from each.
//
// It returns nothing at all when annotation is empty: there is then no annotation
// to resolve, and a map of empty values would be indistinguishable from a group
// whose boundaries each resolved a real annotation to nothing. Those are different
// findings, so they are different answers.
//
// A boundary the cascade holds no annotations for is present with an empty value
// rather than skipped, because a boundary that brought no name to the assembly it
// feeds is exactly what a rule about the group's naming has to be able to name.
func groupAssemblyNames(
	annotation string,
	group *pendingDispatch,
	boundaryAnnotations map[string]types.OrderedMap[string],
) types.OrderedMap[string] {
	var out types.OrderedMap[string]
	if annotation == "" || group == nil {
		return out
	}
	for _, boundary := range group.contributingBoundaries {
		name := ""
		if resolved, ok := boundaryAnnotations[boundary]; ok {
			name, _ = resolved.Get(annotation)
		}
		out.Set(boundary, name)
	}
	return out
}

// declaredAssembly returns the assembly the group's elected provider claims, and
// the empty string for a group nothing was recorded for.
func (e *assemblyElections) declaredAssembly(assemblyPath, provider string) string {
	return e.byGroup[dispatchKey{Path: assemblyPath, Provider: provider}].declared
}

// assemblerProvided reports whether the group's elected engine can carry out an
// assembly, and false for a group nothing was recorded for.
func (e *assemblyElections) assemblerProvided(assemblyPath, provider string) bool {
	return e.byGroup[dispatchKey{Path: assemblyPath, Provider: provider}].provided
}

// assemblyNameAnnotation returns the boundary annotation the group's elected
// engine names its assembly from and whether that engine requires the group's
// boundaries to agree on it, and ("", false) for a group nothing was recorded for.
func (e *assemblyElections) assemblyNameAnnotation(
	assemblyPath, provider string,
) (string, bool) {
	got := e.byGroup[dispatchKey{Path: assemblyPath, Provider: provider}]
	return got.nameAnnotation, got.nameMustAgree
}

// assemblyNames returns each contributing boundary's resolved value for that
// annotation, and nothing for a group nothing was recorded for.
func (e *assemblyElections) assemblyNames(
	assemblyPath, provider string,
) types.OrderedMap[string] {
	return e.byGroup[dispatchKey{Path: assemblyPath, Provider: provider}].names
}

// groupDeclaredAssembly returns the assembly the provider elected to assemble one
// dispatch group claims, empty when it claims none.
//
// It is unexported for the reason the group's other readers are: its only readers
// are in this package — the presentation of what the grouping holds — and one walk
// is what keeps a coordinate a rule runs over and the value it reads from being
// derived twice. A render that recorded no election answers empty, which is the
// same answer a group with nothing elected gets.
func (r *RenderResult) groupDeclaredAssembly(assemblyPath, provider string) string {
	if r.elections == nil {
		return ""
	}
	return r.elections.declaredAssembly(assemblyPath, provider)
}

// groupAssemblerProvided reports whether the engine elected to assemble one
// dispatch group can carry out an assembly.
//
// A render that recorded no election answers false, on the same reading the
// retention itself uses: no elected engine is nothing to assemble with.
func (r *RenderResult) groupAssemblerProvided(assemblyPath, provider string) bool {
	if r.elections == nil {
		return false
	}
	return r.elections.assemblerProvided(assemblyPath, provider)
}

// groupAssemblyNameAnnotation returns the boundary annotation the engine elected
// to assemble one dispatch group names its assembly from, and whether that engine
// requires the group's boundaries to agree on it.
//
// A render that recorded no election answers ("", false), which is the same answer
// a group with nothing elected gets: neither has a name anything authored.
func (r *RenderResult) groupAssemblyNameAnnotation(
	assemblyPath, provider string,
) (string, bool) {
	if r.elections == nil {
		return "", false
	}
	return r.elections.assemblyNameAnnotation(assemblyPath, provider)
}

// groupBoundaryAssemblyNames returns what each boundary feeding one dispatch group
// resolved that annotation to, keyed by boundary in the order the group first
// heard from each.
//
// A render that recorded no election answers nothing, on the same reading the
// retention itself uses: with no annotation there is nothing to resolve.
func (r *RenderResult) groupBoundaryAssemblyNames(
	assemblyPath, provider string,
) types.OrderedMap[string] {
	if r.elections == nil {
		var none types.OrderedMap[string]
		return none
	}
	return r.elections.assemblyNames(assemblyPath, provider)
}
