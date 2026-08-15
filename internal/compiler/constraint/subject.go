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

func (p provenance) Raise(sentinel error, detail ...string) Objection {
	o := Objection{sentinel: sentinel, file: p.file, locator: p.locator}
	if len(detail) > 0 {
		o.detail = make([]string, len(detail))
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
// told two different things about which element the objection is about.
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
	// Assembled from a copy: appending onto the subject's own segments could
	// write into the array a sibling subject reads.
	within := make([]string, 0, len(p.segments)+1)
	within = append(within, p.segments...)
	o.locator = authoredLocator(append(within, leaf)...)
	return o
}

// authoredLocator joins segments the author actually typed into the dotted
// locator dialect, and is the only place a locator is assembled — so a
// traversal contributes segments and never a format.
//
// The dialect is a leading "." followed by dot-separated segments, matching
// what a consumer recovering a position splits on when it walks the authored
// document.
//
// It returns the empty string when no authored segment remains, which is the
// mechanical form of not inventing a locator: a container the compiler
// synthesized contributes no segment, so a subject reached only through
// synthesized containers has nothing to name and its locator stays empty
// rather than naming a path the source file does not contain. An empty segment
// is dropped for the same reason.
//
// Three hand-assembled locator builders exist elsewhere in the compiler today,
// each raising for one violation class. They are the shape this replaces, and
// they are left in place deliberately until the checks that use them are
// written against this framework: unifying them ahead of that would leave the
// framework unexercised and the call sites still hand-assembling.
func authoredLocator(segments ...string) string {
	authored := make([]string, 0, len(segments))
	for _, s := range segments {
		if s == "" {
			continue
		}
		authored = append(authored, s)
	}
	if len(authored) == 0 {
		return ""
	}
	return "." + strings.Join(authored, ".")
}
