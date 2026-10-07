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
// [CheckBesideEntityRead] is the same judgement for a file read beside an entity's
// document rather than as a document of a directory read whole: the other documents
// in an entity's directory, read to tell whether another entity shares it, and the
// .wasm a Plugin entity's artifact is read from beside it. Its refusal names the
// remedy such a file has: keep the file itself where it sits, rather than a link to
// it.
//
// [CheckCacheRead] is the same judgement for a file or directory read beneath the
// workspace's artifact cache: a cached entity, plugin, source archive, external
// input, documentation bundle or registry index, and every entry a cache inventory
// lists. The cache is the product's own store, written beside the workspace, so it
// is read from the place its writes are held to, and its refusal names the remedy
// the cache has: a pull fills it again, and a cache kept outside the workspace is
// one the configuration names rather than one a link reaches.
//
// [CheckOverlayRead] is the same judgement for an overlay a catalog generation reads
// beneath the overlay directory it was given: the authored half of each Plugin
// entity it generates. A missing overlay changes what is generated, so a link
// leading out halts the generation rather than being passed over, and its refusal
// names the remedy an overlay has: keep the overlay itself beneath the overlay
// directory, or name a directory holding every overlay.
//
// [CheckFetchedRead] is the same judgement for a file or directory a pull reads
// beneath a repository a source fetched and checked out, to archive it. The
// checkout is a root of its own: what the repository carries is read only from
// inside it, so a link the repository commits leading out of it is not read and
// halts the pull, and its refusal names the remedy such a link has — it is the
// repository's, not the workspace's, so the repository is where it is changed.
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
// configuration is merged from, or the lockfile beside a Release, or a file beside
// an entity's document — another document in its directory, a Plugin entity's
// .wasm — or a file or directory beneath the workspace's artifact cache, or an
// overlay beneath the overlay directory a catalog generation reads, or a file a
// pull archives from a repository a source fetched, leads, through a link, out of
// the root it is held to: the workspace, a directory outside it the owner typed or
// the configuration named, the artifact cache directory, or the checkout of the
// fetched repository.
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
		"from, before any command runs, where it reads the lockfile beside a Release for the pins a build, a " +
		"check or the editor resolves against, where it reads a file beside an entity's document — the other " +
		"documents in the directory of an entity being pinned or published, and the .wasm beside a Plugin entity " +
		"a build or a pull loads — and where it reads the workspace's artifact cache: a cached entity, plugin, " +
		"source archive, external input, documentation bundle or registry index, and the entries a cache status, " +
		"prune or clear lists — and where a catalog generation reads the overlays beneath the overlay directory it " +
		"was given, and where a pull archives the files of a repository a source fetched. Each of those reads the " +
		"filesystem ahead of every declared stage, and no stage holds the entry the rule is judged against."),
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

// besideEntityRemedyFor is what the refusal tells its reader for a file beside an
// entity's document that must stay inside root.
//
// Such a file is read where it sits, beside the entity, and no setting names one
// kept elsewhere: listing a catalog directory changes neither which documents sit
// in an entity's directory nor where a Plugin entity's .wasm is looked for. So the
// remedy is the file itself, in place of the link.
func besideEntityRemedyFor(root string) string {
	return "nothing was read through that link. Remove the link, or make it lead to a place inside " + root +
		". A file beside an entity — another document in its directory, or the built .wasm beside a Plugin " +
		"entity — is read where it sits, so keep the file itself there rather than a link to it"
}

// cacheRemedyFor is what the refusal tells its reader for a file or directory
// beneath the workspace's artifact cache that must stay inside root.
//
// The cache is the product's own store: a pull fills it, so with the link removed
// the next pull writes what it held again. A cache kept somewhere else is reached
// by naming its directory in the workspace configuration, which is read where it
// is named, rather than by a link beneath the workspace, which is not followed out
// of it.
func cacheRemedyFor(root string) string {
	return "nothing was read through that link. Remove the link, or make it lead to a place inside " + root +
		": the artifact cache is read only from inside the directory it is kept in, where a pull writes it, so " +
		"with the link removed a pull fills it again. To keep the artifact cache outside the workspace, name its " +
		"directory, relative to the workspace root, as artifact_cache_dir in the workspace configuration rather " +
		"than reaching it through a link"
}

// overlayRemedyFor is what the refusal tells its reader for an overlay beneath the
// overlay directory that must stay inside root.
//
// An overlay is read only from beneath the overlay directory, and no setting names
// one kept elsewhere, so the remedy is the overlay itself in place of the link — or a
// different overlay directory, which is read where it was named.
func overlayRemedyFor(root string) string {
	return "nothing was read through that link. Remove the link, or make it lead to a place inside " + root +
		". Overlays are read from beneath the overlay directory, so keep the overlay itself there rather than a " +
		"link to it, or name a directory holding every overlay as the overlay directory: a directory you name is " +
		"read where it leads, even when it is itself a link"
}

// fetchedRemedyFor is what the refusal tells its reader for a link a fetched
// repository carries, which must stay inside the checkout at root.
//
// The link is committed in the repository, so nothing in the workspace removes it
// and the checkout it was met in is gone once the pull halts: the remedy is in the
// repository — a link that stays inside it, or the file itself in its place — and
// in the revision the source names. root is not named, for the same reason.
func fetchedRemedyFor(string) string {
	return "nothing was read through that link, and nothing was cached. The link is committed in the repository " +
		"the source names, which is read only from inside its own checkout: a link there is archived as the file or " +
		"directory it leads to only where that lies inside the repository too. Make the link lead to a place inside " +
		"the repository, or commit the file itself in its place, and name the revision that carries the change"
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

// CheckBesideEntityRead is [CheckRead] for a file about to be read beside an
// entity's document: the same judgement and the same condition, with the remedy a
// file beside an entity has. root is the one what is composed and read beside that
// document is held to — for a document found beneath a catalog directory, the root
// that directory picks; for one named by path, the root its own directory picks.
func CheckBesideEntityRead(root, path string) error {
	return checkRead(root, path, besideEntityRemedyFor)
}

// CheckCacheRead is [CheckRead] for a file or directory about to be read beneath
// the workspace's artifact cache: the same judgement and the same condition, with
// the remedy the cache has. root is the one a write beneath the same cache
// directory is held to — the root [writescope.RootForCache] picks for the
// workspace and the configured cache directory — so the cache is read from the
// place it may be written to, and a cache directory, a directory on the way, or the
// file itself, that is a link leading out of that root is not read.
func CheckCacheRead(root, path string) error {
	return checkRead(root, path, cacheRemedyFor)
}

// CheckOverlayRead is [CheckRead] for an overlay, or a directory that may hold
// overlays, about to be read beneath the overlay directory a catalog generation
// merges its Plugin entities' authored halves from: the same judgement and the same
// condition, with the remedy an overlay has. root is the one the overlay directory
// picks ([writescope.RootFor]) — the workspace for an overlay directory inside it,
// and the overlay directory itself for one named outside it.
func CheckOverlayRead(root, path string) error {
	return checkRead(root, path, overlayRemedyFor)
}

// CheckFetchedRead is [CheckRead] for a file or directory about to be read beneath
// the checkout of a repository a source fetched, to be archived: the same judgement
// and the same condition, with the remedy a link in a fetched repository has. root
// is the checkout itself, a root of its own, so a link the repository carries
// leading out of it — to anywhere on the machine running the pull — is not read.
func CheckFetchedRead(root, path string) error {
	return checkRead(root, path, fetchedRemedyFor)
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
