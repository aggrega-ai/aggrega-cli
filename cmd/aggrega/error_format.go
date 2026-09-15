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
// shape (the bracketed head, source pointer, message, optional expression,
// optional pillar snapshot). When err neither is nor wraps a *CompilerError but
// still names a registered condition, it renders the same head with the short
// code alone above the error's own text; when it names neither, it falls back
// to err.Error() so non-CLI callers and unidentified errors retain their
// existing presentation.
//
// The bracketed head carries the category the failure was filed under and,
// when the chain names a registered condition, that condition's short code
// beside it. Which frames carry the short code is decided by
// diagnosticPosition below, not here. Either token may be absent — a chain
// naming no registered condition has no code, and a failure with no concern to
// route the reader to has no category — and an absent token is omitted rather
// than stood in for.
//
// So one head shape serves every diagnostic, and which of its tokens are
// present is a fact about the failure rather than about how it was built. A
// chain that crossed a categorising boundary and names a condition carries
// both; one that crossed such a boundary and names no condition carries the
// category alone; one that names a condition and crossed no such boundary
// carries the short code alone; one with neither carries no brackets at all
// and is its own message.
//
// Dispatch runs six detection branches in a fixed order, and that order is
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
//     Running first is only half of it: the join is looked for along the
//     chain, not on the outermost frame alone. Every branch below reaches
//     through a wrap, so a join under one plain wrapping frame would reach a
//     branch that matches one element and renders it as the whole failure —
//     first place in the order buys nothing against a test the wrap defeats.
//     The reach stops at the first frame a branch below claims, which is what
//     leaves a join an envelope or a decoration deliberately sits above to
//     the branch that renders the frame's own text with it.
//
//  2. A decoration next — a failure carrying added context that is not itself
//     a diagnostic. It renders the failure it leads with and appends its own
//     text. This runs ahead of the envelope because the envelope branch
//     renders from the outermost envelope it can reach and discards every
//     frame above it, so a decoration detected after it would have its text
//     silently dropped for exactly those failures that carry an envelope.
//
//  3. The envelope next. An error that is, or wraps, a *cerrors.CompilerError
//     renders through the chain walk and returns.
//
//  4. The structured domain types next. Each is detected with errors.As; the
//     first match is converted through the type's ToCompilerError and
//     re-entered here. That recursive call lands on the envelope branch and
//     renders.
//
//  5. The trust and signing family next. It has no domain type to convert
//     from, so the chain is matched against the conditions themselves and the
//     envelope is built with the category fixed outright — the same shape the
//     acquisition family's sentinel match takes. It runs after every branch
//     above precisely because matching a whole family of conditions is a
//     broader test than an errors.As on one concrete type: run earlier it
//     would claim failures a later branch answers better, so run last among the
//     categorising branches it can only fill what would otherwise have fallen
//     through.
//
//  6. A chain that names a registered condition and crossed no categorising
//     boundary at all, last. It has no category and never will — nothing on
//     that path derived one from an entity kind and no boundary fixed one — but
//     it does name a condition, so the head carries the short code alone. This
//     runs after every branch above for the same reason the one above it does,
//     and it is the widest test of all: it asks only whether the chain names
//     any registered condition, so anywhere earlier it would claim failures
//     every other branch answers with a category as well.
//
// Falling through every branch returns the error's own Error output. That
// remains the presentation for a failure that names no registered condition
// either, and it is deliberately still reachable: a command that refuses its
// arguments, a file it cannot read, a cancellation, a library fault carrying no
// registered condition of ours. Removing it to force conversion would turn every
// such failure into a worse diagnostic rather than a better one — an empty head
// asserts that a category was expected and is missing, which is a different and
// wrong claim.
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
	return formatCompilerErrorAt(err, leadingDiagnostic)
}

