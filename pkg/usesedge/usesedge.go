// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package usesedge publishes the halting conditions of `uses` edge wiring so a
// consumer can match them with errors.Is.
//
// A `uses` edge is drawn between two components: the consuming component names
// a socket and a target, and the compiler routes the target's provides into the
// consumer's render context. Three rules govern what a Release may do to that
// wiring — it may bind a declared-but-unbound socket and it may set per-edge
// infra, but it may not bind a socket that neither the catalog entity nor the
// Aggregate declared, may not point an edge at a component that does not exist,
// and may not redirect an edge the developer already declared. This package
// makes the second and third of those matchable; the first is
// ErrUnknownUsesSocket in pkg/resolver.
//
// The conditions are raised inside packages the compiler does not export, so
// the accessors here forward to the same values the build reports. A match
// against one recognises exactly the failure the build would raise, and the
// accessor names are the same names the conditions carry throughout the
// documentation.
package usesedge

import "github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"

// ErrDanglingUsesTarget reports that a `uses` edge names a target component
// that is not present in the resolved topology. It is origin-agnostic: an edge
// authored in an Aggregate and an edge wired by a Release are checked alike.
// The build halts rather than dropping the edge, which would leave the
// consumer's injected context silently empty.
func ErrDanglingUsesTarget() error { return topology.ErrDanglingUsesTarget() }

// ErrReleaseEdgeRetarget reports that a Release per-edge `uses` entry matching
// an existing developer-declared edge supplies a different target. A Release
// may configure an edge or wire one into a declared-but-unbound socket, but the
// developer owns the architecture they authored, so redirecting a declared edge
// halts. An entry naming the same target, or omitting the target, is a
// configuration and is permitted.
func ErrReleaseEdgeRetarget() error { return topology.ErrReleaseEdgeRetarget() }
