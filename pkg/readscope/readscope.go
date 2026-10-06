// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package readscope holds a read of workspace content to the root it is read
// beneath: the workspace, or a directory outside it that the owner typed or the
// workspace configuration named.
//
// The product reads many files nobody named one by one: every document beneath a
// catalog directory, the Aggregates and Manifests beside a Release, the files an
// entity declares as content. A link among them can lead anywhere the user can
// read, so a catalog directory, a Release's directory or an entity's directory
// would otherwise hand the product a file from elsewhere on the machine under a
// name inside the workspace — and a lookup that met such a link and silently
// passed over it could resolve another entity of the same name in its place.
//
// [CheckRead] is the judgement such a read is held to before the file is opened.
// It asks [pathscope.ResolvesInside], which is the one definition of inside, and
// turns a path leading out into the halt this package declares, naming the link
// and where it leads. It opens nothing itself: the caller reads what it was about
// to read once the judgement has let it through, and a caller listing what it
// found rather than reading it for a build names the link as not read instead of
// halting.
//
// [CheckConfigurationRead] is the same judgement for a workspace configuration
// file the configuration is about to be merged from. Only the remedy its refusal
// names differs, because a configuration file kept outside the workspace has a
// way in of its own — the global configuration file — where a catalog document
// has a catalog directory.
//
// [CheckLockfileRead] is the same judgement for the lockfile beside a Release,
// which a build, a pull, a check and the editor read the Release's pins from. A
// lockfile is the product's own file, composed and written beside the Release and
// held there when it is written, so it is read from the same place under the same
// root, and its refusal names the remedy a lockfile has: there is no other place
// to keep one.
//
// [CheckReference] is the same judgement for a file a document names by a path —
// a Release's aggregate, manifest and extends, a Manifest's extends, a Release's
// parameters import. Such a reference is the author's words rather than a listing
// of a directory, so it can climb out of its root as written as well as through a
// link, and it is refused either way. Its root is picked the same way, for the
// directory of the document the resolution started at.
//
// # Which root holds a read
//
// The root is the one a write beneath the same directory is held to,
// [writescope.RootFor]: the workspace root where the directory lies inside the
// workspace, and the directory itself where it is one the owner typed or the
// configuration named outside the workspace. Deciding which directory a read
// starts beneath is the reader's; deciding which root that directory holds it to
// never is.
package readscope

import (
	"fmt"
	"path/filepath"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
)

// ErrReadLeadsOutOfWorkspace is the halt raised when a file an operation is about
// to read, beneath a directory it reads as a whole — a catalog directory, the
// directory holding a Release — or a configuration file the workspace's
// configuration is merged from, or the lockfile beside a Release, leads, through a
// link, out of the root it is held to: the workspace, or a directory outside it
// the owner typed or the configuration named.
//
// Nothing is read through the link. The refusal names the path, the root it must
// stay inside, the link it meets on the way and the place that link leads to, so
// a reader knows which link to remove or repoint.
//
// Category: none. The fault is in the layout on disk rather than in any document
// an author wrote, so there is no authoring surface to route the reader to. Match
// with errors.Is(err, ErrReadLeadsOutOfWorkspace()).
func ErrReadLeadsOutOfWorkspace() error { return errReadLeadsOutOfWorkspace }

var errReadLeadsOutOfWorkspace = cerrors.Register("read-leads-out-of-workspace", cerrors.Meta{
	ShortCode: "E0321",
	Message:   "a file this operation reads leads through a link out of the directory it must stay inside",
	RulePlace: cerrors.RuleNoStageReaches("The rule is evaluated where the product reads a directory of workspace " +
		"content as a whole — a catalog directory's documents, read by a lookup a build, a pull, a check or the " +
		"editor depends on, and the Aggregates and Manifests beside a Release, read to resolve the names the " +
		"Release gives them — where it reads the configuration files the workspace's configuration is merged " +
		"from, before any command runs, and where it reads the lockfile beside a Release for the pins a build, a " +
		"check or the editor resolves against. Each of those reads the filesystem ahead of every declared stage, " +
		"and no stage holds the entry the rule is judged against."),
})

