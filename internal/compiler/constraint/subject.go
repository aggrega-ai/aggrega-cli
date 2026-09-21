// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import (
	"slices"
	"strings"
)

// Subject is what a check is invoked once per, and the only source of an
// [Objection]. It answers where the thing it stands for was authored, and it
// raises objections about it.
//
// The unexported method is the seal: an unexported interface method is
// qualified by the package declaring it, so a type declared elsewhere cannot
// satisfy Subject however its own methods are named. This package is therefore
// the only place a subject is declared, and it supplies one only for the
// duration of a check invocation — the framework passes one in and never
// returns one.
//
// The seal's exact bound, including the one route it does not close and why
// that route is harmless, is in the package documentation. Read it before
// relying on the seal for anything beyond objection construction.
//
// A subject kind embeds Subject and adds its own unexported marker, which is
// what makes the kinds distinct at compile time: a check declaring one kind
// cannot be handed another.
type Subject interface {
	// SourceFile returns the workspace-relative file the subject was
	// authored in, empty when it has no single-file origin.
	SourceFile() string

	// Locator returns the dotted locator naming the authored element,
	// empty when none is derivable.
	Locator() string

	// Raise produces an Objection attributed to this subject, carrying
	// sentinel and the check's detail.
	Raise(sentinel error, detail ...string) Objection

	// RaiseWith is Raise for a rule supplying a value its registered text
	// does not name, so at least one of its detail values carries a
	// caption saying what it is. See [DetailValue].
	RaiseWith(sentinel error, detail ...DetailValue) Objection

	// RaiseWithAdvice is RaiseWith for a rule whose remedy is a property of
	// the occurrence rather than of the rule, so no registered sentence
	// could carry it. See [Objection.Advice].
	RaiseWithAdvice(sentinel error, advice string, detail ...DetailValue) Objection

	// RaiseAt produces an Objection attributed to the element leaf names
	// inside this subject, carrying sentinel, leaf as the first detail
	// value, and the check's remaining detail after it.
	RaiseAt(sentinel error, leaf string, detail ...string) Objection

	sealed()
}

// provenance is the one implementation of [Subject]'s behaviour. Every subject
// kind embeds it, so attribution and objection construction have a single home
// and a kind contributes only its traversal and its marker.
//
// The segments are kept alongside the locator they assemble because a raise
// positioned inside the subject has to assemble a longer locator, and it does
// that by handing a longer segment list to the same assembly the shorter one
// came from. Keeping the string alone would leave that raise to re-express the
// dialect's separator, which is the second assembly site authoredLocator exists
// to prevent.
type provenance struct {
	file     string
	segments []string
	locator  string
}

func (provenance) sealed() {}

func (p provenance) SourceFile() string { return p.file }

func (p provenance) Locator() string { return p.locator }

// newProvenance is the one place a subject's attribution is assembled: a source
// file, and the segments the author typed joined into the locator dialect. Every
// subject kind's constructor goes through it, so a traversal supplies segments
// and never a format.
func newProvenance(sourceFile string, segments ...string) provenance {
	return provenance{
		file: sourceFile,
		// Copied because a subject outlives the call that built its segments,
		// and a producer reusing its slice would otherwise change a locator
		// already handed out.
		segments: slices.Clone(segments),
		locator:  authoredLocator(segments...),
	}
}

// Raise, RaiseWith and RaiseWithAdvice are three signatures over one
// construction, and both parts of that are deliberate.
//
// The construction is single: each narrower form widens its arguments and hands
// them to the widest, so there is one place an objection's fields are populated
// and the forms cannot come to disagree about what raising means.
//
// The signatures stay several, which is the part a later reading is likely to want
// to "unify" — folding the narrow forms away so every rule wrapped its values and
// passed an empty remedy. The rendered message is identical either way, so no
// reported behaviour turns on the choice, and the great majority of rules raise
// exactly the one value their registered text names and have no remedy of their
// own: ceremony on those says nothing, at every call site, forever. So the narrow
// form is kept for the ordinary case, the captioning form is reached for when a
// rule has something to caption, and the advising form when a rule's remedy is a
// property of the occurrence rather than of the rule. Collapsing them is a change
// with a cost and no reader-visible benefit.
func (p provenance) Raise(sentinel error, detail ...string) Objection {
	values := make([]DetailValue, 0, len(detail))
	for _, d := range detail {
		values = append(values, Uncaptioned(d))
	}
	return p.RaiseWith(sentinel, values...)
}

func (p provenance) RaiseWith(sentinel error, detail ...DetailValue) Objection {
	return p.RaiseWithAdvice(sentinel, "", detail...)
}

// RaiseWithAdvice is the widest of the three, and it is the one construction the
// other two widen into — so an objection's fields are populated in exactly one
// place and no two forms can come to disagree about what raising means.
//
// Advice is a separate parameter rather than a further detail value because the
// two land in different parts of a composed message: a detail names a value the
// finding is about and is quoted as one, while advice is a sentence telling the
// author what to change. A rule that put its remedy in the detail would have it
// rendered as a quoted value among the values, which reads as one more thing the
// finding is about.
//
// It is reached for only by a rule whose remedy is a property of the occurrence.
// A rule whose registered sentence already says what to do supplies none, and the
// message it composes is unchanged by the parameter existing.
func (p provenance) RaiseWithAdvice(sentinel error, advice string, detail ...DetailValue) Objection {
	o := Objection{sentinel: sentinel, file: p.file, locator: p.locator, advice: advice}
	if len(detail) > 0 {
		o.detail = make([]DetailValue, len(detail))
		copy(o.detail, detail)
	}
	return o
}

