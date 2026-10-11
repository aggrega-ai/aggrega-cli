// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package writescope holds a write or a delete the product works out for itself to
// the root it is composed beneath: the workspace, or a directory outside it that
// the owner typed or the workspace configuration named.
//
// The product composes many paths of its own: the build output directory and every
// file it writes there, the trace, the lockfile and the plugin trust record beside
// a Release, the entity.lock beside an entity, the workspace configuration file,
// the artifact cache and each subtree of it, the drafts an import writes, and the
// Plugin entities a catalog generation writes. The person who started the
// operation named the operation, or the directory it writes under, not those
// paths. A link beneath that directory can lead any of them anywhere the user can
// write, so "rebuild the output" becomes deleting a directory elsewhere on the
// machine and "record the pins" becomes overwriting a file nobody meant the
// workspace to reach.
//
// [CheckWrite] and [CheckDelete] are the judgement every such write and delete is
// held to before it happens. Each asks [pathscope.ResolvesInside], which is the
// one definition of inside, and turns a path leading out into the halt this
// package declares, naming the link and where it leads. Neither writes or removes
// anything itself: the caller performs the operation it was about to perform,
// once the judgement has let it through.
//
// # Which root holds a write
//
// Every path the product composes, it composes beneath some directory: the
// workspace root, an output directory the owner typed, the directory of the
// Release a lockfile sits beside, the catalog directory an entity was found in.
// [RootFor] is the one answer to which root that directory holds its writes to.
// Where it lies inside the workspace, the root is the workspace root. Where it is
// a directory the owner typed on the command line, or one the workspace
// configuration names, and it lies outside the workspace, it is a root of its own: it is honoured where it was named — followed, if it is a
// link — and every file composed beneath it is held to it, so a link beneath it
// leading out of it is refused as a link inside the workspace is. Deciding which
// directory a write is composed beneath is the writer's, because only the writer
// knows; deciding which root that directory holds the write to is never a
// writer's.
//
// The artifact cache has an answer of its own, [RootForCache], asked by every
// writer, clearer and reader of the cache: the cache directory itself is the root
// of everything beneath it, the default as much as one the configuration names
// outside the workspace, so what a write may reach beneath it is what a removal or
// a read may reach. Where the cache directory itself lies is read from how the
// configuration spells it: one spelled climbing out of the workspace is a
// directory it named outside, honoured where it was named, and one spelled inside
// the workspace — the default among them — is held to the workspace.
//
// # Which paths it holds
//
// A path that reaches the root on its way — spelled beneath it, or entering it
// through a link — is judged. A path that never reaches the root passes: one
// written beneath a root that does not exist yet, where nothing beneath the root
// exists for a link to stand in, or one spelled elsewhere entirely. With no root
// at all, nothing is held.
package writescope

import (
	"fmt"
	"os"
	"path/filepath"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
)

// ErrWriteLeadsOutOfWorkspace is the halt raised when a path an operation is
// about to write or delete leads, through a link, out of the root it is held to —
// the workspace, a directory outside it the owner typed or the configuration
// named ([RootFor]), or the artifact cache directory for what is kept beneath it
// ([RootForCache]): the build output directory, a file composed beneath it, the
// trace, the lockfile, the plugin trust record, an entity.lock, the workspace
// configuration file, the artifact cache or a subtree of it, an imported draft, or
// a generated Plugin entity.
//
// The write or delete does not happen. The refusal names the path, the root it
// must stay inside, the link it meets on the way and the place that link leads to,
// so a reader knows which link to remove or repoint.
//
// Category: none. The fault is in the layout on disk rather than in any document
// an author wrote, so there is no authoring surface to route the reader to. Match
// with errors.Is(err, ErrWriteLeadsOutOfWorkspace()).
func ErrWriteLeadsOutOfWorkspace() error { return errWriteLeadsOutOfWorkspace }

