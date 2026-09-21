// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package reachviewalias is the assertion route again, with every document
// renamed first. It must FAIL to compile.
//
// It is a separate program from the plain assertion because the two are refused
// by different things. A reader that scans source text for the documents' own
// spellings sees nothing here: the names written below are local, and the
// package the documents come from is imported under a name of this file's
// choosing. What refuses this program is the type identity the language works
// in, under which an alias is the type it names and no renaming is available.
//
// That is the whole reason the gate is a compile failure rather than a scan. A
// scan reports a clean tree for exactly this file.
package reachviewalias

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	whatever "github.com/aggrega-ai/aggrega-cli/pkg/types"
)

type (
	// deployedApplication is the authored Aggregate document under another name.
	deployedApplication = whatever.Aggregate

	// environmentBinding is the Release document under another name.
	environmentBinding = whatever.Release

	// wiring is the finalised graph under another name.
	wiring = whatever.IRGraph
)

func TheAggregate(v constraint.RenderedView) *deployedApplication {
	return v.(*deployedApplication)
}

func TheRelease(v constraint.RenderedView) *environmentBinding {
	return v.(*environmentBinding)
}

func TheGraph(v constraint.RenderedView) *wiring { return v.(*wiring) }
