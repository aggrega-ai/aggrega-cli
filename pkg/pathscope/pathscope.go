// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package pathscope is the one home for deciding a path's scope. Two questions
// live here: does a path stay inside the directory it is relative to, and where
// does a path somebody chose for themselves land.
//
// Containment is the first question. A path that escapes its root is a defect
// wherever it appears: a rendered artifact name that writes outside the output
// directory, an archive entry that unpacks over a neighbouring tree, a plugin
// selection that reads source the plugin was never given. Every one of those is
// the same predicate, and a hand-rolled copy of it is how one call site comes to
// disagree with the next. This package is that predicate's only home.
//
// Resolution is the second, and its subject is a path whose reach is not this
// product's to bound — a file the person driving a client picked out of a
// dialog. Such a path names wherever they pointed, so it has no root to stay
// inside, and containment is the wrong question of it rather than a relaxed one.
//
// # The five entry points, and why they are five
//
// [Resolve] and [IsLocalSlash] answer containment, in the two path algebras the
// compiler actually uses. Resolve works in operating-system path arithmetic, for
// a caller holding a root it will open files under. IsLocalSlash works in
// slash-separated logical names, for a caller holding a name that is not a
// filesystem path yet — an archive entry, a rendered output name — and may never
// become one on the machine that is judging it.
//
// [Relative] answers containment in the other direction, for a caller that holds
// a path and needs the name to report for it rather than the path to open. Resolve
// composes a root and a relative name into the location to open; Relative decomposes
// a path into the name it has relative to that root, and refuses one that has no
// such name because it lies outside. It is a separate entry point rather than a
// second return value on Resolve because the callers are separate: one is about to
// read a file, the other is about to put a name in an answer.
//
// The distinction between those two is not cosmetic. A backslash is an ordinary
// character in a logical name and a separator in a Windows filesystem path, so
// judging a logical name with filesystem arithmetic gives one answer on Windows
// and another on Linux for identical input. Deciding the two algebras with one
// function and a mode argument would put that hazard one wrong argument away, so
// the two are separate names over one unexported core and there is no mode to
// pass.
//
// [ReportedName] answers containment's third caller: one that is about to put a
// path in an answer somebody outside this process reads. It decides nothing new —
// it asks [Relative] and spells the path whichever way that verdict allows, the
// name relative to the root for a path inside it and the absolute path for one
// outside. It is a separate entry point rather than a flag on Relative because
// the two callers want opposite things from a refusal: a caller judging
// containment wants the false, and a caller reporting a path wants a spelling
// regardless, since an answer with the path left out is one its reader cannot
// act on.
//
// [ResolveSelected] answers the second question, for an operating-system path a
// person selected. It is a third name rather than an option on Resolve for the
// same reason the two algebras are two names: which question applies is a
// property of where the path came from, and no argument at a call site can be
// trusted to carry that. Written as one function with a switch, the permissive
// answer would be one wrong argument away from every workspace-scoped path in
// the tree; written as three names, a reader of a call site sees which class was
// chosen.
//
// # What this package deliberately does not do
//
// It carries no diagnostic. A refusal is reported as a bare false, because the
// callers differ in what a refusal means to their own reader and in the
// vocabulary they say it in: one answers a plugin across a reply envelope,
// another rejects a request, another skips a file silently. Each attaches its
// own condition and its own message at its own call site.
//
// The analysis is purely lexical, exactly as [path.Clean] is. It resolves no
// symbolic link, so a caller whose root can contain a link out of the tree needs
// a filesystem-level check as well as this one.
package pathscope

import (
	"path"
	"path/filepath"
	"strings"
)