var errWriteLeadsOutOfWorkspace = cerrors.Register("write-leads-out-of-workspace", cerrors.Meta{
	ShortCode: "E0320",
	Message:   "a path this operation writes or deletes leads through a link out of the directory it must stay inside",
	RulePlace: cerrors.RuleNoStageReaches("The rule is evaluated at the moment the product writes or deletes a path " +
		"of its own, inside the workspace or beneath a directory named outside it — a build writing its output, a " +
		"pull writing the lockfile and the cache, a pin writing an entity.lock, a configuration write, a cache " +
		"clear or prune, an import writing its drafts, a catalog generation writing its entities. None of those " +
		"moments is a declared stage of a compilation, and what the rule reads is the filesystem rather than a " +
		"document a compilation holds."),
})

// remedyFor is what the refusal tells its reader, in terms of what they can change,
// for a path that must stay inside root.
func remedyFor(root string) string {
	return "nothing was written to or removed from that path. Remove the link, or make it lead to a place inside " +
		root + ", and run the operation again"
}

// RootFor returns the root a write the product composes beneath anchor is held
// to, given the workspace at workspaceRoot: anchor itself where it lies outside
// the workspace, and workspaceRoot where it lies inside it.
//
// anchor is the directory the writer composes beneath, and it is the writer's to
// name: a directory the owner typed or the configuration named, or the directory
// such a file sits in. Which side of the workspace it lies on is the one
// judgement of where a path leads on disk ([pathscope.ResolvesInside]), not its
// spelling, so an anchor that is a link
// leading out of the workspace is outside it and is honoured where it was named.
// A directory nobody named — one the product found inside the workspace — is not
// an anchor; a writer composing beneath one passes the workspace root, or an
// empty anchor, which is the same answer.
//
// An empty anchor holds the write to the workspace root, and with no workspace
// root the anchor is the root, so a composed path is held to some root whenever
// there is anything to hold it to.
func RootFor(workspaceRoot, anchor string) string {
	if anchor == "" {
		return workspaceRoot
	}
	if _, inside := pathscope.ResolvesInside(workspaceRoot, anchor); inside {
		return workspaceRoot
	}
	return anchor
}

// RootForCache returns the root a write, a removal or a read beneath the
// workspace's artifact cache directory is held to, given the workspace at
// workspaceRoot and the cache directory cacheDir as the configuration spells it:
// relative to the workspace root, which is how every writer, clearer and reader of
// the cache joins it.
//
// The answer is the cache directory itself, whatever it is: the cache is the one
// root of everything kept beneath it. A class root, a slot or a file beneath it
// that is a link leading out of it — elsewhere inside the workspace as much as
// anywhere else — is refused, by a write, a removal and a read alike, so no write
// and no removal through the cache reaches a file outside the directory it is kept
// in, and what a write may reach is exactly what a removal may.
//
// Where the cache directory itself lies is read from its spelling first, unlike
// [RootFor]'s anchor, and that is deliberate. A configured cache directory that
// climbs out of the workspace is a directory the configuration named outside it,
// honoured where it was named — followed, if it is a link. One whose spelling stays
// beneath the workspace root — the default, or a configured one that does not
// climb out — is a directory the product works out inside the workspace: it is
// followed to wherever it leads inside the workspace, and where it, or a directory
// on the way to it, leads out of the workspace, the answer is the workspace root
// instead, so every path beneath it is refused naming that link. The default
// directory is nobody's naming, so a link standing at it is a link inside the
// workspace, refused, rather than a root of its own made so by where it leads.
//
// It is the one answer every writer, clearer and reader of the cache asks, so no
// two of them can come to hold the cache to different roots — a read is held where
// the write it reads back was, and a removal where both were. With no workspace
// root nothing is held, and the answer is empty.
func RootForCache(workspaceRoot, cacheDir string) string {
	if workspaceRoot == "" {
		return ""
	}
	dir := filepath.Join(workspaceRoot, cacheDir)
	if _, beneath := pathscope.Relative(workspaceRoot, cacheDir); beneath && RootFor(workspaceRoot, dir) != workspaceRoot {
		return workspaceRoot
	}
	return dir
}

