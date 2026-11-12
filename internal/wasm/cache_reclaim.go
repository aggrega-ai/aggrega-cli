// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasm

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/filelock"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegment"
)

// DefaultCompileCacheCapBytes is the size the compiled-module cache is held to
// when no cap is configured: 53687091200 bytes (50 GiB).
//
// The cache holds machine code for every distinct guest a machine has ever
// compiled, and a single entry reaches hundreds of megabytes, so a handful of
// guest versions is gigabytes. Nothing shrinks it on its own — a compiled entry
// stays until something removes it — so without a cap the store grows for as
// long as the machine keeps building. The default is deliberately generous: it
// is a ceiling that stops unbounded growth rather than a budget meant to be
// felt, so a machine doing ordinary work never reaches it and never pays a
// recompile it would not otherwise have paid.
const DefaultCompileCacheCapBytes int64 = 50 << 30

// compileCacheReclaimLockName is the lock file reclamation holds while it trims
// the compiled-module cache, so several processes sharing one cache directory do
// not each walk and trim it at the same time.
//
// The name is deliberately of the same shape as the per-guest compile-lock
// sidecars (a `.compile-` prefix and a `.lock` suffix), which is what keeps it
// out of the cache-content census: the leaf-membership test excludes that shape
// already, so the lock file is never counted toward the total and never a
// candidate for removal. "reclaim" is not a content digest, so the name cannot
// collide with a per-guest lock file.
const compileCacheReclaimLockName = ".compile-reclaim.lock"

// guestCompileLockName is the name of the lock file one guest's compile takes in
// the compiled-module cache directory: the `.compile-` prefix, the guest's
// content digest without its algorithm prefix, and the `.lock` suffix — the shape
// the leaf-membership test excludes. The digest is spelled through the one
// spelling of a derived name (pathsegment.Encode), which writes a lowercase hex
// digest as itself and keeps any other key to one name every platform accepts.
// Each platform that locks names its lock file here, so two of them cannot name
// one guest's lock differently.
func guestCompileLockName(key string) string {
	return ".compile-" + pathsegment.Encode(digest.BareHex(key)) + ".lock"
}

// compileCacheCapResolver holds the installed cap resolver, or nothing when none
// was installed. See SetCompileCacheCapResolver for why the cap arrives as a
// function rather than a value.
var compileCacheCapResolver atomic.Value

// SetCompileCacheCapResolver installs the function that reports the size cap the
// compiled-module cache is held to. It is called once, at process start, before
// any plugin is loaded; with no resolver installed the cap is
// DefaultCompileCacheCapBytes.
//
// The cap arrives as a function rather than a value because resolving it can
// mean reading configuration off disk, and a process that never loads a plugin
// must not pay for that. The function is called at most once per process — only
// if and when a persistent on-disk cache is actually constructed — so a command
// that touches no guest resolves no configuration.
//
// A nil resolver is ignored rather than clearing an installed one, so a caller
// cannot accidentally disarm the cap by passing an unresolved value.
func SetCompileCacheCapResolver(resolve func() int64) {
	if resolve == nil {
		return
	}
	compileCacheCapResolver.Store(resolve)
}

// compileCacheCapBytes reports the cap to hold the compiled-module cache to,
// asking the installed resolver and falling back to the built-in default when
// none was installed. The fallback is what caps the cache for every caller that
// installs no resolver at all.
func compileCacheCapBytes() int64 {
	if fn, ok := compileCacheCapResolver.Load().(func() int64); ok && fn != nil {
		return fn()
	}
	return DefaultCompileCacheCapBytes
}

// CompileCacheLeaf is one entry of the compiled-module cache: its label (the
// path relative to the cache directory, forward-slashed), its absolute path, its
// size on disk, and the time it was compiled.
//
// ModTime orders reclamation — oldest-compiled first — and is never rendered, so
// a reclamation report carries no raw timestamps and the selection stays a pure
// function of the on-disk entry set.
type CompileCacheLeaf struct {
	Label   string
	Path    string
	Size    int64
	ModTime time.Time
}

// CompileCacheReclaim reports one reclamation pass: the entry total before it
// ran, the cap it was held to, the entries removed in removal order, the entries
// it had to leave because another process held them open, and the resulting
// accounting. A pass that found the cache already within its cap removes nothing
// and reports Removed empty.
type CompileCacheReclaim struct {
	// Total is the byte total of the cache entries before the pass.
	Total int64
	// Cap is the cap the pass held the cache to.
	Cap int64
	// Removed is the entries removed, in removal order (oldest-compiled first).
	Removed []CompileCacheLeaf
	// HeldOpen is the entries the pass would have removed but left in place,
	// in the order it reached them, because another process held them open and
	// the platform refuses to remove an open file. Only Windows refuses, so this
	// is empty everywhere else. Each one still counts toward Remaining.
	HeldOpen []CompileCacheLeaf
	// Reclaimed is the bytes the removals freed.
	Reclaimed int64
	// Remaining is the byte total left after the pass.
	Remaining int64
}

