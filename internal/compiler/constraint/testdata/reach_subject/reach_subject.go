// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package reachsubject asks the other value a rule is handed — the subject —
// for a whole document. It must FAIL to compile.
//
// A rule receives exactly two things, so covering the view alone would leave the
// second one unexamined. A subject carries the provenance of the thing it stands
// for and the ability to raise about it, and nothing else, so there is no
// accessor here either.
//
// The bound on this one is worth stating where it is written rather than only
// where it is summarised: it probes the contract every subject kind embeds, so
// what it establishes is that the shared contract offers no route. A kind that
// declared an accessor of its own would not be refused here.
package reachsubject

import "github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"

func TheAggregate(s constraint.Subject) any { return s.Aggregate() }

func TheRelease(s constraint.Subject) any { return s.Release() }

func TheGraph(s constraint.Subject) any { return s.Graph() }
