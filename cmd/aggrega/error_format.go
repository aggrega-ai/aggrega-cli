// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	stderrors "errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// formatCompilerError renders a CompilerError in the documented multi-line
// shape (category tag in brackets, source pointer, message, optional
// expression, optional pillar snapshot). When err is not (or does not wrap)
// a *CompilerError, falls back to err.Error() so non-CLI callers and
// non-categorised errors retain their existing presentation.
//
// Dispatch runs three detection branches in a fixed order, and that order is
// part of the contract rather than an implementation detail:
//
//  1. Multi-error first. A chain built by errors.Join exposes
//     Unwrap() []error; every element is rendered on its own and the results
//     joined by a line carrying only "---". Readers split on that separator
//     to enumerate the joined errors, and each element keeps its own category
//     tag because each is rendered independently. This runs first because
//     errors.As matches only the first matching element, so a join of several
//     envelopes would otherwise render one and silently drop the rest.
//
//  2. The envelope next. An error that is, or wraps, a *cerrors.CompilerError
//     renders through the chain walk and returns.
//
//  3. The structured domain types last. Each is detected with errors.As; the
//     first match is converted through the type's ToCompilerError and
//     re-entered here. That recursive call lands on the envelope branch and
//     renders. Falling through every branch returns the error's own Error
//     output, which is the presentation for errors that have not adopted the
//     conversion pattern.
//
// A new domain type's detection branch goes AFTER the envelope branch, never
// before, and the reason is recursion termination rather than taste. A
// converted envelope still carries the domain-typed error in its wrap chain,
// so errors.As against that domain type keeps matching once the conversion has
// happened. Were a domain branch to run first, it would convert an
// already-converted error again, and the resulting envelope would again carry
// the domain type in its chain, with nothing differing between iterations to
// stop it. Detecting the envelope first ends the walk there, and leaves the
// domain branches firing only on a chain that carries no envelope yet — which
// is what makes conversion an entry path rather than a re-entry path.
//
// Adding a domain type to the pattern is four steps: declare ToCompilerError
// on the type with the Category its authoring surface implies; confirm the
// type's own Error output still carries the diagnostic the user needs, since
// the envelope's message is usually built out of it; add the detection branch
// here, below the envelope branch; and cover it with a test asserting both
// that errors.Is against the type's sentinel still matches and that the
// rendered output carries the expected category tag.
func formatCompilerError(err error) string {
	if err == nil {
		return ""
	}
	// Multi-error chains FIRST — see the branch order documented above.
	// Recursion is safe: each sub-error re-enters and reaches a terminal
	// branch, and a sub-error that is itself a multi-error is handled by the
	// same recursive call.
	type multiErr interface{ Unwrap() []error }
	if me, ok := err.(multiErr); ok {
		parts := me.Unwrap()
		if len(parts) > 0 {
			rendered := make([]string, 0, len(parts))
			for _, sub := range parts {
				if s := formatCompilerError(sub); s != "" {
					rendered = append(rendered, s)
				}
			}
			if len(rendered) > 0 {
				return strings.Join(rendered, "\n---\n")
			}
		}
	}
	// The envelope SECOND, and every structured-type branch below it. The
	// ordering rule and why breaking it does not terminate are documented
	// above; a branch added out of order compiles and passes its own test.
	var ce *cerrors.CompilerError
	if stderrors.As(err, &ce) && ce != nil {
		return renderCompilerErrorChain(ce)
	}
	var re *resolver.ResolverError
	if stderrors.As(err, &re) && re != nil {
		return formatCompilerError(re.ToCompilerError())
	}
	var rve *validator.RecipeValidationError
	if stderrors.As(err, &rve) && rve != nil {
		return formatCompilerError(rve.ToCompilerError())
	}
	var wve *validator.WorkloadValidationError
	if stderrors.As(err, &wve) && wve != nil {
		return formatCompilerError(wve.ToCompilerError())
	}
	var ve *validator.ValidationError
	if stderrors.As(err, &ve) && ve != nil {
		return formatCompilerError(ve.ToCompilerError())
	}
	var ae *analyzer.AnalysisError
	if stderrors.As(err, &ae) && ae != nil {
		return formatCompilerError(ae.ToCompilerError())
	}
	return err.Error()
}

