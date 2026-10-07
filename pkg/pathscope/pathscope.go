// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package pathscope is the one home for deciding a path's scope. Three questions
// live here: does a path stay inside the directory it is relative to as it is
// written, where does a path somebody chose for themselves land, and does a path
// lead inside a directory once the links on the way to it are followed.
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
// Where a path leads on disk is the third, and it is a different question from
// the first rather than a stricter form of it. A path written inside a workspace
// can lead anywhere through a symbolic link on the way to it, so a verdict on the
// spelling says nothing about which file is read, written or removed. [ResolvesInside]
// answers it, and it is the only entry point here that judges the filesystem.
//
// Beside the three questions sits the walk of a tree beneath a root, [WalkDir],
// because a root reached through a link is the directory it leads to for a walk
// as it is for ResolvesInside: a walk of such a root reads where it leads, and
// hands on every path spelled beneath the root as named. It answers no question of
// scope; a walker judges each link it meets with ResolvesInside.
//
// # The entry points, and why they are separate
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
// The distinction between those two is not cosmetic. A backslash is a separator
// in a Windows filesystem path and in no Linux one, so judging a logical name
// with the host's own filesystem arithmetic gives one answer on Windows and
// another on Linux for identical input. IsLocalSlash gives one answer on every
// machine instead, and judges a climb with a backslash read as a separator
// there too: a logical name is joined onto a root in the spelling of the machine
// that writes it, and a Windows join reads a backslash as one, so a name that
// climbs out through backslashes is refused everywhere. Deciding the two
// algebras with one function and a mode argument would put the host-dependent
// answer one wrong argument away, so the two are separate names over one
// unexported core and there is no mode to pass.
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
// [ResolvesInside] answers the third question. It is the one definition of
// "inside" for a path on disk — for a path read, for a path written, and for a
// path not yet created — and every check that asks whether a path leads out of a
// workspace asks it, so no layer derives the answer for itself. It is separate
// from the lexical entry points because it answers about the filesystem as it is
// at the moment of asking, while they answer about a spelling and give the same
// answer on every machine.
//
// # What this package deliberately does not do
//
// It carries no diagnostic. A refusal is reported as a bare false, beside the
// facts a caller needs to word its own refusal, because the callers differ in
// what a refusal means to their own reader and in the vocabulary they say it in:
// one answers a plugin across a reply envelope, another rejects a request,
// another skips a file silently, another halts an operation before it writes.
// Each attaches its own condition and its own message at its own call site.
//
// Every entry point but [ResolvesInside] and [WalkDir] is purely lexical, exactly
// as [path.Clean] is. Those resolve no symbolic link, so a caller whose root can
// contain a link out of the tree asks [ResolvesInside] as well.
package pathscope

