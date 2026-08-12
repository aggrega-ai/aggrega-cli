// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

// NoHostPowers is the empty set of host powers, and the capability a check
// declares when it needs none.
//
// Host powers — reading the filesystem, resolving an entry in the plugin cache,
// fetching an artifact, reading workspace configuration, calling a guest across
// the plugin boundary — are a separate axis from the facts a check reads,
// declared separately and never folded into a view. Folding them in would make
// every check nominally able to read the filesystem, which erases the
// distinction the axis exists to draw.
//
// The axis is narrow, and it is occupied. What occupies it is fact production
// rather than any check. Producing the facts a stage holds is what reaches out:
// the stage holding each plugin's self-report resolves the plugin, fetches its
// bytes and calls describe() across the boundary; the stage holding the
// normalized documents reaches the catalog; the stage holding artifact bytes
// runs the rendering guests. So the powers are declared where they are
// exercised — on each stage of the fact registry, every stage stating its value
// and a stage reaching for nothing saying so rather than omitting it.
//
// No declared check reads a host power, and none needs to. A check receives its
// view and its subject and nothing else, so every value it reads was already
// produced for it, including every value some production used a power to
// obtain. Host powers concentrate at the plugin boundary, where a guest declares
// what it needs, and that concentration is where the boundary shows rather than
// an exception to it: the power exercised there is a call across the boundary
// itself. So the axis is not ceremony for a case that never arises, and a check
// declaring a non-empty capability is a finding worth examining rather than a
// routine shape.
//
// A fact has no slot for a host power: it declares a stage, a carrier, a subject
// key and a result, and there is nowhere in that to record a filesystem read. So
// a power cannot arrive through a fact even by accident. It arrives only by
// declaration — on the stage, where the production that exercises it is, and on
// this axis, which is what a check would have to use if one ever needed one.
type NoHostPowers struct{}
