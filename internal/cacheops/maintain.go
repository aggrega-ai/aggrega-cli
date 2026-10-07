// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cacheops

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/attribindex"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/filelock"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// errPruneNeedsWorkspace is returned when a prune runs with no resolvable
// workspace to draw a reference set from.
//
// It is a package-local error value and not a registered halting condition, which
// is what the two refusals beside it in Prune are as well — no lockfile under the
// workspace, and no candidate that parsed as one. A prune reports its failures as
// plain errors and builds no diagnostic envelope, so none of the three carries a
// short code a reader could quote, and naming one of them under the condition
// naming convention would promise an identifier no surface prints. It stays a
// declared value rather than an inline one so a caller matches this refusal by
// identity instead of by its message text.
var errPruneNeedsWorkspace = errors.New("cache prune needs a workspace, but none could be resolved")

// Removal is one element an operation removed — or, in a result's HeldOpen, one
// it would have removed and had to leave — named by the class it belonged to and
// carrying that class's reach.
//
// Reach travels with the removal rather than being looked up again by the
// reader, so a caller reporting what was removed cannot describe a machine-wide
// removal as workspace-local by consulting the wrong table.
type Removal struct {
	Class string `json:"class"`
	Label string `json:"label"`
	Reach Reach  `json:"reach"`
	Size  int64  `json:"size"`
}

// Shortfall is a set of elements an operation left behind because it has no
// basis to reclaim them, with the verb that would.
//
// It is reported rather than folded into the retained count because the two mean
// different things: a retained element is one the operation examined and kept,
// and a shortfall is one it could not judge at all.
type Shortfall struct {
	Class string `json:"class"`
	Count int    `json:"count"`
	Size  int64  `json:"size"`
	Reach Reach  `json:"reach"`
	// ReclaimedBy names the operations that can reclaim the set, in the
	// vocabulary a caller invokes them under.
	ReclaimedBy []string `json:"reclaimedBy"`
}

// PruneResult is what a prune did: the elements it removed in removal order, the
// totals, the elements it had no basis to judge, and the classes it cannot
// reclaim at all.
//
// MachineWide names the classes among those it acted on whose contents are
// shared across every project on the machine. It is computed from the classes
// rather than asserted, so it cannot understate the reach of what was removed.
//
// HeldOpen is the elements the prune would have removed but left in place because
// another process held them open, which only Windows refuses; it is empty
// everywhere else. Such an element is counted as neither removed nor retained,
// and a later prune reclaims it. One that is a directory may have lost the files
// in it that were not open; none of its bytes count as reclaimed.
type PruneResult struct {
	Removed           []Removal   `json:"removed"`
	RetainedCount     int         `json:"retainedCount"`
	ReclaimedBytes    int64       `json:"reclaimedBytes"`
	UnattributedCount int         `json:"unattributedCount"`
	UnattributedSize  int64       `json:"unattributedSize"`
	NotPruneEligible  []Shortfall `json:"notPruneEligible"`
	MachineWide       []string    `json:"machineWide"`
	HeldOpen          []Removal   `json:"heldOpen,omitempty"`
}

