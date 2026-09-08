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
// # The three entry points, and why they are three
//
// [Resolve] and [IsLocalSlash] answer containment, in the two path algebras the
// compiler actually uses. Resolve works in operating-system path arithmetic, for
// a caller holding a root it will open files under. IsLocalSlash works in
// slash-separated logical names, for a caller holding a name that is not a
// filesystem path yet — an archive entry, a rendered output name — and may never
// become one on the machine that is judging it.
//
// The distinction between those two is not cosmetic. A backslash is an ordinary
// character in a logical name and a separator in a Windows filesystem path, so
// judging a logical name with filesystem arithmetic gives one answer on Windows
// and another on Linux for identical input. Deciding the two algebras with one
// function and a mode argument would put that hazard one wrong argument away, so
// the two are separate names over one unexported core and there is no mode to
// pass.
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