// diagnosticPosition says where a rendered diagnostic sits in the output, and
// it decides exactly one thing: whether the bracketed head carries the
// condition's short code.
//
// The short code names the condition once, at the point the reader meets the
// failure. Repeating it on every frame beneath a "caused by" marker would add
// lines of the same fact — the frames below a marker are the same failure
// seen from further in, not further failures — so a continuation prints the
// category alone, exactly as it did before short codes existed.
type diagnosticPosition int

const (
	// leadingDiagnostic is a diagnostic the reader scans from the left
	// margin: the command's failure, or one element of a top-level
	// errors.Join, each of which is separated by the "---" rule and is a
	// failure in its own right rather than a detail of its neighbour.
	leadingDiagnostic diagnosticPosition = iota
	// continuingDiagnostic is anything rendered beneath a "caused by:" or
	// "caused by (N):" marker.
	continuingDiagnostic
)

// formatCompilerErrorAt is formatCompilerError with the position the result
// will occupy. It is the recursion's real entry point: every branch passes the
// position through, so a chain reached from beneath a "caused by" marker keeps
// rendering as a continuation however many conversions and forks it crosses.
func formatCompilerErrorAt(err error, pos diagnosticPosition) string {
	if err == nil {
		return ""
	}
	// Multi-error chains FIRST — see the branch order documented above.
	// Recursion is safe: each sub-error re-enters and reaches a terminal
	// branch, and a sub-error that is itself a multi-error is handled by the
	// same recursive call.
	//
	// Each element inherits this call's position rather than being forced to
	// one: a join reached at the top level is a list of independent failures,
	// each of which leads its own diagnostic, while a join reached beneath a
	// "caused by (N):" marker is a list of details and stays a continuation.
	//
	// The join is looked for ALONG THE CHAIN rather than on the outermost frame
	// alone, and that reach is what makes this branch's precedence real. Every
	// branch below reaches through a wrap; a test that did not would hand a
	// wrapped join to the first branch that does, and each of those matches ONE
	// element and renders it as the whole failure — so a report of several
	// findings arrives as a report of the first, with the rest dropped silently.
	//
	// The walk stops at the first frame a branch below claims, so a join that a
	// decoration or an envelope deliberately sits above stays that frame's
	// detail: the branch claiming the frame renders the frame's own text and the
	// join beneath it together. See firstRenderableFrame.
	if frame, kind := firstRenderableFrame(err); kind == frameMulti {
		parts, _ := joinedErrors(frame)
		if len(parts) > 0 {
			rendered := make([]string, 0, len(parts))
			for _, sub := range parts {
				if s := formatCompilerErrorAt(sub, pos); s != "" {
					rendered = append(rendered, s)
				}
			}
			if len(rendered) > 0 {
				return strings.Join(rendered, "\n---\n")
			}
		}
	}
	// A skip-context decoration SECOND, ahead of the envelope, and the position
	// is load-bearing rather than incidental. The decoration is not a diagnostic
	// of its own: it is a pair of diagnostics — a failure, and the catalog files
	// the tolerant load refused — with the decoration deciding which of the two
	// leads. So it renders the leading one in whatever shape that diagnostic's
	// own type calls for, then appends its note.
	//
	// It has to be detected BEFORE the envelope because the envelope branch
	// renders from the outermost envelope errors.As can reach and discards every
	// frame above it. Detected after, the note would be discarded exactly when
	// the decorated failure carries an envelope — the case where the note is
	// often the only text naming what the author wrote wrong.
	//
	// Recursion terminates for a different reason from the conversion branches
	// below: this branch re-enters on a failure the decoration carries rather
	// than on the decoration, and neither of the two carries a decoration of its
	// own, so the chain the recursion sees is strictly shorter each time.
	//
	// Text a frame ABOVE the decoration contributed — the label naming which
	// step of the command was running, say — is carried across explicitly.
	// Recursing on the leading failure alone would drop it, and that label is
	// what tells the reader which step failed.
	var sce *skipContextError
	if stderrors.As(err, &sce) && sce != nil {
		// The decoration renders whichever of the two failures the reader is
		// meant to meet first. Where it singled out the refusal that explains
		// the failure, that refusal leads and carries its own head — its own
		// category and its own short code — because it is the condition the
		// author can act on; the failure it explains is inside the note. Where
		// it could not, the decorated failure leads exactly as it would with no
		// record at all.
		lead := sce.err
		if sce.promoted != nil {
			lead = sce.promoted
		}
		rendered := formatCompilerErrorAt(lead, pos)
		if prefix := strings.TrimSuffix(err.Error(), sce.Error()); prefix != err.Error() {
			rendered = prefix + rendered
		}
		return rendered + sce.note
	}
	// The envelope THIRD, and every structured-type branch below it. The
	// ordering rule and why breaking it does not terminate are documented
	// above; a branch added out of order compiles and passes its own test.
	var ce *cerrors.CompilerError
	if stderrors.As(err, &ce) && ce != nil {
		return renderCompilerErrorChain(ce, headShortCode(err, pos))
	}
	var re *resolver.ResolverError
	if stderrors.As(err, &re) && re != nil {
		return formatCompilerErrorAt(re.ToCompilerError(), pos)
	}
	var wve *validator.WorkloadValidationError
	if stderrors.As(err, &wve) && wve != nil {
		return formatCompilerErrorAt(wve.ToCompilerError(), pos)
	}
	var ve *validator.ValidationError
	if stderrors.As(err, &ve) && ve != nil {
		return formatCompilerErrorAt(ve.ToCompilerError(), pos)
	}
	var ae *analyzer.AnalysisError
	if stderrors.As(err, &ae) && ae != nil {
		return formatCompilerErrorAt(ae.ToCompilerError(), pos)
	}
	// The trust and signing family LAST among the conversions, and the position
	// is a decision rather than an accident. These failures have no domain type
	// to convert from, so they are recognised by matching the chain against the
	// conditions themselves — which is a broader test than an errors.As on one
	// concrete type, and would therefore claim a failure another branch has a
	// better answer for. Running it after every branch above means it fires only
	// on a chain that reached the fallback, so nothing that already prints a head
	// changes: an envelope keeps the category its own boundary chose, and a domain
	// type keeps the category its authoring surface derives.
	if te, ok := trustFailureEnvelope(err); ok {
		return renderCompilerErrorChain(te, headShortCode(err, pos))
	}
	// A chain that names a registered condition and reached here LAST, and it is
	// the widest test in the dispatch: every branch above asks what a failure IS
	// — a join, a decoration, an envelope, a domain type, a member of one family
	// — while this asks only whether the chain names any condition at all. So it
	// can sit nowhere else: anywhere earlier it would claim failures those
	// branches answer with a category as well as a code.
	//
	// What reaches it is a failure that crossed no categorising boundary. That is
	// not a gap to be filled with a plausible category, and the head is composed
	// so that it does not have to be: an absent token is omitted rather than
	// stood in for, so what the reader meets is the short code alone. Guessing
	// the category instead would put a wrong answer in the token that says which
	// surface to go and fix, which is the part of a diagnostic a reader acts on
	// first.
	//
	// The guard is the code rather than a type test, and it is what keeps this
	// branch off everything else: a chain naming no condition has nothing for a
	// head to carry, so it falls to the line below and renders exactly as it
	// always did. A continuation is covered by the same guard, since the position
	// is what decides whether a short code is read at all.
	if code := headShortCode(err, pos); code != "" {
		return renderCompilerError(conditionOnlyEnvelope(err), code)
	}
	return err.Error()
}