// WithinCapBefore reports whether the cache already fit its cap, so nothing was
// removed. It reads the pass's own inputs rather than counting removals, so it
// distinguishes "already within cap" from "trimmed to exactly the cap".
func (r CompileCacheReclaim) WithinCapBefore() bool {
	return r.Cap <= 0 || r.Total <= r.Cap
}

// isCompileCacheLeaf reports whether a non-directory filename beneath the
// compiled-module cache is cache content — a compiled module worth counting and
// reclaiming. Two kinds of file are excluded because they are not compiled
// modules: the transient per-guest lock sidecars that serialize concurrent
// compiles, and the half-written `*.tmp` staging files an in-flight compile
// writes before renaming into place. Both come and go with a compile in progress
// and neither is finished content, so excluding them means reclamation cannot
// disturb a compile that is running right now.
func isCompileCacheLeaf(name string) bool {
	if strings.HasPrefix(name, ".compile-") && strings.HasSuffix(name, ".lock") {
		return false
	}
	if strings.HasSuffix(name, ".tmp") {
		return false
	}
	return true
}

// CollectCompileCacheLeaves enumerates the compiled-module cache entries beneath
// dir, each with the size and compile time reclamation orders by. It walks the
// whole tree, so the version-and-architecture subdirectories the runtime
// namespaces its entries under are all covered — including one left behind by a
// runtime version no longer in use, which is exactly the oldest content.
//
// A directory that does not exist is an empty entry set rather than an error, so
// a caller can ask about a cache that was never populated. A directory that is
// itself a link — the machine's owner keeping the cache elsewhere — is walked
// where it leads (pathscope.WalkDir), where the runtime compiles into it, each
// leaf's path spelled beneath dir: walked as the link alone it would be one leaf
// whose path is the link, the first thing a reclamation removes.
func CollectCompileCacheLeaves(dir string) ([]CompileCacheLeaf, error) {
	if dir == "" {
		return nil, nil
	}
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []CompileCacheLeaf
	err := pathscope.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !isCompileCacheLeaf(d.Name()) {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			rel = p
		}
		out = append(out, CompileCacheLeaf{
			Label:   filepath.ToSlash(rel),
			Path:    p,
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ReclaimCompileCache holds the compiled-module cache at dir to capBytes,
// removing entries oldest-compiled first until the remainder fits. It is the one
// implementation of capped reclamation: the cache-maintenance command and the
// automatic enforcement both call it, so the two can never disagree about which
// entries a cap removes.
//
// Order is oldest-compiled first — modification time ascending, with the entry
// label ascending as the tie-break — which makes the selection deterministic for
// a given on-disk state. Compile time is what is available: reading an entry does
// not touch it, so "oldest" means least recently compiled rather than least
// recently used.
//
// A cap of zero or less means unbounded: the total is still reported but nothing
// is removed. A cache already within its cap is reported and left alone.
//
// Removing an entry is safe while other processes are reading the same cache
// directory, and that is the property the whole design rests on. An entry is
// looked up by opening it, so a caller either opens it before it is removed —
// and the open file survives the removal, because the bytes stay reachable
// through the open handle until it is closed — or does not find it, which is an
// ordinary cache miss that recompiles. There is no window in which a caller
// finds an entry and then fails to read it. The cost of removing an entry
// something still wants is therefore a recompile, never a failure. In-flight
// compiles are untouched on top of that: the staging files and lock sidecars a
// compile in progress writes are not cache content and are never removed.
//
// Windows keeps the same guarantee by a different route: it refuses to remove a
// file another process has open, so an entry being read cannot be removed at all.
// The pass leaves such an entry in place, reports it in HeldOpen, and goes on to
// the next-oldest entry, so the cap is still held wherever the rest of the cache
// allows. Leaving it is not a failure: the entry is in use, which is the one
// thing that makes it worth keeping, and a later pass reclaims it once nothing
// holds it.
//
// Any other removal failure is returned loudly rather than swallowed, together
// with the partial accounting of what had already been removed, so a caller can
// report the work that did happen.
func ReclaimCompileCache(dir string, capBytes int64) (CompileCacheReclaim, error) {
	if dir == "" {
		return CompileCacheReclaim{}, fmt.Errorf("reclaim compiled-module cache: no cache directory")
	}
	leaves, err := CollectCompileCacheLeaves(dir)
	if err != nil {
		return CompileCacheReclaim{}, fmt.Errorf("reclaim compiled-module cache: enumerate %s: %w", dir, err)
	}
	res := CompileCacheReclaim{Cap: capBytes}
	for _, l := range leaves {
		res.Total += l.Size
	}
	res.Remaining = res.Total
	if res.WithinCapBefore() {
		return res, nil
	}
	sort.Slice(leaves, func(i, j int) bool {
		if !leaves[i].ModTime.Equal(leaves[j].ModTime) {
			return leaves[i].ModTime.Before(leaves[j].ModTime)
		}
		return leaves[i].Label < leaves[j].Label
	})
	for _, l := range leaves {
		if res.Remaining <= capBytes {
			break
		}
		if rmErr := os.Remove(l.Path); rmErr != nil {
			if filelock.HeldOpen(rmErr) {
				res.HeldOpen = append(res.HeldOpen, l)
				continue
			}
			return res, fmt.Errorf("reclaim compiled-module cache: remove %s: %w", l.Path, rmErr)
		}
		res.Removed = append(res.Removed, l)
		res.Reclaimed += l.Size
		res.Remaining -= l.Size
	}
	return res, nil
}

// reclaimCompileCacheAtStartup holds the compiled-module cache at dir to its
// configured cap. It runs exactly once per process, from the construction of the
// process-wide compilation cache, which is what makes the timing safe on both
// axes that matter.
//
// It is not on the compile path. Construction happens once, before this process
// compiles or reads anything, so the pass cannot remove an entry this process is
// about to use — and it never runs again, so no compile pays for it. That is the
// whole reason the reclamation point is here rather than after a compile
// installs an entry: a per-compile pass would walk the cache on the hot path,
// and would do it at the one moment entries are being written.
//
// Other processes sharing the directory are unaffected for the reason
// ReclaimCompileCache documents: a removed entry is a cache miss that
// recompiles, never a read that fails. The lock this takes is therefore an
// efficiency measure and not a safety one — it keeps several processes starting
// at once from each walking and trimming the same directory — which is why a
// process that cannot take it skips the pass entirely instead of waiting. It is
// its own lock, separate from the per-guest compile locks, so reclamation never
// blocks a compile and a compile never blocks reclamation.
//
// Running before this process compiles has one consequence worth stating,
// because it looks like a defect and is not: the cap bounds what this process
// INHERITS, not what it leaves behind. A process whose own work needs entries the
// pass removed recompiles them and writes them back, so the store can sit above
// the cap once the process has finished, and is trimmed again at the start of the
// next one. The cap is therefore a ceiling on accumulation across runs rather
// than an instant-by-instant bound — which is the only kind of cap that can be
// held without ever taking an entry away from work already underway. It also
// means a cap set below what a single run compiles makes every run discard and
// recompile the same modules, so a cap belongs above the working set.
//
// The pass is silent. Every byte it removes is re-derivable by recompiling, so
// there is nothing a caller must act on, and `aggrega cache status` reports the
// cache's size whenever someone wants to see it. Failures are swallowed for the
// same reason: a cache that could not be trimmed is a cache that is larger than
// asked for, which is never a reason to fail the work the process was started to
// do.
func reclaimCompileCacheAtStartup(dir string) {
	if dir == "" {
		return
	}
	capBytes := compileCacheCapBytes()
	if capBytes <= 0 {
		return
	}
	compileCacheCapAppliedTo.Store(dir)
	reclaimCompileCacheUnderLock(dir, capBytes)
}

// compileCacheCapAppliedTo records the compiled-module cache directory a cap was
// held over when this process opened it. It stays empty when no on-disk cache was
// opened and when the configured cap is the uncapped one, so it reports that a cap
// was genuinely in force rather than merely that a directory was resolved.
//
// It exists so that "the store this process uses is capped" is an observable fact
// rather than an inference. Which store that is depends on how the process was
// started, so the answer cannot be pinned to a fixed path; and a lock file left in
// the directory would not distinguish this process's pass from one an earlier
// process made against the same shared directory.
var compileCacheCapAppliedTo atomic.Value

// reclaimCompileCacheUnderLock is the cap-holding pass itself, taking the cap as
// an argument: it takes the reclamation lock, trims, and releases. It is separate
// from reclaimCompileCacheAtStartup only so the pass can be exercised against a
// caller-chosen directory and cap. That separation matters because the cap the
// startup pass reads is process-global: a test that installed a small one to
// drive a trim would be arming it for every other cache in the process,
// including the one a whole test run shares, whose entries are expensive to
// rebuild.
func reclaimCompileCacheUnderLock(dir string, capBytes int64) {
	if dir == "" || capBytes <= 0 {
		return
	}
	release, ok := flockCompileCacheReclaim(dir)
	if !ok {
		return
	}
	defer release()
	_, _ = ReclaimCompileCache(dir, capBytes)
}
