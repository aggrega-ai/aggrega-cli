// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package branchout is a view chain that is not a chain: two views embed the
// base, so the facts they declare are complete at points with no order between
// them. A check reading one fact from each branch has no single narrowest
// sufficient view, so reading this shape must refuse rather than pick one.
package branchout

type Check[V View, C any, S any] struct {
	Run func(V, S) []string
}

type View interface{}

type AlphaView interface {
	View

	AlphaFact(node string) string
}

type BetaView interface {
	View

	BetaFact(node string) string
}