// renderCompilerErrorChain renders the head envelope in the documented
// shape, then walks its wrap chain so every nested *CompilerError frame
// reaches the terminal: each nested envelope renders as an indented
// "caused by:" continuation carrying its own category tag, source
// pointer, and message — the innermost file-pointed frame is always
// visible, and a phase-context wrap can no longer swallow the specific
// diagnostic beneath it.
//
// Walk rules:
//   - A nested envelope adding nothing beyond the previously rendered
//     frame (same category, same source, message equal to or contained
//     in the previous frame's message) is deduplicated, not repeated.
//   - A non-envelope link (an fmt wrap) is traversed without rendering
//     for as long as the chain beneath it still reaches a frame this walk
//     renders on its own terms — another envelope, or a multi-error fork.
//     Once it does not, that link is the OUTERMOST non-envelope frame and
//     it renders as a final continuation, because fmt wrapping means its
//     Error() already carries the whole flattened tail beneath it in one
//     string. Rendering it recovers every wrap frame's contribution — an
//     assertion's name, a fail message, the sentinel's own explanation —
//     without repeating the tail once per level. It renders only when its
//     text is not already part of the output, so a frame the envelopes
//     above already stated is not said twice.
//   - A multi-error link (errors.Join nested under an envelope) renders
//     every joined sub-error: a "caused by (N):" header, then each
//     sub-error through the full formatCompilerError recursion (its own
//     frame walk, multi-error walk, and conversion branches), separated
//     by the same "---" rule the top-level Join rendering uses. The walk
//     ends at the link — the chain forks there and each sub-chain owns
//     its own tail. Recursion terminates because sub-errors are
//     independent chains re-entering branches that terminate.
//
// Rendering is read-only on the chain — errors.Is / errors.As behavior
// is untouched.
func renderCompilerErrorChain(head *cerrors.CompilerError) string {
	if head == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(renderCompilerError(head))
	prev := head
	depth := 0
	link := head.Wrap
	for link != nil {
		if me, isMulti := link.(interface{ Unwrap() []error }); isMulti {
			subs := me.Unwrap()
			rendered := make([]string, 0, len(subs))
			for _, sub := range subs {
				if s := formatCompilerError(sub); s != "" {
					rendered = append(rendered, s)
				}
			}
			if len(rendered) > 0 {
				pad := strings.Repeat("  ", depth+1)
				fmt.Fprintf(&b, "\n%scaused by (%d):", pad, len(rendered))
				for i, block := range rendered {
					if i > 0 {
						fmt.Fprintf(&b, "\n%s---", pad)
					}
					b.WriteString(indentBlock(block, depth+2))
				}
			}
			break
		}
		if ce, ok := link.(*cerrors.CompilerError); ok {
			if ce == nil {
				break
			}
			if !frameRedundant(prev, ce) {
				depth++
				b.WriteString(indentCausedBy(renderCompilerError(ce), depth))
			}
			prev = ce
			link = ce.Wrap
			continue
		}
		next := stderrors.Unwrap(link)
		if !hasRenderableFrame(next) {
			if txt := link.Error(); txt != "" && !strings.Contains(b.String(), txt) {
				b.WriteString(indentCausedBy(txt, depth+1))
			}
			break
		}
		link = next
	}
	return b.String()
}

// hasRenderableFrame reports whether the chain rooted at err still reaches
// a frame the chain walk renders on its own terms: a *CompilerError
// envelope, or a multi-error fork. It decides whether a non-envelope link
// may be traversed silently. While the answer is yes, traversing loses
// nothing — a frame further down will speak. Once it is no, the current
// link is the last thing carrying information and must render, since
// nothing beneath it will.
//
// The scan follows single-Unwrap links only, deliberately mirroring the
// spine renderCompilerErrorChain itself walks. It is not errors.As, which
// searches the whole error tree including multi-error branches and so would
// answer a different question than "will this walk reach another frame".
func hasRenderableFrame(err error) bool {
	for cur := err; cur != nil; cur = stderrors.Unwrap(cur) {
		if _, ok := cur.(*cerrors.CompilerError); ok {
			return true
		}
		if _, ok := cur.(interface{ Unwrap() []error }); ok {
			return true
		}
	}
	return false
}

// frameRedundant reports whether a nested envelope adds no information
// beyond the previously rendered frame: same category, same source
// pointer, and a message the previous frame's message already contains
// (equality included). Expression or pillar context on the nested frame
// always counts as new information.
func frameRedundant(prev, next *cerrors.CompilerError) bool {
	if next.Expression != "" || len(next.Context) > 0 {
		return false
	}
	return next.Category == prev.Category &&
		next.Source.String() == prev.Source.String() &&
		strings.Contains(prev.Message, next.Message)
}

// indentBlock indents every line of a rendered block per nesting depth,
// each on its own line — the layout for the sub-error blocks beneath a
// "caused by (N):" header.
func indentBlock(block string, depth int) string {
	pad := strings.Repeat("  ", depth)
	var b strings.Builder
	for _, ln := range strings.Split(block, "\n") {
		b.WriteString("\n")
		b.WriteString(pad)
		b.WriteString(ln)
	}
	return b.String()
}

// indentCausedBy lays a rendered frame (or terminal-error text) out as a
// "caused by:" continuation: every line indented per nesting depth, the
// first line prefixed with the continuation marker.
func indentCausedBy(block string, depth int) string {
	pad := strings.Repeat("  ", depth)
	var b strings.Builder
	for i, ln := range strings.Split(block, "\n") {
		b.WriteString("\n")
		b.WriteString(pad)
		if i == 0 {
			b.WriteString("caused by: ")
		}
		b.WriteString(ln)
	}
	return b.String()
}

// renderCompilerError formats a *CompilerError per the documented
// multi-line shape: category tag in brackets, source pointer, message,
// optional expression, optional pillar snapshot. Extracted from the
// formatCompilerError detection-and-render combo so the rendering stays
// pure (no chain-walking) and the caller controls when rendering fires.
func renderCompilerError(ce *cerrors.CompilerError) string {
	if ce == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[%s]", ce.Category)
	if src := ce.Source.String(); src != "" {
		fmt.Fprintf(&b, " %s", src)
	}
	if ce.Message != "" {
		fmt.Fprintf(&b, "\n  %s", ce.Message)
	}
	if ce.Expression != "" {
		fmt.Fprintf(&b, "\n  expression: %s", ce.Expression)
	}
	if len(ce.Context) > 0 {
		fmt.Fprintf(&b, "\n  active pillars:")
		keys := make([]string, 0, len(ce.Context))
		for k := range ce.Context {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "\n    %s", k)
		}
	}
	return b.String()
}
