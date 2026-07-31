// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import "strings"

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

	sealed()
}

// provenance is the one implementation of [Subject]'s behaviour. Every subject
// kind embeds it, so attribution and objection construction have a single home
// and a kind contributes only its traversal and its marker.
type provenance struct {
	file    string
	locator string
}

func (provenance) sealed() {}

func (p provenance) SourceFile() string { return p.file }

func (p provenance) Locator() string { return p.locator }

func (p provenance) Raise(sentinel error, detail ...string) Objection {
	o := Objection{sentinel: sentinel, file: p.file, locator: p.locator}
	if len(detail) > 0 {
		o.detail = make([]string, len(detail))
		copy(o.detail, detail)
	}
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