// Resolve reports where rel lands under root and whether it stays there.
//
// root and rel are operating-system paths, and rel is interpreted relative to
// root. On success the returned path is the cleaned join of the two — the path
// to open — and ok is true. On refusal the returned path is empty.
//
// Three inputs are refused:
//
//   - An empty root. Joining against one silently resolves rel against the
//     process working directory, which is a different directory than the caller
//     named and usually not one it may write to. A caller that means the working
//     directory says so, with ".".
//   - An absolute rel, which names a location of its own and ignores root
//     entirely.
//   - A rel that climbs out of root. Climbing is judged on the cleaned relative
//     path, so an interior "a/../b" resolves to "b" and is accepted, while
//     "../elsewhere" is refused. A name that merely begins with two dots —
//     "..config" — is an ordinary name and is accepted. A rel that climbs above
//     root is refused even where the join would land back inside it, as
//     "../app/ok" against a root of "/srv/app" would: the answer is a property
//     of the relative path and does not depend on what root is named.
//
// The empty rel is refused, because an empty relative path names nothing. A
// caller for which the empty string means the root itself normalises it to "."
// before calling, which keeps that meaning at the site that holds it rather than
// spreading it to every caller that does not.
//
// A sibling directory whose name merely extends root's — "/srv/app-old" against
// a root of "/srv/app" — is refused, which a bare string-prefix comparison
// against the root would accept.
func Resolve(root, rel string) (string, bool) {
	if root == "" {
		return "", false
	}
	if filepath.IsAbs(rel) {
		return "", false
	}
	if !staysInside(filepath.ToSlash(rel)) {
		return "", false
	}
	return filepath.Clean(filepath.Join(filepath.Clean(root), rel)), true
}