// Prune removes every prunable element whose content digest is absent from the
// reference set the workspace's lockfiles pin, and reports what it did.
//
// It halts loudly when no workspace or no lockfile is present rather than
// pruning against an empty reference set, which would evict every entry.
//
// A removal failure part-way through returns both the error and the result
// assembled so far, so a caller renders the removals that did happen instead of
// reporting a partial prune as though nothing had been removed. A stop is
// reported the same way and for the same reason: the removals already performed
// cannot be undone, so a withdrawn prune reports what it did rather than
// claiming it did nothing.
func Prune(ctx context.Context, loc Locations) (PruneResult, error) {
	var res PruneResult
	if loc.WorkspaceRoot == "" {
		return res, errPruneNeedsWorkspace
	}

	lockPaths := FindLockfiles(loc.WorkspaceRoot, loc.CacheDir)
	if len(lockPaths) == 0 {
		return res, fmt.Errorf("cache prune needs a pulled workspace: no lockfiles (*.lock) found under %s; run `aggrega pull --release <release>` first", loc.WorkspaceRoot)
	}
	refset, loaded, err := BuildReferenceSet(loc.WorkspaceRoot, lockPaths)
	if err != nil {
		return res, err
	}
	if loaded == 0 {
		return res, fmt.Errorf("cache prune found %d lockfile candidate(s) under %s but none parsed as a lockfile; run `aggrega pull --release <release>` first", len(lockPaths), loc.WorkspaceRoot)
	}

	// Prune every prunable class, drawn from the single class-set assembly. The
	// describe cache is included because a describe entry's filename stem is the
	// guest's content digest, which equals the pin a lockfile plugin entry records
	// — so it maps to the same reference set. The compiled-module cache is prunable
	// too, but by attribution rather than by an element digest, so it is handled by
	// a dedicated arm that consults the attribution index directly.
	//
	// The roots a prune removes from are held before anything beneath them is read,
	// so a cache directory linked out of the workspace, or a class root linked out of
	// the cache directory, is refused as the removal it would be, with nothing listed
	// through it.
	if err := holdWorkspaceClassRoots(loc, loc.workspaceClasses(), true); err != nil {
		return res, err
	}
	classes, err := Collect(ctx, loc)
	if err != nil {
		return res, err
	}
	ax, _ := attribindex.Load(loc.AttribIndexPath)
	res.MachineWide = MachineWideClasses(classes)

	// Reconciliation inputs: the leaf keys removed, and the guest referrer digests
	// those removals drop, so a surviving kernel's referrer set no longer counts a
	// pruned guest.
	var removedLeafKeys, droppedReferrers []string
	for _, cl := range classes {
		if !cl.Prunable {
			continue
		}
		if cl.Name == CompiledModuleClassName {
			for _, el := range cl.Elements {
				// The interruption point sits ahead of the removal, so a stop leaves the
				// element on disk. A removal cannot be undone, which is why the check is
				// here rather than after the loop.
				if serr := stopped(ctx); serr != nil {
					return res, serr
				}
				entry, attributed := ax.Entry(el.Label)
				if !attributed {
					// A compiled leaf whose attribution cannot be determined is retained on
					// purpose — never removed on a guess — and reported apart from the
					// retained count so that count is read correctly.
					res.UnattributedCount++
					res.UnattributedSize += el.Size
					continue
				}
				if anyReferrerInSet(entry.Referrers, refset) {
					res.RetainedCount++
					continue
				}
				if rmErr := os.Remove(el.Path); rmErr != nil {
					if filelock.HeldOpen(rmErr) {
						res.HeldOpen = append(res.HeldOpen, Removal{Class: cl.Name, Label: el.Label, Reach: cl.Reach, Size: el.Size})
						continue
					}
					return res, fmt.Errorf("cache prune: remove %s: %w", el.Path, rmErr)
				}
				removeEmptyParents(el.Path, cl.Root)
				res.Removed = append(res.Removed, Removal{Class: cl.Name, Label: el.Label, Reach: cl.Reach, Size: el.Size})
				res.ReclaimedBytes += el.Size
				removedLeafKeys = append(removedLeafKeys, el.Label)
				if !entry.Kernel {
					droppedReferrers = append(droppedReferrers, entry.Referrers...)
				}
			}
			continue
		}
		for _, el := range cl.Elements {
			// As above: ahead of the removal, so a stop leaves the element on disk.
			if serr := stopped(ctx); serr != nil {
				return res, serr
			}
			if el.DigestHex == "" || refset[el.DigestHex] {
				res.RetainedCount++
				continue
			}
			if err := holdRemoval(loc, cl, el.Path); err != nil {
				return res, err
			}
			held, rmErr := removeAllLeavingHeldOpen(el.Path)
			if rmErr != nil {
				return res, fmt.Errorf("cache prune: remove %s: %w", el.Path, rmErr)
			}
			if held.Files > 0 {
				res.HeldOpen = append(res.HeldOpen, Removal{Class: cl.Name, Label: el.Label, Reach: cl.Reach, Size: el.Size})
				continue
			}
			removeEmptyParents(el.Path, cl.Root)
			res.Removed = append(res.Removed, Removal{Class: cl.Name, Label: el.Label, Reach: cl.Reach, Size: el.Size})
			res.ReclaimedBytes += el.Size
		}
	}

	// Reconcile the attribution index so it no longer references removed leaves and
	// a surviving kernel's referrer set no longer counts a pruned guest. This is
	// index hygiene, not prune correctness — but it keeps the shared-by accounting
	// honest for the next inspection.
	if len(removedLeafKeys) > 0 && loc.AttribIndexPath != "" {
		if err := attribindex.RemoveLeaves(loc.AttribIndexPath, removedLeafKeys, droppedReferrers); err != nil {
			return res, fmt.Errorf("cache prune: reconcile attribution index: %w", err)
		}
	}

	// The classes a prune cannot reclaim at all, so the retained count above is not
	// read against the full set.
	for _, cl := range classes {
		if cl.Prunable || len(cl.Elements) == 0 {
			continue
		}
		s := Shortfall{Class: cl.Name, Reach: cl.Reach, ReclaimedBy: []string{"cache-clear"}}
		for _, el := range cl.Elements {
			s.Count++
			s.Size += el.Size
		}
		res.NotPruneEligible = append(res.NotPruneEligible, s)
	}
	return res, nil
}

