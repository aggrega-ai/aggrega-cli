// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

// assemblyElection is what one dispatch group's elected engine says about
// combining the group's per-node output, and whether it can.
//
// The two are independent and the pair is the whole point of holding them
// together: an engine states what becomes of the group's output, and the plugin
// serving it either offers that assembly or offers per-node rendering alone. A
// group whose claim and offer disagree is the one an author has to hear about, and
// neither half alone shows it.
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

// newAssemblyElections records what each group's elected engine claimed and what
// it could do, at the point every contributor has been sorted into its group.
//
// It is taken here because this is the first point the election is settled and the
// last point at which acting on it still saves work: a group's candidate engines
// are complete once the node walk has ended, and the election is a function of
// those candidates alone, so nothing further in the render can change what a group
// would elect.
//
// A group whose candidates are empty records an absent claim and no assembly, so
// the two questions a rule asks have an answer for every group rather than for
// only the ones that elected something.
func newAssemblyElections(
	groups map[dispatchKey]*pendingDispatch, keys []dispatchKey,
) *assemblyElections {
	out := &assemblyElections{byGroup: make(map[dispatchKey]assemblyElection, len(keys))}
	for _, key := range keys {
		elected := groupAssemblerEngine(groups[key])
		_, provided := elected.(BoundaryAssembler)
		out.byGroup[key] = assemblyElection{
			declared: boundaryAssemblyOf(elected),
			provided: provided,
		}
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