// RaiseAt is the raise for a rule whose objection is about a named element
// inside the subject rather than about the subject as a whole.
//
// A rule measuring a payload against a contract objects about one key inside
// that payload, and a finding positioned at the payload's owner leaves the
// author to find which key it meant. leaf names that element relative to the
// subject, and may be several authored keys deep.
//
// Which rules use it is the rule author's statement and not a property the
// framework derives: calling it declares that leaf names an authored element.
// A rule whose leading value names something else — a declared type, a
// mechanism, a key some other declaration publishes — or names one this
// subject's own locator already ends with, calls [Subject.Raise] instead and
// keeps its finding at the subject. The framework cannot tell those apart,
// because every detail value is a string, which is why the declaration is the
// call rather than something derived from the value.
//
// leaf becomes the objection's first detail value as well as the last part of
// its locator, so a reader of the detail and a reader of the locator cannot be
// told two different things about which element the objection is about. The
// detail carries it whole, because that is the value the rule names; the locator
// carries it as one segment per key, because that is what a position is.
//
// leaf arrives already spelled in the locator's own dialect, so a rule reporting
// a key several levels down inside a payload hands over one dotted value rather
// than separate segments. It is split back into those segments here, which is
// what keeps the locator a path the authored document can be walked by. The cost
// is stated rather than hidden: a leaf that is itself ONE key containing the
// separator cannot be expressed through this call, because the joining already
// happened in the caller and nothing in the joined string records where the key
// boundaries were. A rule holding such a key names the element in its detail and
// positions at the owner, with [Subject.Raise].
//
// A subject with no authored locator gets none from this either. An element
// inside a site the source file does not name has nothing to be named relative
// to, and rooting leaf on nothing would name a path no file contains — the
// same reason the traversals drop a tail whose base is empty.
func (p provenance) RaiseAt(sentinel error, leaf string, detail ...string) Objection {
	o := p.Raise(sentinel, append([]string{leaf}, detail...)...)
	if p.locator == "" {
		return o
	}
	keys := strings.Split(leaf, locatorSeparator)
	// Assembled from a copy: appending onto the subject's own segments could
	// write into the array a sibling subject reads.
	within := make([]string, 0, len(p.segments)+len(keys))
	within = append(within, p.segments...)
	o.locator = authoredLocator(append(within, keys...)...)
	return o
}

// locatorSeparator is what the locator dialect separates its segments with.
//
// It is declared rather than spelled at each use because the join below and the
// addressability rule below it are two readings of one fact: the segment a join
// would render indistinguishable from two segments is exactly the segment that
// contains this.
//
// It is deliberately not shared with the separators [scopeNesting] carries. Those
// say how the compiler composed one axis's path, this says how a locator is
// spelled, and the two agreeing on a character today does not make them one fact
// — a change to either must be free to leave the other alone.
const locatorSeparator = "."

// authoredLocator joins segments the author actually typed into the dotted
// locator dialect, and is the only place a locator is assembled — so a
// traversal contributes segments and never a format.
//
// The dialect is a leading "." followed by dot-separated segments, matching
// what a consumer recovering a position splits on when it walks the authored
// document.
//
// # The guarantee: one locator has exactly one reading
//
// Splitting a locator on the separator yields exactly the segments it was built
// from, so the path it names is the path the authored document spells. Nothing
// else in the dialect distinguishes a segment boundary from a separator inside a
// segment, which is why the guarantee is enforced here rather than described: an
// authored key may itself contain the separator — an artifact's output name is a
// filename, a file mechanism delivers into a mount path, and a Release
// addresses a composed component by the qualified identifier the composition
// gave it — and joining such a key in would produce a string that reads equally
// as one key or as two.
//
// So a segment carrying the separator, and every segment after it, contributes
// nothing: the locator names the deepest site that is addressable and stops
// there. That trades depth for truth, and it is the cheaper side of the trade
// for two reasons. A consumer walking the authored document already stops at
// exactly that key, because the segment it would descend by is not a key the
// document holds — so the position a reader is taken to does not move. And a
// reader who is given the container is a reader who can find the entry inside
// it, while a reader given a path that resolves nowhere has to work out which of
// its segments was never a key.
//
// A locator is a position and not an identifier, which is why the dropped value
// is not spelled here in some escaped form. The element's own name reaches the
// reader as a coordinate the subject carries and as the detail a rule raises
// with — the same division [RunOnAssemblyPathProviders] draws for a value that
// is legitimate as a coordinate and refused as a segment.
//
// It returns the empty string when no authored segment remains, which is the
// mechanical form of not inventing a locator: a container the compiler
// synthesized contributes no segment, so a subject reached only through
// synthesized containers has nothing to name and its locator stays empty
// rather than naming a path the source file does not contain. An empty segment
// is dropped for the same reason — dropped and not stopped at, because an
// unauthored container has a nameable site beneath it while an unaddressable
// key does not.
//
// Hand-assembled locator builders exist elsewhere in the compiler today, each
// raising for one violation class. They are the shape this replaces, and they
// are left in place deliberately until the checks that use them are written
// against this framework: unifying them ahead of that would leave the framework
// unexercised and the call sites still hand-assembling. Their number is not
// stated, because it falls by one with each such check and a stated count would
// go stale while still reading as current.
func authoredLocator(segments ...string) string {
	authored := make([]string, 0, len(segments))
	for _, s := range segments {
		if s == "" {
			continue
		}
		if strings.Contains(s, locatorSeparator) {
			break
		}
		authored = append(authored, s)
	}
	if len(authored) == 0 {
		return ""
	}
	return locatorSeparator + strings.Join(authored, locatorSeparator)
}