// holdWorkspaceClassRoots holds every workspace class root an operation would
// remove from or clear to the root the workspace's cache directory picks — the
// cache directory itself — before anything is removed, and before anything beneath
// them is enumerated.
//
// The workspace's cache classes sit under the workspace's own cache directory, so
// a class root that is a link leading out of that directory — elsewhere inside the
// workspace as much as anywhere else — would turn a clear or a prune into deleting
// files the cache does not own, and a cache directory leading out of the workspace
// would turn one into deleting directories elsewhere on the machine. One class
// root leading out halts the operation with every class left as it was. The
// machine-wide classes are the machine's own stores and are not held to any
// workspace, which is why only the workspace's classes are passed here.
// prunableOnly narrows the set to the classes a prune reclaims from, which are the
// only ones a prune removes anything under.
func holdWorkspaceClassRoots(loc Locations, classes []workspaceClass, prunableOnly bool) error {
	for _, wc := range classes {
		if prunableOnly && !wc.Prunable {
			continue
		}
		if err := holdRemoval(loc, wc.Class, wc.Root); err != nil {
			return err
		}
	}
	return nil
}

// holdRemoval holds one removal from class cl — its root, or one element of it —
// to the root the cache directory loc.CacheDir picks for the workspace at
// loc.WorkspaceRoot (writescope.RootForCache): the cache directory itself, the
// root its writes and its reads are held to as well — or the workspace, where a
// cache directory spelled inside it leads out of it. A machine-wide class, and a
// path in a run that resolved no workspace, are held to nothing.
func holdRemoval(loc Locations, cl Class, path string) error {
	if cl.Reach != ReachWorkspace || cl.Root == "" {
		return nil
	}
	return writescope.CheckDelete(writescope.RootForCache(loc.WorkspaceRoot, loc.CacheDir), path)
}

// anyReferrerInSet reports whether any of a compiled leaf's referrer digests is
// present in the lockfile reference set. Both sides are normalized (prefix-free
// lowercase hex), so membership is a direct lookup.
func anyReferrerInSet(referrers []string, refset map[string]bool) bool {
	for _, r := range referrers {
		if refset[r] {
			return true
		}
	}
	return false
}

