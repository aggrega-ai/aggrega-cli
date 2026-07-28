// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// derivedSensitiveKeys reads which of a Recipe's template outputs were computed
// from a declared-sensitive datum, so a display can withhold them.
//
// It exists because withholding by KEY is not enough. A declared-sensitive leaf
// is identifiable wherever the leaf itself appears, but a template that
// interpolates it produces a new string under a key nobody declared — a local
// whose text reads a withheld password prints that password in the clear on every
// surface that shows the resolved block. What can be read to recover the link is
// the reference the author wrote, and nothing else: the resolved value is
// arbitrary text the compiler must not inspect, and a key's name says nothing
// about what it holds.
//
// The analysis is entirely STATIC — authored template text plus the declared-leaf
// path sets. It evaluates nothing, needs no resolved value, and has no ordering
// relationship to the evaluation, which is what lets it run wherever the
// declarations and the Recipe are both in scope.
//
// A reference withholds its output when, after its pillar prefix is stripped, the
// referenced path is EQUAL to, an ANCESTOR of, or a DESCENDANT of a declared
// path:
//
//   - equal is the direct read.
//   - ancestor covers every read that reaches a whole object and therefore
//     reaches each withheld leaf under it: `range .values.creds` references
//     .values.creds, and `index .values $k` references .values. This is what
//     makes a dynamic key and a rebound dot conservative rather than invisible.
//   - descendant covers a declaration on an intermediate object, which already
//     withholds its whole subtree where the pillar is displayed, so a read of one
//     leaf beneath it must withhold too.
//
// Withholding is TRANSITIVE across locals. Entries resolve in author order and
// each may read an earlier sibling, so a local reading an already-withheld local
// is withheld itself; the walk follows the same author order and carries the set
// forward, which is why one pass suffices.
//
// Where a reference cannot be resolved statically the output is withheld. That is
// a deliberate choice of direction, not an accident of implementation:
// over-withholding costs a reader one value they must find another way, while
// under-withholding prints a secret. A template whose text does not parse is
// treated the same way, for the same reason.
//
// declaredValues and declaredInfra are the host Component's declared leaf paths.
// With neither declaring anything, nothing can have been computed from a
// declaration and the zero value is returned.
func derivedSensitiveKeys(recipe *types.Recipe, declaredValues, declaredInfra map[string]bool) redact.Derived {
	if recipe == nil || (len(declaredValues) == 0 && len(declaredInfra) == 0) {
		return redact.Derived{}
	}

	withheld := map[string]bool{}
	for key, tmplText := range recipe.Spec.Locals.All() {
		if templateReadsWithheld(tmplText, declaredValues, declaredInfra, withheld) {
			withheld[key] = true
		}
	}

	// Which parts of the source carry a template is asked of the one walk that
	// classifies a source's shape — the same walk the evaluation goes through — so
	// a shape the evaluator learns to evaluate is a shape this reading covers
	// without a second edit. Nothing is replaced here: the walk's rebuilt value is
	// discarded and each template's text is returned unchanged, which is what makes
	// this a read of the same classification rather than a second evaluation.
	source := map[string]bool{}
	_, _ = mapRecipeSourceTemplates(recipe.Spec.Source, func(field, tmplText string) (string, error) {
		if templateReadsWithheld(tmplText, declaredValues, declaredInfra, withheld) {
			source[field] = true
		}
		return tmplText, nil
	})

	derived := redact.Derived{}
	if len(withheld) > 0 {
		derived.Locals = withheld
	}
	if len(source) > 0 {
		derived.Source = source
	}
	return derived
}

// templateReadsWithheld reports whether one template's references reach anything
// withheld. The reference set comes from the over-collecting census walk, which
// is the direction this question needs: a read hidden inside a rebound range or
// with body, or inside a chained defensive wrapper, has still happened, and a
// walk that attributed only root-anchored reads would answer "not withheld" for
// exactly the shapes that most need the conservative answer.
//
// A template with no actions reads nothing and short-circuits before the parse. A
// template that fails to parse yields no reference set, and the unresolvable case
// is withheld.
func templateReadsWithheld(tmplText string, declaredValues, declaredInfra, withheldLocals map[string]bool) bool {
	if tmplText == "" || !strings.Contains(tmplText, "{{") {
		return false
	}
	refs, err := CollectTemplateFieldAccesses(tmplText)
	if err != nil {
		return true
	}
	for _, ref := range refs {
		if referenceReachesWithheld(ref, declaredValues, declaredInfra, withheldLocals) {
			return true
		}
	}
	return false
}

// referenceReachesWithheld decides one reference against the withheld sets. The
// reference is the census spelling — a leading "." then the pillar root then the
// remaining segments — and only the three pillars a locals-or-source surface can
// read are consulted; every other root names data no declaration covers.
func referenceReachesWithheld(ref string, declaredValues, declaredInfra, withheldLocals map[string]bool) bool {
	root, rest := splitPillarReference(ref)
	switch root {
	case "values":
		return pathReachesWithheld(rest, declaredValues)
	case "infra":
		return pathReachesWithheld(rest, declaredInfra)
	case localsContextRoot:
		if rest == "" {
			// A read of the whole pillar reaches every entry in it.
			return len(withheldLocals) > 0
		}
		return withheldLocals[firstPathSegment(rest)]
	}
	return false
}

// splitPillarReference splits a census reference into its pillar root and the
// path below that root. rest is empty for a whole-pillar reference.
func splitPillarReference(ref string) (root, rest string) {
	trimmed := strings.TrimPrefix(ref, ".")
	if i := strings.Index(trimmed, "."); i >= 0 {
		return trimmed[:i], trimmed[i+1:]
	}
	return trimmed, ""
}

func firstPathSegment(path string) string {
	if i := strings.Index(path, "."); i >= 0 {
		return path[:i]
	}
	return path
}

// pathReachesWithheld reports whether one referenced path within a pillar is
// equal to, an ancestor of, or a descendant of any declared path in that pillar.
//
// The declared set is a set and is walked to a boolean, so the walk's order
// cannot reach any displayed byte.
func pathReachesWithheld(path string, declared map[string]bool) bool {
	if len(declared) == 0 {
		return false
	}
	if path == "" {
		// A read of the whole pillar reaches every declared leaf under it.
		return true
	}
	for d := range declared {
		if d == path ||
			strings.HasPrefix(d, path+".") ||
			strings.HasPrefix(path, d+".") {
			return true
		}
	}
	return false
}
