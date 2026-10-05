// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package writescope holds a write or a delete the product works out for itself
// inside a workspace to that workspace.
//
// The product composes many paths of its own beneath the workspace it is working
// in: the build output directory and every file it writes there, the trace, the
// lockfile and the plugin trust record beside a Release, the entity.lock beside an
// entity, the workspace configuration file, the artifact cache and each subtree of
// it, and the drafts an import writes. The person who started the operation named
// the operation, not those paths. A link inside the workspace can lead any of them
// anywhere the user can write, so "rebuild the output" becomes deleting a
// directory elsewhere on the machine and "record the pins" becomes overwriting a
// file nobody meant the workspace to reach.
//
// [CheckWrite] and [CheckDelete] are the judgement every such write and delete is
// held to before it happens. Each asks [pathscope.ResolvesInside], which is the
// one definition of inside, and turns a path leading out into the halt this
// package declares, naming the link and where it leads. Neither writes or removes
// anything itself: the caller performs the operation it was about to perform,
// once the judgement has let it through.
//
// # Which paths it holds
//
// A path written inside the workspace root, as its spelling places it. A path the
// product composes outside the root — beside a file the owner typed on the command
// line, or beside one the workspace configuration names — is not a path inside the
// workspace and is left as it was; so is every path when there is no workspace
// root to hold it to.
package writescope

import (
	"fmt"
	"os"
	"path/filepath"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
)

// ErrWriteLeadsOutOfWorkspace is the halt raised when a path an operation is
// about to write or delete inside the workspace leads out of it through a link:
// the build output directory, a file composed beneath it, the trace, the
// lockfile, the plugin trust record, an entity.lock, the workspace configuration
// file, the artifact cache or a subtree of it, or an imported draft.
//
// The write or delete does not happen. The refusal names the path, the link it
// meets on the way and the place that link leads to, so a reader knows which link
// to remove or repoint.
//
// Category: none. The fault is in the workspace's layout on disk rather than in
// any document an author wrote, so there is no authoring surface to route the
// reader to. Match with errors.Is(err, ErrWriteLeadsOutOfWorkspace()).
func ErrWriteLeadsOutOfWorkspace() error { return errWriteLeadsOutOfWorkspace }

var errWriteLeadsOutOfWorkspace = cerrors.Register("write-leads-out-of-workspace", cerrors.Meta{
	ShortCode: "E0320",
	Message:   "a path this operation writes or deletes inside the workspace leads out of it through a link",
	RulePlace: cerrors.RuleNoStageReaches("The rule is evaluated at the moment the product writes or deletes a path " +
		"of its own inside the workspace — a build writing its output, a pull writing the lockfile and the cache, " +
		"a pin writing an entity.lock, a configuration write, a cache clear or prune, an import writing its " +
		"drafts. None of those moments is a declared stage of a compilation, and what the rule reads is the " +
		"filesystem under the workspace rather than a document a compilation holds."),
})

// remedy is what the refusal tells its reader, in terms of what they can change.
const remedy = "nothing was written to or removed from that path. Remove the link, or make it lead to a " +
	"place inside the workspace, and run the operation again"

// maxLinkHops bounds how far CheckWrite follows a chain of links that lead
// nowhere to find where a write would create its file. It is the limit an
// operating system applies to one path resolution; a longer chain fails the write
// on its own.
const maxLinkHops = 40

// CheckWrite reports whether path, a file or directory the operation is about to
// create or write inside the workspace at root, may be written: nil when it may,
// and the [ErrWriteLeadsOutOfWorkspace] halt when it leads out.
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
// A path the root does not hold — its spelling outside the root, or an empty root
// — passes; see the package documentation.
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
		return refusal(verbWrite, abs, abs, landing)
	}
	return nil
}

// CheckDelete reports whether path, a file or directory the operation is about
// to remove inside the workspace at root, may be removed: nil when it may, and the
// [ErrWriteLeadsOutOfWorkspace] halt when it leads out.
//
// It is the judgement [CheckWrite] makes, without the second look at a last part
// that is a link leading nowhere: removing such a link removes the link itself,
// which sits inside the workspace.
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
		// Spelled outside the root: not a path inside the workspace.
		return false, nil
	}
	return true, refusal(verb, absolute(path), res.Link, res.LinkLeadsTo)
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

// refusal composes the halt for path, which meets link on the way and is led by
// it to leadsTo.
func refusal(verb, path, link, leadsTo string) error {
	values := []string{
		verb + " " + path,
		fmt.Sprintf("the link %s leads to %s", link, leadsTo),
	}
	return cerrors.Raise(errWriteLeadsOutOfWorkspace, "", cerrors.SourceLocation{File: link},
		cerrors.ComposeMessage(ErrWriteLeadsOutOfWorkspace(), values, remedy), nil)
}

// absolute is p made absolute against the working directory and cleaned, the
// spelling the refusal names a path in.
func absolute(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}