// EvictResult is what an eviction did: the leaves it removed, the totals, and the
// cap it was held to.
//
// WithinCapBefore reports that the cache already fitted the cap, in which case
// nothing was removed. Reach is stated outright because eviction addresses one
// class and that class is machine-wide — there is no set of classes to derive a
// union from.
//
// HeldOpen is the leaves the trim would have removed but left in place because
// another process held them open, which only Windows refuses; they still count
// toward RemainingBytes, and the trim went on to the next-oldest leaf instead.
type EvictResult struct {
	WithinCapBefore bool      `json:"withinCapBefore"`
	Removed         []Removal `json:"removed"`
	TotalBefore     int64     `json:"totalBefore"`
	ReclaimedBytes  int64     `json:"reclaimedBytes"`
	RemainingBytes  int64     `json:"remainingBytes"`
	CapBytes        int64     `json:"capBytes"`
	MachineWide     []string  `json:"machineWide"`
	HeldOpen        []Removal `json:"heldOpen,omitempty"`
}

// Evict trims the compiled-module cache to capBytes and reports what it did.
//
// The selection and the removal are not decided here: both come from the shared
// capped-reclamation routine, the same one that applies the configured cap
// automatically. This only assembles the outcome, so the operation and the
// automatic cap can never disagree about which leaves a given cap removes. What
// that routine guarantees — removal oldest-compiled first with a deterministic
// tie-break, in-flight compiles left alone, and a removed entry costing a later
// recompile rather than a failed read — is documented once, there.
//
// A removal failure returns both the error and the leaves removed before it, so
// a partial trim is never silent.
func Evict(ctx context.Context, loc Locations, capBytes int64) (EvictResult, error) {
	out := EvictResult{
		CapBytes:    capBytes,
		MachineWide: []string{CompiledModuleClassName},
	}
	if loc.CompileDir == "" {
		return out, errors.New("cache evict: no compiled-module cache directory could be resolved")
	}
	// The reclamation is one call that selects and removes without reporting
	// progress, so the last point a stop can be observed is before it starts.
	// Nothing here can interrupt it part-way, and a check afterwards would
	// withhold the reply while the leaves were already gone.
	if serr := stopped(ctx); serr != nil {
		return out, serr
	}
	res, err := wasm.ReclaimCompileCache(loc.CompileDir, capBytes)
	out.TotalBefore = res.Total
	out.WithinCapBefore = res.WithinCapBefore()
	out.Removed = compiledModuleRemovals(res.Removed)
	out.HeldOpen = compiledModuleRemovals(res.HeldOpen)
	out.ReclaimedBytes = res.Reclaimed
	out.RemainingBytes = res.Remaining
	if err != nil {
		return out, fmt.Errorf("cache evict: %w", err)
	}
	return out, nil
}

// compiledModuleRemovals names compiled-module cache leaves as elements of their
// class, each carrying the class's machine-wide reach.
func compiledModuleRemovals(leaves []wasm.CompileCacheLeaf) []Removal {
	var out []Removal
	for _, l := range leaves {
		out = append(out, Removal{
			Class: CompiledModuleClassName,
			Label: l.Label,
			Reach: ReachMachine,
			Size:  l.Size,
		})
	}
	return out
}

// ClearedClass is one whole cache subtree a clear removed, with its reach and the
// bytes the clear reclaimed from it.
//
// HeldOpenFiles and HeldOpenBytes count the files the clear had to leave in the
// subtree because another process held them open, which only Windows refuses —
// a compile's lock file or staging file, or a module being read. They are zero
// everywhere else, and a later clear removes what they count.
type ClearedClass struct {
	Class         string `json:"class"`
	Reach         Reach  `json:"reach"`
	Size          int64  `json:"size"`
	HeldOpenFiles int    `json:"heldOpenFiles,omitempty"`
	HeldOpenBytes int64  `json:"heldOpenBytes,omitempty"`
}

// ClearResult is what a total wipe did: every subtree it removed and the bytes it
// reclaimed.
//
// MachineWide is computed from the classes actually removed, so a clear that
// found no machine-wide subtree does not claim to have reached one.
type ClearResult struct {
	Cleared        []ClearedClass `json:"cleared"`
	ReclaimedBytes int64          `json:"reclaimedBytes"`
	MachineWide    []string       `json:"machineWide"`
}

