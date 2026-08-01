// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

// NoHostPowers is the empty set of host powers, and the capability a check
// declares when it needs none.
//
// Host powers — reading the filesystem, resolving an entry in the plugin cache,
// fetching an artifact, reading workspace configuration — are a separate axis
// from the facts a check reads, declared separately and never folded into a
// view. Folding them in would make every check nominally able to read the
// filesystem, which erases the distinction the axis exists to draw.
//
// The axis is narrow, and it is not empty. A shipped check does read a host
// power to decide its predicate: the rendering pipeline's pre-walk
// provider-compatibility enforcement. For each pipeline step it resolves the
// step's plugin, loads it and calls the plugin's describe() export — that
// resolution and load is the host power — and halts when the provider rendering
// the scope the step's export declares is absent from the providers the plugin
// reports its step is compatible with. A value a host power returned is what
// decides the halt.
//
// Host powers concentrate at the plugin boundary, where a guest declares what it
// needs, and that occupant is where the boundary shows rather than an exception
// to it: the power it exercises is a call across the boundary itself. So the
// axis is not ceremony for a case that never arises, and a check declaring a
// non-empty capability is a finding worth examining rather than a routine
// shape.
//
// The registry has no slot for a host power at all: a fact declares a stage, a
// carrier, a subject key and a result, and there is nowhere to record a
// filesystem read. So a host power cannot arrive through a fact even by
// accident; it can only arrive by declaration, on this axis.
type NoHostPowers struct{}
