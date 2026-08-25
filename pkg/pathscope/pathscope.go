// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package pathscope answers one question — does this path stay inside the
// directory it is relative to — and answers it in the two path algebras the
// compiler actually uses.
//
// A path that escapes its root is a defect wherever it appears: a rendered
// artifact name that writes outside the output directory, an archive entry that
// unpacks over a neighbouring tree, a plugin selection that reads source the
// plugin was never given. Every one of those is the same predicate, and a
// hand-rolled copy of it is how one call site comes to disagree with the next.
// This package is that predicate's only home.
//
// # The two algebras, and why they are separate entry points
//
// [Resolve] works in operating-system path arithmetic, for a caller holding a
// root it will open files under. [IsLocalSlash] works in slash-separated logical
// names, for a caller holding a name that is not a filesystem path yet — an
// archive entry, a rendered output name — and may never become one on the
// machine that is judging it.
//
// The distinction is not cosmetic. A backslash is an ordinary character in a
// logical name and a separator in a Windows filesystem path, so judging a
// logical name with filesystem arithmetic gives one answer on Windows and
// another on Linux for identical input. Deciding the two algebras with one
// function and a mode argument would put that hazard one wrong argument away, so
// the two are separate names over one unexported core and there is no mode to
// pass.
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