// Clear removes every cache subtree. Each subtree is sized before removal so the
// result states what was reclaimed. Absent subtrees are no-ops.
//
// The workspace's class roots are held to the cache directory before anything is
// listed or removed (holdWorkspaceClassRoots), so a class root that is a link
// leading out of the cache directory — elsewhere inside the workspace as much as
// anywhere else — refuses the clear and nothing is removed. A subtree whose root is
// a link to a directory that the hold admits — a workspace class root leading
// elsewhere inside the cache directory, or a machine-wide store its owner keeps
// elsewhere — is the directory it leads to, which is where the inventory lists it:
// the clear empties that directory, keeps the link, and reports the bytes it
// reclaimed there (clearClassRoot).
//
// A file another process holds open is left in its subtree and counted rather
// than failing the clear, so every subtree is still cleared of everything else.
// Only Windows refuses that removal; elsewhere the open file is removed like any
// other.
//
// A removal failure returns both the error and the subtrees cleared before it,
// and a stop is reported the same way: the subtrees already removed cannot be
// restored, so a withdrawn clear reports what it removed.
func Clear(ctx context.Context, loc Locations) (ClearResult, error) {
	var res ClearResult
	// The roots a clear removes are held before anything beneath them is read, so a
	// cache directory linked out of the workspace, or a class root linked out of the
	// cache directory, is refused as the removal it would be, with nothing listed
	// through it.
	if err := holdWorkspaceClassRoots(loc, loc.workspaceClasses(), false); err != nil {
		return res, err
	}
	classes, err := Collect(ctx, loc)
	if err != nil {
		return res, err
	}
	for _, cl := range classes {
		// Ahead of the removal, so a stop leaves the subtree on disk.
		if serr := stopped(ctx); serr != nil {
			return res, serr
		}
		if cl.Root == "" {
			continue
		}
		if _, err := os.Stat(cl.Root); err != nil {
			continue
		}
		size := dirSize(cl.Root)
		held, rmErr := clearClassRoot(cl.Root)
		if rmErr != nil {
			return res, fmt.Errorf("cache clear: remove %s: %w", cl.Root, rmErr)
		}
		reclaimed := size - held.Bytes
		res.Cleared = append(res.Cleared, ClearedClass{
			Class:         cl.Name,
			Reach:         cl.Reach,
			Size:          reclaimed,
			HeldOpenFiles: held.Files,
			HeldOpenBytes: held.Bytes,
		})
		if cl.Reach == ReachMachine {
			res.MachineWide = append(res.MachineWide, cl.Name)
		}
		res.ReclaimedBytes += reclaimed
	}
	return res, nil
}

// clearClassRoot removes what a clear reclaims from the subtree rooted at root: the
// root and everything beneath it, or — where root is a link to a directory — every
// entry of the directory the link leads to, the link itself kept. It reports what it
// had to leave because another process held it open, as removeAllLeavingHeldOpen
// does.
//
// A root that is a link is the directory it leads to: the inventory lists what lies
// there, and dirSize sizes it there, so removing the link instead would report as
// reclaimed bytes that are still on disk, leave in place everything the inventory
// had just listed, and take away the link its owner placed. Each entry is removed
// as removeAllLeavingHeldOpen removes a root, so a link met beneath is removed as
// the link it is and never entered.
//
// It decides nothing about where a root may lead: a workspace class root reaches
// here only once the hold has found it inside the cache directory, and a
// machine-wide store is one its owner names, emptied where it leads.
func clearClassRoot(root string) (heldOpenFiles, error) {
	if !linksToDirectory(root) {
		return removeAllLeavingHeldOpen(root)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return heldOpenFiles{}, err
	}
	var held heldOpenFiles
	for _, e := range entries {
		h, rmErr := removeAllLeavingHeldOpen(filepath.Join(root, e.Name()))
		held.Files += h.Files
		held.Bytes += h.Bytes
		if rmErr != nil {
			return held, rmErr
		}
	}
	return held, nil
}

// linksToDirectory reports whether path is itself a symbolic link that leads to a
// directory.
func linksToDirectory(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&fs.ModeSymlink == 0 {
		return false
	}
	target, err := os.Stat(path)
	return err == nil && target.IsDir()
}