// conditionOnlyEnvelope builds the rendering-only envelope a failure that names
// a registered condition and carries no category is rendered from.
//
// It is the trust family's envelope one step further out, and it is built the
// same way for the same reasons. The message is err.Error(), which for a wrapped
// chain is already the whole flattened text — exactly the string the
// uncategorised fallback prints — so the envelope changes what the reader sees
// in one respect only: a bracketed head appears above text that is otherwise
// unchanged. It carries no wrap chain because it has nothing left to say; its
// message already holds every frame's contribution, and the short code is read
// off the ORIGINAL error rather than off this envelope, so dropping the chain
// costs the head nothing.
//
// The category is left absent deliberately, and it is the absent value rather
// than an unfilled one. A failure reaching this point crossed no boundary that
// derives a category from an entity kind and none that fixes one outright, so
// there is no concern to route the reader to; the empty Category is exactly how
// that is said. See the Category type, which documents the absence as a state.
//
// It exists for the duration of one render and is never returned to a caller, so
// errors.Is, errors.As and the chain any command returns are untouched.
func conditionOnlyEnvelope(err error) *cerrors.CompilerError {
	return cerrors.Unidentified("", cerrors.SourceLocation{}, err.Error(), nil, nil)
}

// headShortCode returns the short code the head of a diagnostic rendered at
// pos should carry, or the empty string when it carries none.
//
// Two reasons it can be empty, and the rendering treats them alike because the
// reader cannot act on the difference: the diagnostic is a continuation, or the
// chain names no registered condition. The second is a fact about the chain
// rather than a failure, so the head prints whatever else it has rather than a
// placeholder standing in for an identifier that does not exist.
//
// It reads the whole error rather than the envelope alone. The condition's
// identity usually sits below the envelope, but a caller may also wrap the
// envelope in it, and the chain walk in ShortCodeOf reaches both — so the code
// printed is the one the chain names, wherever in the chain it was raised.
//
// A code a PLUGIN raised is printed qualified by the plugin that raised it —
// `<plugin>/E0241` rather than `E0241` — because a code is unique only within
// whoever allocated it. The compiler's own registry refuses a duplicate, so its
// codes need no qualifier; nothing coordinates two plugin authors, so two guests
// can publish one code for two unrelated conditions and a reader meeting a bare
// one could not tell which they had. The compiler's own conditions report no
// plugin and are therefore unchanged, which is why this needs no test of where
// the failure came from beyond the read itself.
func headShortCode(err error, pos diagnosticPosition) string {
	if pos != leadingDiagnostic {
		return ""
	}
	code, _ := cerrors.ShortCodeOf(err)
	return qualifyByRaisingPlugin(err, code)
}