// maxLinkHops bounds how far CheckWrite follows a chain of links that lead
// nowhere to find where a write would create its file. It is the limit an
// operating system applies to one path resolution; a longer chain fails the write
// on its own.
const maxLinkHops = 40

// CheckWrite reports whether path, a file or directory the operation is about to
// create or write beneath root, may be written: nil when it may, and the
// [ErrWriteLeadsOutOfWorkspace] halt when it leads out of root.
//
// The path is judged by [pathscope.ResolvesInside]: the deepest part of it that
// exists, with its links resolved, must be root or lie beneath it.
//
// One shape passes that judgement and still writes elsewhere, and it is refused
// here too: the last part of path being a link that leads nowhere. The definition
// passes over such a link for the directory holding it, which is inside — but
// creating a file through it creates the file the link points at, wherever that
// is. So where the last part is such a link, where it points is judged as well.
//
// root is the root the write is held to, as [RootFor] answers it. A path the root
// does not hold — one that never reaches the root on its way, or an empty root —
// passes; see the package documentation.
func CheckWrite(root, path string) error {
	held, err := check(verbWrite, root, path)
	if err != nil || !held {
		return err
	}
	abs := absolute(path)
	landing, viaLink := landingOf(abs)
	if !viaLink {
		return nil
	}
	if _, inside := pathscope.ResolvesInside(root, landing); !inside {
		return refusal(verbWrite, root, abs, abs, landing)
	}
	return nil
}

// CheckDelete reports whether path, a file or directory the operation is about
// to remove beneath root, may be removed: nil when it may, and the
// [ErrWriteLeadsOutOfWorkspace] halt when it leads out of root.
//
// It is the judgement [CheckWrite] makes, without the second look at a last part
// that is a link leading nowhere: removing such a link removes the link itself,
// which sits beneath root.
func CheckDelete(root, path string) error {
	_, err := check(verbDelete, root, path)
	return err
}

// The two verbs a refusal names its path with.
const (
	verbWrite  = "writing"
	verbDelete = "removing"
)

// check is the judgement both entry points share. It reports whether the root
// holds path at all, and the halt where a held path leads out.
func check(verb, root, path string) (bool, error) {
	if root == "" || path == "" {
		return false, nil
	}
	res, inside := pathscope.ResolvesInside(root, path)
	switch {
	case inside:
		return true, nil
	case res.Link == "":
		// The path never reaches the root on its way, so no link carries it out of
		// the root: the root does not hold it.
		return false, nil
	}
	return true, refusal(verb, root, absolute(path), res.Link, res.LinkLeadsTo)
}

// landingOf follows a chain of links at the last part of p that lead nowhere,
// and returns the path a file created at p would be created at, and whether p is
// such a link at all.
func landingOf(p string) (string, bool) {
	info, err := os.Lstat(p)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return p, false
	}
	if _, err := os.Stat(p); err == nil {
		// The link leads somewhere: the definition already judged where.
		return p, false
	}
	cur := p
	for hop := 0; hop < maxLinkHops; hop++ {
		info, err := os.Lstat(cur)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			break
		}
		target, err := os.Readlink(cur)
		if err != nil {
			break
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(cur), target)
		}
		cur = filepath.Clean(target)
	}
	return cur, true
}

// refusal composes the halt for path, which must stay inside root and meets link
// on the way, which leads it to leadsTo.
func refusal(verb, root, path, link, leadsTo string) error {
	inside := absolute(root)
	values := []string{
		verb + " " + path,
		"which must stay inside " + inside,
		fmt.Sprintf("the link %s leads to %s", link, leadsTo),
	}
	return cerrors.Raise(errWriteLeadsOutOfWorkspace, "", cerrors.SourceLocation{File: link},
		cerrors.ComposeMessage(ErrWriteLeadsOutOfWorkspace(), values, remedyFor(inside)), nil)
}

// absolute is p made absolute against the working directory and cleaned, the
// spelling the refusal names a path in.
func absolute(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}
