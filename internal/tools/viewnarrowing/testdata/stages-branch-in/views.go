// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package branchin is a view chain that joins: one view embeds two others, so
// its facts are complete at a point two stages claim. Reading this shape must
// refuse for the same reason a branch must — the order between the stages it
// joins is not stated by the embedding.
package branchin

type Check[V View, S any] struct {
	Run func(V, S) []string
}

type View interface{}

type AlphaView interface {
	View

	AlphaFact(node string) string
}

type BetaView interface {
	AlphaView

	BetaFact(node string) string
}

type JoinView interface {
	AlphaView
	BetaView

	JoinFact(node string) string
}