// qualifyByRaisingPlugin joins the plugin that raised a condition to that
// condition's short code, and returns code unchanged where no plugin raised it or
// where there is no code to qualify.
//
// The two tokens are joined by a solidus, and the code is the LAST segment. That
// ordering is what makes the pair readable when the identity itself carries
// separators — a plugin's identity coordinate is already `<type>/<name>`, and an
// artifact-addressed one carries more — because the code's own shape is fixed at
// the letter E and four digits, so the segment after the final solidus is always
// the code and never part of the identity. Putting the code first and separating
// the two some other way was the alternative, and it fails on exactly those
// identities: every candidate separator is a character an addressed identity may
// itself contain, and none of them has the fixed trailing shape to fall back on.
//
// An empty code is returned untouched rather than yielding a bare `<plugin>/`.
// That case does not arise from a guest failure the host recognised — the plugin
// is recorded on the same value the code is, and only where both identifiers were
// reported — but the head is composed from two independent chain reads, so the
// guard is what keeps a future third reader from printing a dangling separator.
func qualifyByRaisingPlugin(err error, code string) string {
	if code == "" {
		return code
	}
	plugin, ok := cerrors.RaisingPluginOf(err)
	if !ok || plugin == "" {
		return code
	}
	return plugin + "/" + code
}