import (
	"os"
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
// The analysis is lexical: it resolves no symbolic link, so a root that can
// contain a link out of the tree asks [ResolvesInside] as well as this.
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
// The verdict is the same on every platform, so an archive unpacked on Windows
// and on Linux is judged identically. An empty name, an absolute name, and a
// name that climbs above its directory are all refused; a name that merely
// begins with two dots is accepted, and an interior "a/../b" resolves to "b" and
// is accepted.
//
// A climb is judged with a backslash read as a separator as well as a slash, on
// every platform, because a name is joined onto a root in the spelling of the
// machine that writes it and a Windows join reads a backslash as a separator.
// So "..\x" and "a\..\..\x" are refused on Linux as they are on Windows. The
// backslash is read that way for the climb alone: "a\b" is accepted, and a name
// opening with a backslash is not thereby absolute, since a join places it
// beneath the root. Both the name as written and the name cleaned as a
// slash-separated name are judged, because a caller may join either, and
// cleaning drops a whole slash-separated part for each "..", a part the
// backslash reading may count as several: "a\b/../..\x" climbs only once it is
// cleaned, to "..\x".
//
// When this reports true, joining name onto any root produces a path under that
// root, whether the name is joined as written or cleaned first, so a caller that
// has asked this question does not also need to check the join. A root here is
// a directory path: an empty string is not one, and nor is a bare Windows drive
// name such as "C:", since a Windows join onto either starts a name opening
// with a separator from the top of the drive.
func IsLocalSlash(name string) bool {
	return staysInside(name) && !climbsAcrossBackslash(name) && !climbsAcrossBackslash(path.Clean(name))
}

// climbsAcrossBackslash reports whether p climbs above its directory once each
// backslash in it is read as a separator, as a Windows join reads it.
//
// Separators leading the name are dropped first, because a join does not treat
// them as a fresh start: on Windows, joining "\x" onto a root lands at the root's
// own "x", while "\.." lands above it. A name holding no backslash answers false,
// which leaves its verdict exactly the one staysInside gives it.
func climbsAcrossBackslash(p string) bool {
	if !strings.Contains(p, `\`) {
		return false
	}
	rest := strings.TrimLeft(strings.ReplaceAll(p, `\`, "/"), "/")
	return rest != "" && !staysInside(rest)
}

// Resolution is what [ResolvesInside] found on the way to its verdict: the part
// of the path it judged, where that part leads, and — for a path that leads out —
// the link it leaves through.
//
// Every path in it is absolute and cleaned. It is facts rather than a refusal,
// for the reason the package carries no diagnostic: the caller that asked is the
// one that knows what a path leading out means to its own reader.
type Resolution struct {
	// Met is the deepest part of the path that exists, spelled as the path spells
	// it. It is the path itself where the path exists.
	Met string
	// LeadsTo is Met with its links resolved: the place the judgement was made on.
	LeadsTo string
	// Link is set only for a path that leads out after reaching the root on the
	// way — spelled inside it, or entering it through a link earlier on the way: it
	// is the shortest part of the path beyond where it reaches the root, spelled as
	// the path spells it, whose links resolved lead outside the root — the link a
	// reader removes or repoints. It is empty for a path inside, and for a path that
	// never reaches the root on the way, where no link is what carries it out.
	Link string
	// LinkLeadsTo is where Link leads with its links resolved, and empty where
	// Link is.
	LinkLeadsTo string
}

// ResolvesInside reports whether path is inside root on disk: whether the
// deepest part of path that exists, with its links resolved once, is root or lies
// beneath it, root resolved the same way.
//
// "Exists" means a stat succeeds, so a link that leads nowhere, or round a loop,
// is not existence: it is passed over and the directory holding it is judged
// instead. Nothing is followed to find out where such a link was meant to go.
//
// The deepest existing part is what makes one verdict serve a path that exists
// and one that does not yet. A write names a file about to be created, beneath
// directories that may themselves be links, and those directories are the ones
// the write goes through. Where the whole path exists the part judged is the path
// itself, so the verdict is the one resolving the whole path would give.
//
// root is resolved the same way: its deepest existing part with its links
// resolved, and the part of it that does not exist yet written beneath that. A
// root reached through a link of its own therefore still counts what lies under
// it as inside, and a root that does not exist has nothing inside it.
//
// A relative root or path is made absolute against the process working
// directory, which is where the operating system opens it. An empty root or an
// empty path names nothing to judge and is reported outside, with an empty
// Resolution.
//
// It judges where a path leads on disk and nothing else: it reads no value and
// infers nothing from a name, and it is a verdict at the moment of asking rather
// than a lock on the filesystem. It reports facts and no diagnostic, so a caller
// names the link and where it leads in its own words — see [Resolution].
func ResolvesInside(root, path string) (Resolution, bool) {
	if root == "" || path == "" {
		return Resolution{}, false
	}
	absRoot := absolute(root)
	resolvedRoot := resolveDeepest(absRoot)
	absPath := absolute(path)

	met := deepestExisting(absPath)
	res := Resolution{Met: met, LeadsTo: resolveExisting(met)}
	if within(resolvedRoot, res.LeadsTo) {
		return res, true
	}

	// Leading out. Where the path reaches the root on the way — spelled inside it,
	// or entering it through a link of its own — some part of it beyond that point
	// is a link that carries it out; find the shortest such part so the caller can
	// name the link rather than only the path.
	base := ""
	switch {
	case within(absRoot, met):
		base = absRoot
	case within(resolvedRoot, met):
		base = resolvedRoot
	default:
		base = entersRoot(resolvedRoot, met)
	}
	if base != "" {
		res.Link, res.LinkLeadsTo = met, res.LeadsTo
		rel, err := filepath.Rel(base, met)
		if err == nil && rel != "." {
			prefix := base
			for _, part := range strings.Split(rel, string(filepath.Separator)) {
				prefix = filepath.Join(prefix, part)
				if leadsTo := resolveExisting(prefix); !within(resolvedRoot, leadsTo) {
					res.Link, res.LinkLeadsTo = prefix, leadsTo
					break
				}
			}
		}
	}
	return res, false
}

// within reports whether p is root or lies beneath it. Both are absolute and
// cleaned; containment below the root is the lexical question [Relative] owns.
func within(root, p string) bool {
	if p == root {
		return true
	}
	_, ok := Relative(root, p)
	return ok
}

// entersRoot returns the shortest leading part of p whose links resolved lie at
// or beneath resolvedRoot, spelled as p spells it, or "" where no leading part of
// p does. p exists, so every leading part of it does too. It is asked only for a
// path spelled outside the root, which can still reach the root through a link on
// the way: a directory outside that leads into the workspace.
func entersRoot(resolvedRoot, p string) string {
	prefix := filepath.VolumeName(p) + string(filepath.Separator)
	rest := strings.TrimPrefix(p[len(filepath.VolumeName(p)):], string(filepath.Separator))
	if rest == "" {
		return ""
	}
	for _, part := range strings.Split(rest, string(filepath.Separator)) {
		prefix = filepath.Join(prefix, part)
		if within(resolvedRoot, resolveExisting(prefix)) {
			return prefix
		}
	}
	return ""
}

// absolute is p made absolute against the working directory and cleaned, or p
// cleaned where the working directory cannot be read.
func absolute(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}

// deepestExisting returns p where a stat of it succeeds, and otherwise the
// nearest directory above it where one does. A link leading nowhere, or round a
// loop, fails the stat and so is passed over for the directory holding it.
func deepestExisting(p string) string {
	for {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		p = parent
	}
}

// resolveExisting is p, which exists, with its links resolved; p itself where the
// resolution fails, which only a change racing the call can cause.
func resolveExisting(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

// resolveDeepest is p with its deepest existing part's links resolved and the
// part that does not exist yet written beneath it.
func resolveDeepest(p string) string {
	met := deepestExisting(p)
	resolved := resolveExisting(met)
	if met == p {
		return resolved
	}
	rest, err := filepath.Rel(met, p)
	if err != nil {
		return p
	}
	return filepath.Join(resolved, rest)
}

// staysInside is the predicate the lexical entry points decide with, over a
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
