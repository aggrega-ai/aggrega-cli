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
// The axis is expected to be empty. Across the whole corpus of shipped checks in
// this compiler, none reads a host power to decide its predicate: the host-power
// consumers in the same packages fetch entities, resolve plugins and load files,
// and none of them evaluates a rule over the compilation's product. Host powers
// concentrate instead at the plugin boundary, where a guest declares what it
// needs. So the axis is not ceremony for a case that never arises — its
// emptiness is the evidence it is drawn in the right place, and a check
// declaring a non-empty capability is a finding worth examining rather than a
// routine shape.
//
// The registry has no slot for a host power at all: a fact declares a stage, a
// carrier, a subject key and a result, and there is nowhere to record a
// filesystem read. So a host power cannot arrive through a fact even by
// accident; it can only arrive by declaration, on this axis.
type NoHostPowers struct{}