// renderCompilerErrorChain renders the head envelope in the documented
// shape, then walks its wrap chain so every nested *CompilerError frame
// reaches the terminal: each nested envelope renders as an indented
// "caused by:" continuation carrying its own category tag, source
// pointer, and message — the innermost file-pointed frame is always
// visible, and a phase-context wrap can no longer swallow the specific
// diagnostic beneath it.
//
// shortCode is what the head's bracketed tag carries beside the category, or
// empty for none; the caller has already decided it from the diagnostic's
// position. Every frame this function renders below the head is a
// continuation, so each is rendered with no short code — including the joined
// sub-errors under a "caused by (N):" header, which re-enter the full
// formatting recursion at the continuing position for that reason.
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
func renderCompilerErrorChain(head *cerrors.CompilerError, shortCode string) string {
	if head == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(renderCompilerError(head, shortCode))
	prev := head
	depth := 0
	link := head.Unwrap()
	for link != nil {
		if subs, isMulti := joinedErrors(link); isMulti {
			rendered := make([]string, 0, len(subs))
			for _, sub := range subs {
				if s := formatCompilerErrorAt(sub, continuingDiagnostic); s != "" {
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
				b.WriteString(indentCausedBy(renderCompilerError(ce, ""), depth))
			}
			prev = ce
			link = ce.Unwrap()
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

// joinedErrors returns the errors a frame carries when it carries several at
// once, and reports whether it does.
//
// It is the one place the multi-error shape is recognised. The shape is a
// method returning a slice rather than a single error, and every reader here
// asks for it through this function rather than spelling the assertion out
// again. Two spellings of the question agree on every chain carrying no
// wrapping frame, so a reader asking it of the outermost frame alone and a
// reader asking it of a whole chain are indistinguishable until a wrap
// appears — at which point the narrower one hands a wrapped join to a branch
// that matches a single element and reports it as the whole failure. The
// agreement is what makes a second spelling worth removing rather than
// keeping in step.
func joinedErrors(err error) ([]error, bool) {
	multi, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return nil, false
	}
	return multi.Unwrap(), true
}

// frameKind says what one frame of an error chain is, in the only terms the
// dispatch above cares about: whether some branch renders that frame on terms
// of its own, and if so which kind of branch.
//
// It exists so that a walk which passes through frames can tell the frames it
// may pass through from the frames it may not. Passing through a frame no
// branch claims loses nothing, because nothing below would have rendered it
// differently. Passing through a frame a branch does claim loses exactly what
// that branch would have said.
type frameKind int

const (
	// frameOpaque is a frame no branch claims — a wrap adding context, or any
	// error this file renders no differently from the text it already carries.
	frameOpaque frameKind = iota
	// frameMulti is a frame carrying several errors at once.
	frameMulti
	// frameEnvelope is a *cerrors.CompilerError.
	frameEnvelope
	// frameClaimed is a frame one of the branches below the multi-error branch
	// renders on terms of its own: the decoration, and each structured domain
	// type. A walk looking for something further down must stop at one of
	// these rather than pass through it, because the text it contributes is
	// reachable only through the branch that claims it.
	frameClaimed
)

// classifyFrame reports which kind the single frame err is, looking at that
// frame alone and never at what it wraps.
//
// The claimed set mirrors the detection branches above one for one, and that
// correspondence is the whole point of collecting it here: a branch added above
// without a kind added here would be a branch a walk is free to step over.
func classifyFrame(err error) frameKind {
	if _, ok := joinedErrors(err); ok {
		return frameMulti
	}
	if _, ok := err.(*cerrors.CompilerError); ok {
		return frameEnvelope
	}
	switch err.(type) {
	case *skipContextError,
		*resolver.ResolverError,
		*validator.WorkloadValidationError,
		*validator.ValidationError,
		*analyzer.AnalysisError:
		return frameClaimed
	}
	return frameOpaque
}

// firstRenderableFrame walks the single-Unwrap spine rooted at err and returns
// the first frame some branch claims, together with the kind it was classified
// as. It reports frameOpaque and a nil frame when the spine reaches no such
// frame at all.
//
// It answers "what does this chain lead with, once the wraps above it are
// discounted" — which is the question the multi-error branch has to ask,
// because every branch beneath it reaches through a wrap and would otherwise
// answer first. Stopping at the first claimed frame is what keeps the answer
// honest: a join sitting beneath an envelope or beneath a decoration is that
// frame's detail rather than the failure the reader leads with, and the branch
// claiming the frame renders both the frame's own text and the join beneath it.
//
// It is not errors.As, which searches the whole error tree including the
// branches of a multi-error, and so would report a frame reachable only by
// descending into one of several joined elements — a frame that is one
// finding's detail rather than anything the chain leads with.
func firstRenderableFrame(err error) (error, frameKind) {
	for cur := err; cur != nil; cur = stderrors.Unwrap(cur) {
		if kind := classifyFrame(cur); kind != frameOpaque {
			return cur, kind
		}
	}
	return nil, frameOpaque
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
//
// It asks a DIFFERENT question from firstRenderableFrame and therefore keeps
// its own walk, sharing only the per-frame classifier. This walk renders
// envelopes and joins and nothing else, so a claimed frame is not a stopping
// point for it: it steps over one and keeps looking, exactly as it would over
// any other frame it cannot render. Collapsing the two walks into one answer
// would have a claimed frame report itself renderable to a walk that cannot
// render it, and the terminal link — the one carrying the flattened tail —
// would then be skipped and its text lost.
func hasRenderableFrame(err error) bool {
	for cur := err; cur != nil; cur = stderrors.Unwrap(cur) {
		switch classifyFrame(cur) {
		case frameEnvelope, frameMulti:
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
	if next.Expression() != "" || len(next.Context()) > 0 {
		return false
	}
	return next.Category() == prev.Category() &&
		next.Source().String() == prev.Source().String() &&
		strings.Contains(prev.Message(), next.Message())
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
// multi-line shape: the bracketed head, source pointer, message, optional
// expression, optional pillar snapshot. Extracted from the
// formatCompilerError detection-and-render combo so the rendering stays
// pure (no chain-walking) and the caller controls when rendering fires.
//
// The bracketed head holds whichever of the two tokens the diagnostic has: the
// category, and the condition's short code. Deciding whether the short code
// should be there is the caller's job, not this function's — what to print
// depends on where the result will sit and on what the whole chain names, and
// this function sees neither.
//
// Either token can be absent, and an absent one is omitted rather than stood
// in for. A short code is absent when the chain names no registered condition;
// a category is absent when there is no concern to route the reader to — which
// is not the same as the failure being no fault of an authored artifact, since
// several categories name a concern rather than an authoring surface and are
// printed for exactly such failures. With both absent the brackets are omitted
// too, because empty brackets assert that a head was expected and is missing,
// which is a different thing from a head with nothing to say.
//
// The tokens are joined rather than formatted into a fixed shape, so a token
// added beside them needs no new arm here and no count to keep in step.
func renderCompilerError(ce *cerrors.CompilerError, shortCode string) string {
	if ce == nil {
		return ""
	}
	var b strings.Builder
	tokens := make([]string, 0, 2)
	if ce.Category() != "" {
		tokens = append(tokens, string(ce.Category()))
	}
	if shortCode != "" {
		tokens = append(tokens, shortCode)
	}
	if len(tokens) > 0 {
		fmt.Fprintf(&b, "[%s]", strings.Join(tokens, " "))
	}
	if src := ce.Source().String(); src != "" {
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		b.WriteString(src)
	}
	if ce.Message() != "" {
		if b.Len() > 0 {
			b.WriteString("\n  ")
		}
		b.WriteString(ce.Message())
	}
	if ce.Expression() != "" {
		fmt.Fprintf(&b, "\n  expression: %s", ce.Expression())
	}
	if pillars := ce.Context(); len(pillars) > 0 {
		fmt.Fprintf(&b, "\n  active pillars:")
		keys := make([]string, 0, len(pillars))
		for k := range pillars {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "\n    %s", k)
		}
	}
	return b.String()
}
