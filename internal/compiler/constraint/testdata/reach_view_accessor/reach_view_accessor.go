// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package reachviewaccessor asks the deepest view for a whole authored
// document, and for the finalised graph, by the names an author reaching for one
// would actually write. It must FAIL to compile.
//
// This is the first of the routes: the rule has a view in hand and asks it for
// the document rather than for a fact read out of it. A view exposes named facts
// and nothing whole, so there is no accessor to call and the program does not
// build.
package reachviewaccessor

import "github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"

func TheAggregate(v constraint.RenderedView) any { return v.Aggregate() }

func TheRelease(v constraint.RenderedView) any { return v.Release() }

func TheGraph(v constraint.RenderedView) any { return v.Graph() }

func TheGraphUnderItsTypeName(v constraint.RenderedView) any { return v.IRGraph() }
