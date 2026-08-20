// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package reachviewassertion widens the view it was handed back down to a
// whole document. It must FAIL to compile.
//
// This is the route that does not depend on any accessor name. The question it
// asks is whether the value a rule receives could BE a document — whether the
// compilation might hand a document straight through as the view — and the
// compiler answers it by method set rather than by spelling: a document
// satisfies none of the view's accessors, so the assertion is refused where it
// is written rather than failing at run time on some inputs and not others.
package reachviewassertion

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func TheAggregate(v constraint.RenderedView) *types.Aggregate { return v.(*types.Aggregate) }

func TheRelease(v constraint.RenderedView) *types.Release { return v.(*types.Release) }

func TheGraph(v constraint.RenderedView) *types.IRGraph { return v.(*types.IRGraph) }
