// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// dispatchedStageCoordinates is the assembly-path and provider enumeration a
// build's grouping carries.
//
// It reads the group attribution recorded when the last node was sorted into its
// group, and derives nothing else. That attribution is the only thing that knows
// which providers met at which evaluated path: a boundary's own path declaration
// says where it wants to land, and only the grouping says who else landed there.
// An enumeration reading the deployment document instead would name every path
// the operator declared, including ones no component reached, and would present a
// rule about a collision coordinates at which nothing was grouped.
//
// # Why it is built over the result rather than handed the attribution
//
// The provider set at a path is already answered by the result's own reader, and
// this value delegates that axis to the same reader the presentation delegates
// the fact to. One reader means the providers a rule is invoked over and the
// providers its facts are keyed by cannot disagree — and they could disagree,
// because each walk has its own deduplication to get right.
//
// # Order
//
// Both axes return a deterministic order and neither ranges a plain Go map. The
// attribution is held in assembly-path order and then provider order, so the path
// set is in path order and the provider set within a path keeps that same group
// order.
type dispatchedStageCoordinates struct {
	// result is the render whose grouping is being enumerated. It is held rather
	// than copied because the coordinates are written once, before any group
	// assembles, and are not written to afterwards.
	result *RenderResult

	// releaseFile positions an assembly-path coordinate at the document the
	// boundaries feeding it were declared in. It labels rather than answers: no
	// rule reads it, and it is what lets a finding about a group be reported at a
	// file rather than at nowhere.
	releaseFile string
}

// newDispatchedStageCoordinates enumerates how a render grouped its nodes.
//
// It returns nil when the render is absent or recorded no grouping. An
// enumeration over no grouping would present every rule an empty subject set, and
// a rule invoked zero times reports exactly what a rule invoked and satisfied
// reports — so a caller checks for nil rather than being handed a value that
// answers emptily.
func newDispatchedStageCoordinates(
	result *RenderResult, releaseFile string,
) *dispatchedStageCoordinates {
	if result == nil || len(result.Contributions) == 0 {
		return nil
	}
	return &dispatchedStageCoordinates{result: result, releaseFile: releaseFile}
}

// AssemblyPaths returns every evaluated assembly path a group landed at,
// deduplicated, in the order the grouping recorded them, which is path order.
//
// A path appears once however many providers assembled there — which is the whole
// point of the axis, since a rule invoked once per path and provider needs the
// path several providers share to be one coordinate rather than several.
//
// The coordinate is positioned at the deployment document, which is where the
// boundaries whose path declarations evaluated to it are written and the only
// document an operator can change one in. The path itself contributes no locator
// segment: it is evaluated output rather than a key that document holds.
func (c *dispatchedStageCoordinates) AssemblyPaths() []constraint.AuthoredCoordinate {
	var out []constraint.AuthoredCoordinate
	seen := make(map[string]bool, len(c.result.Contributions))
	for _, contribution := range c.result.Contributions {
		if seen[contribution.AssemblyPath] {
			continue
		}
		seen[contribution.AssemblyPath] = true
		out = append(out, constraint.AuthoredCoordinate{
			Value: contribution.AssemblyPath, SourceFile: c.releaseFile,
		})
	}
	return out
}

// AssemblyGroupProviders returns the providers that assembled at one evaluated
// assembly path, in the order the grouping recorded its groups.
//
// It delegates to the render's own reader rather than walking the attribution
// again, so a rule is invoked over exactly the providers the presentation can
// answer for.
func (c *dispatchedStageCoordinates) AssemblyGroupProviders(assemblyPath string) []string {
	return c.result.assemblyProviders(assemblyPath)
}

// The enumeration is held to every axis the rules over a build's grouping are
// keyed by, so an axis that stops being satisfied fails to compile here rather
// than at the invocation that needed it.
var (
	_ dispatchedStageAxes                        = (*dispatchedStageCoordinates)(nil)
	_ constraint.AssemblyPathEnumerator          = (*dispatchedStageCoordinates)(nil)
	_ constraint.AssemblyGroupProviderEnumerator = (*dispatchedStageCoordinates)(nil)
)
