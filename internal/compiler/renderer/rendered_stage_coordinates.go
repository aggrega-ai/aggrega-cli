// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"slices"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// renderedStageCoordinates is the boundary, provider and output-path enumeration
// a render's own assembled output carries.
//
// It reads the attribution the assembly recorded beside each group and derives
// nothing else. That attribution is the only thing that knows which boundaries
// fed which group: boundaries whose paths evaluate alike under one provider
// assemble as one group, so the group key alone names a path and a provider and
// not the boundaries behind them. An enumeration reading the deployment document
// instead would name every boundary the operator declared, including ones no
// component reached, and would present a rule about assembled bytes coordinates
// at which nothing was assembled.
//
// # Why it is built over the result rather than handed the attribution
//
// The path set and the bytes at a path are already answered by the result's own
// readers, and this value delegates the path axis to the same reader the
// presentation delegates the fact to. One reader means the paths a rule is
// invoked over and the paths its facts are keyed by cannot disagree — and they
// could disagree, because a boundary reaching more than one assembly path under
// one provider takes the union across all of them, which is a join a second
// implementation would be free to get wrong in a way nothing compares.
//
// # Order
//
// Every axis returns a deterministic order and each states which. The boundary
// set keeps the order the assembly recorded its groups in, and within a group the
// order that group first heard from each boundary; the provider set within a
// boundary keeps the same group order; the path set is in path order, which is
// the order the result's own reader promises. No axis here ranges a plain Go map.
type renderedStageCoordinates struct {
	// result is the render whose assembled output is being enumerated. It is held
	// rather than copied because the attribution is written once, by the flush
	// that precedes this value's construction, and is not written to afterwards.
	result *RenderResult

	// releaseFile positions a boundary coordinate at the document the boundary was
	// declared in. It labels rather than answers: no rule reads it.
	releaseFile string
}

// newRenderedStageCoordinates enumerates what a render assembled.
//
// It returns nil when the render is absent or recorded no attribution. An
// enumeration over no attribution would present every rule an empty subject set,
// and a rule invoked zero times reports exactly what a rule invoked and satisfied
// reports — so a caller checks for nil rather than being handed a value that
// answers emptily.
//
// A render that genuinely assembled nothing is the same nil, and deliberately:
// there is no output for a rule about assembled bytes to be asked about, so the
// distinction between "nothing assembled" and "no attribution recorded" makes no
// difference to what any rule here could find.
func newRenderedStageCoordinates(
	result *RenderResult, releaseFile string,
) *renderedStageCoordinates {
	if result == nil || len(result.Contributions) == 0 {
		return nil
	}
	return &renderedStageCoordinates{result: result, releaseFile: releaseFile}
}

// Boundaries returns every boundary that fed assembled output, deduplicated, in
// the order the assembly recorded its groups and, within a group, the order that
// group first heard from each boundary.
//
// A boundary appears once however many groups it fed. Its coordinate is
// positioned at the deployment document, which is where a boundary is declared
// and the only document an operator can edit a boundary in.
func (c *renderedStageCoordinates) Boundaries() []constraint.AuthoredCoordinate {
	seen := make(map[string]bool, len(c.result.Contributions))
	var out []constraint.AuthoredCoordinate
	for _, contribution := range c.result.Contributions {
		for _, boundary := range contribution.Boundaries {
			if seen[boundary] {
				continue
			}
			seen[boundary] = true
			out = append(out, constraint.AuthoredCoordinate{
				Value: boundary, SourceFile: c.releaseFile,
			})
		}
	}
	return out
}

// Providers returns the providers that assembled output within one boundary,
// deduplicated, in the order the assembly recorded its groups.
//
// A boundary reaching several providers — a mixed-provider boundary — yields one
// entry per provider, because each rendered its own group and each group's output
// paths are its own.
func (c *renderedStageCoordinates) Providers(boundary string) []string {
	seen := make(map[string]bool, len(c.result.Contributions))
	var out []string
	for _, contribution := range c.result.Contributions {
		if seen[contribution.Provider] || !slices.Contains(contribution.Boundaries, boundary) {
			continue
		}
		seen[contribution.Provider] = true
		out = append(out, contribution.Provider)
	}
	return out
}

// EmittedPaths returns the output paths written for one provider within one
// boundary, in path order.
//
// It delegates to the render's own reader rather than walking the attribution
// again, so a rule is invoked over exactly the paths the presentation can answer
// the bytes for. The two are the same question asked from two sides, and a second
// walk would be free to join boundary to path differently with nothing comparing
// them.
func (c *renderedStageCoordinates) EmittedPaths(boundary string, provider string) []string {
	return c.result.EmittedPaths(boundary, provider)
}

// The enumeration is held to every axis the rules over a render's assembled
// output are keyed by, so an axis that stops being satisfied fails to compile
// here rather than at the invocation that needed it.
var (
	_ renderedStageAxes                = (*renderedStageCoordinates)(nil)
	_ constraint.BoundaryEnumerator    = (*renderedStageCoordinates)(nil)
	_ constraint.ProviderEnumerator    = (*renderedStageCoordinates)(nil)
	_ constraint.EmittedPathEnumerator = (*renderedStageCoordinates)(nil)
)