// remedyFor is what the refusal tells its reader, in terms of what they can change,
// for a path that must stay inside root.
//
// It names the directory to list as the one holding what was kept outside, never
// the directory the link was met beneath: that one may already be listed, and a
// link met beneath a listed directory is judged exactly as one met beneath any
// other, so listing it again changes nothing. A listed directory that is itself a
// link is read where it leads, which is why listing is the remedy at all.
func remedyFor(root string) string {
	return "nothing was read through that link. Remove the link, or make it lead to a place inside " + root +
		". To read what is kept outside, list the directory holding it as a catalog directory in the workspace " +
		"configuration, rather than reaching it through a link beneath one: a listed directory is read where it " +
		"leads, even when it is itself a link"
}

// configurationRemedyFor is what the refusal tells its reader for a workspace
// configuration file that must stay inside root.
//
// It names the global configuration file as the way a configuration kept outside
// the workspace is read, because that is the one layer the product reads from
// wherever it was put, and it is named deliberately — by a flag or an environment
// variable — rather than met beneath the workspace.
func configurationRemedyFor(root string) string {
	return "nothing was read through that link. Remove the link, or make it lead to a place inside " + root +
		": a workspace's configuration files are read only from inside the workspace they configure. A " +
		"configuration file kept elsewhere is read by naming it as the global configuration file, with --config " +
		"or AGGREGA_CONFIG"
}

// lockfileRemedyFor is what the refusal tells its reader for a Release's lockfile
// that must stay inside root.
//
// A lockfile has one place, beside its Release, and a pull writes it there, so the
// remedy is to remove the link and pull again rather than to name the file from
// somewhere else: no setting names a lockfile kept elsewhere.
func lockfileRemedyFor(root string) string {
	return "nothing was read through that link. Remove the link, or make it lead to a place inside " + root +
		": a Release's lockfile is read only from beside the Release, where a pull writes it. With the link " +
		"removed, pulling the Release again writes a fresh lockfile there"
}

// CheckRead reports whether path, a file or directory the operation is about to
// read beneath root, may be read: nil when it may, and the
// [ErrReadLeadsOutOfWorkspace] halt when it leads out of root.
//
// The path is judged by [pathscope.ResolvesInside]: the deepest part of it that
// exists, with its links resolved, must be root or lie beneath it. A path that
// never reaches root on its way passes, because root does not hold it, and so does
// any path where root is empty: with no root there is nothing to hold a read to.
func CheckRead(root, path string) error {
	return checkRead(root, path, remedyFor)
}

// CheckConfigurationRead is [CheckRead] for a workspace configuration file the
// configuration is about to be merged from: the same judgement and the same
// condition, with the remedy a configuration file has.
func CheckConfigurationRead(root, path string) error {
	return checkRead(root, path, configurationRemedyFor)
}

// CheckLockfileRead is [CheckRead] for the lockfile beside a Release, about to be
// read for its pins: the same judgement and the same condition, with the remedy a
// lockfile has. root is the one a write of the same lockfile is held to — the root
// [writescope.RootFor] picks for the Release's directory — so the file a pull may
// write and the file a build may read are held alike.
func CheckLockfileRead(root, path string) error {
	return checkRead(root, path, lockfileRemedyFor)
}

// checkRead is the judgement every entry point makes, composing the refusal with
// the remedy the caller's kind of read has.
func checkRead(root, path string, remedy func(root string) string) error {
	if root == "" || path == "" {
		return nil
	}
	res, inside := pathscope.ResolvesInside(root, path)
	if inside || res.Link == "" {
		return nil
	}
	values := []string{
		"reading " + absolute(path),
		"which must stay inside " + absolute(root),
		fmt.Sprintf("the link %s leads to %s", res.Link, res.LinkLeadsTo),
	}
	return cerrors.Raise(errReadLeadsOutOfWorkspace, "", cerrors.SourceLocation{File: res.Link},
		cerrors.ComposeMessage(ErrReadLeadsOutOfWorkspace(), values, remedy(absolute(root))), nil)
}

// absolute is p made absolute against the working directory and cleaned, the
// spelling the refusal names a path in.
func absolute(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}