// Relative reports the name path has relative to root, in slash form, and
// whether it lies inside that root at all.
//
// It is the one home for that question, so every caller that asks where a path
// sits relative to a root shares one coordinate system — and every caller that
// reports such a name to a reader outside this process shares one rule about
// which names may be reported at all. A path that lies outside the root has no
// name relative to it that the reader could use: a climb above the root does not
// resolve from where that reader stands, and the location it names is the running
// machine's own layout rather than anything the reader asked about.
//
// root and path are operating-system paths, and path may arrive in either
// spelling. An absolute one is decomposed against root. One that is already
// relative to root is taken as it stands, which is the form a recorded
// provenance string is held in — so a caller holding one does not have to anchor
// it first, that being the step which gets forgotten, and forgetting it yields a
// refusal that reads like a containment verdict.
//
// Three inputs are refused, and the returned name is empty for each:
//
//   - An empty root, and an empty path. Neither names anything, and joining
//     against an empty root would silently address the process working directory
//     rather than the root the caller named.
//   - A path that resolves to the root itself. The root is not a name inside
//     itself, and a caller reporting "." for it would be answering with a
//     directory where its reader expects a file.
//   - A path outside the root, whether it climbs above it or names an absolute
//     location elsewhere. A sibling directory whose name merely extends root's is
//     outside it, which a bare string-prefix comparison would accept.
//
// A relative path is judged on the climb it spells rather than on where
// anchoring it would land, exactly as [Resolve] judges one: "../app/ok" against a
// root of "/srv/app" is refused even though joining it lands back inside, because
// the verdict is a property of the value being judged and not of what the root
// happens to be called. Anchoring first would launder such a value into a name
// that looks contained, which is the one answer a caller must never be given.
// An interior climb is not a climb out — "a/../b" resolves to "b" and is
// accepted — and a name that merely begins with two dots is an ordinary name.
//
// The analysis is lexical, as everything here is: it resolves no symbolic link,
// so a root that can contain a link out of the tree needs a filesystem-level
// check as well as this one.
func Relative(root, path string) (string, bool) {
	if root == "" || path == "" {
		return "", false
	}
	if !filepath.IsAbs(path) {
		if !staysInside(filepath.ToSlash(path)) {
			return "", false
		}
		rel := filepath.ToSlash(filepath.Clean(path))
		if rel == "." {
			return "", false
		}
		return rel, true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return "", false
	}
	if !staysInside(filepath.ToSlash(rel)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// ReportedName returns the name to report for path, given the root a reader
// anchors against.
//
// It is the one home for how a path is SPELLED in an answer, as [Relative] is
// the one home for whether a path is inside a root. A path inside the root is
// reported as its name relative to that root, which is the coordinate the rest
// of such an answer is in. A path outside it is reported as the absolute path,
// which is a location the reader can open, rather than as a climb above the
// root: a climb spells one path two ways depending on where it is read from,
// while every other surface naming that same file names it absolutely.
//
// It never withholds a path for a non-empty input. Withholding one leaves a
// reader holding an empty field that reads exactly like a field nobody filled
// in, so a working system and a broken one look alike from where they sit — and
// an out-of-root path is not a secret, being what this product's other surfaces
// print for the same file.
//
// root and path are operating-system paths, and path may arrive in either
// spelling, exactly as [Relative] takes it. The empty path is the one input with
// no name to report, and it is returned unchanged. Where there is no root to
// judge against, the path is reported as it arrived: with nothing to anchor it,
// what the caller was handed is the most that can honestly be said about it.
//
// The absolute form is composed by [ResolveSelected] rather than by arithmetic
// of its own, because the two are asking the same thing — where does this land,
// with no root it has to stay inside — and a second join here would be a copy
// that agrees today.
func ReportedName(root, path string) string {
	if path == "" {
		return ""
	}
	if rel, ok := Relative(root, path); ok {
		return rel
	}
	if abs, ok := ResolveSelected(root, path); ok {
		return abs
	}
	return path
}

// ResolveSelected reports where a path somebody selected lands.
//
// It performs no containment check at all, and that is the contract rather than
// a gap in it. Its subject is a path the person driving a client chose — a file
// picked out of a dialog — and such a path names wherever they pointed, on any
// part of the machine. There is no root for it to stay inside, so asking whether
// it does would answer a question nobody posed. Nor would refusing a selection
// outside the root make a caller safer: the process runs as the person who
// selected the path and already reads whatever they can read, so the refusal
// would buy nothing and would make the operation unusable for exactly the case
// it exists to serve.
//
// [Resolve] remains the entry point for a workspace-scoped path, and every path
// the product composed, or was handed as relative to a root it will confine
// writes to, still goes through it. The two are separate because the classes are
// separate: a path belongs to one or the other by virtue of where it came from,
// so a call site reaching for this one is stating that a person chose the value.
//
// root and selected are operating-system paths, and selected is interpreted
// relative to root only when it is not already absolute. On success the returned
// path is cleaned and ok is true. On refusal the returned path is empty.
//
// Two inputs are accepted that Resolve refuses:
//
//   - An absolute selected, returned cleaned. root is not consulted at all,
//     because an absolute path names a location of its own — which is the whole
//     point of this entry point, and the reason an empty root is no obstacle
//     here.
//   - A relative selected that climbs out of root, resolved against it and
//     landing outside. Climbing is PERMITTED, because refusing
//     "../build/plugin.wasm" while accepting the same file named absolutely
//     would be a distinction no caller could explain to its reader.
//
// Two inputs are refused, each for the reason Resolve refuses it:
//
//   - An empty selected, because an empty path names nothing. A caller for which
//     the empty string means the root itself normalises it to "." before
//     calling, which keeps that meaning at the site that holds it.
//   - A relative selected with an empty root. Joining against an empty root
//     silently resolves against the process working directory, which is a
//     different directory than the caller named. A caller that means the working
//     directory says so, with ".".
func ResolveSelected(root, selected string) (string, bool) {
	if selected == "" {
		return "", false
	}
	if filepath.IsAbs(selected) {
		return filepath.Clean(selected), true
	}
	if root == "" {
		return "", false
	}
	return filepath.Clean(filepath.Join(filepath.Clean(root), selected)), true
}

// IsLocalSlash reports whether name — a slash-separated logical name, such as an
// archive entry or a rendered output name — stays at or under the directory it
// is relative to.
//
// It judges the name in slash arithmetic on every platform, so an archive
// unpacked on Windows and on Linux is judged identically. An empty name, an
// absolute name, and a name that climbs above its directory are all refused; a
// name that merely begins with two dots is accepted, and an interior "a/../b"
// resolves to "b" and is accepted.
//
// When this reports true, joining name onto any root produces a path under that
// root, so a caller that has asked this question does not also need to check the
// join.
func IsLocalSlash(name string) bool {
	return staysInside(name)
}

// staysInside is the predicate both entry points decide with, over a
// slash-separated path. It is unexported so that a caller cannot reach the
// primitive and skip the normalisation its algebra owes — the entry point it
// went through is what fixes which arithmetic applies.
//
// path.Clean is slash-only and identical on every platform, which is what makes
// the answer here a property of the input rather than of the host.
func staysInside(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") {
		return false
	}
	c := path.Clean(p)
	return c != ".." && !strings.HasPrefix(c, "../")
}
