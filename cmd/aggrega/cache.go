// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/attribindex"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/provindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/metadata"
)

// newCacheCmd builds the `aggrega cache` command family: inspect, reclaim, and
// wipe the on-disk caches the compiler populates. Three caches exist:
//
//   - the workspace artifact cache under the configured cache directory
//     (default `.aggrega/cache`), holding pulled catalog entities, resolved
//     source artifacts, and pinned external inputs;
//   - the compiled-module cache, holding provider guests compiled once and
//     reused across every project on the machine;
//   - the plugin-describe cache, holding each guest's static self-report keyed
//     by the guest's content digest.
func newCacheCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "cache",
		Short: "Inspect and maintain the on-disk caches",
		Long: `Inspect and maintain the caches the compiler populates.

status  lists cache contents, one element per line, then a summary line.
prune   reclaims artifact-cache entries no workspace lockfile references.
evict   trims the compiled-module cache to a size cap, oldest first.
clear   wipes every cache, including the compiled-module store.`,
	}
	c.AddCommand(newCacheStatusCmd(), newCachePruneCmd(), newCacheEvictCmd(), newCacheClearCmd())
	return c
}

// cacheElement is one enumerable unit of a cache class: a slot directory, a
// stored blob, or a compiled module. label is the deterministic display path
// within its class; path is the filesystem entry a prune/clear removes; size
// is the element's total byte size; digestHex is the lowercase, prefix-free
// content digest that maps the element to a lockfile reference, or "" when the
// element carries no reproducible content digest (a compiled module keyed by
// the runtime's own internal hashing); note is an optional trailing annotation
// shown in the status listing (used by compiled modules to report their
// attributed identity, the shared runtime kernel, or an unattributed leaf) and
// is empty for classes that carry no annotation.
type cacheElement struct {
	label     string
	path      string
	size      int64
	digestHex string
	note      string
}

// cacheClass is one named cache subtree with its enumerated elements. prunable
// records whether prune may reclaim the class: true for the digest-addressable
// classes whose elements map to a lockfile reference, false for a class keyed by
// content the compiler cannot map to a lockfile (the compiled-module store,
// keyed by the runtime's own internal hashing), which only clear reclaims.
type cacheClass struct {
	name     string
	root     string
	prunable bool
	elements []cacheElement
}

// cacheLocations resolves every cache root for the running command. wsRoot and
// cacheDir are empty when no workspace resolved, in which case only the two
// global caches are addressable.
type cacheLocations struct {
	wsRoot   string
	cacheDir string

	compileDir  string
	describeDir string

	provIndexPath   string
	attribIndexPath string
}

// resolveCacheLocations resolves the workspace (best-effort) and the two
// global cache directories. A workspace-resolution failure is not fatal: the
// caller still operates on the global caches. The two global directories may
// be absent (no resolvable user cache directory), reported by their empty
// return.
func resolveCacheLocations(ctx context.Context) cacheLocations {
	var loc cacheLocations
	if ws, err := workspaceFromContext(ctx); err == nil {
		loc.wsRoot = ws.Root()
		loc.cacheDir = ws.Config().ResolvedArtifactCacheDir()
	}
	if dir, ok := wasm.CompileCacheDir(); ok {
		loc.compileDir = dir
	}
	if dir, ok := wasm.PluginDescribeCacheDir(); ok {
		loc.describeDir = dir
	}
	if p, ok := provindex.DefaultIndexPath(); ok {
		loc.provIndexPath = p
	}
	if root, ok := wasm.AggregaCacheRoot(); ok {
		loc.attribIndexPath = attribindex.DefaultIndexPath(root)
	}
	return loc
}

// entitiesRoot returns the workspace entity-cache subtree root, or "" when no
// workspace resolved.
func (l cacheLocations) entitiesRoot() string {
	if l.wsRoot == "" {
		return ""
	}
	return oci.EntitiesRoot(l.wsRoot, l.cacheDir)
}

// sourceArtifactsRoot returns the workspace source-artifact-cache subtree
// root, or "" when no workspace resolved.
func (l cacheLocations) sourceArtifactsRoot() string {
	if l.wsRoot == "" {
		return ""
	}
	return cacheslot.SourceArtifactsRoot(l.wsRoot, l.cacheDir)
}

// inputsRoot returns the workspace external-input store root, or "" when no
// workspace resolved.
func (l cacheLocations) inputsRoot() string {
	if l.wsRoot == "" {
		return ""
	}
	return externalinputs.StoreRoot(filepath.Join(l.wsRoot, l.cacheDir))
}

// docsBundlesRoot returns the workspace documentation-bundle-cache subtree
// root, or "" when no workspace resolved.
func (l cacheLocations) docsBundlesRoot() string {
	if l.wsRoot == "" {
		return ""
	}
	return docscache.DocsBundlesRoot(l.wsRoot, l.cacheDir)
}

// enumEntities enumerates the entity-cache slots. Each slot is an
// <encoded-uri>/<version> directory holding a content blob; the slot's
// content digest is the blob's content digest, which equals the pinned digest
// a lockfile records for that entity, schema library, mapping library, or
// plugin artifact. A slot whose blob cannot be read carries an empty digest
// and is retained by prune rather than removed on a guess.
func enumEntities(root string) []cacheElement {
	var out []cacheElement
	uriDirs, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, ud := range uriDirs {
		if !ud.IsDir() {
			continue
		}
		encoded := ud.Name()
		display := encoded
		if dec, derr := url.PathUnescape(encoded); derr == nil {
			display = dec
		}
		versDirs, verr := os.ReadDir(filepath.Join(root, encoded))
		if verr != nil {
			continue
		}
		for _, vd := range versDirs {
			if !vd.IsDir() {
				continue
			}
			slot := filepath.Join(root, encoded, vd.Name())
			el := cacheElement{
				label: display + "@" + vd.Name(),
				path:  slot,
				size:  dirSize(slot),
			}
			if blob, berr := os.ReadFile(filepath.Join(slot, "blob")); berr == nil {
				el.digestHex = normalizeDigestHex(digest.ContentDigest(blob))
			}
			out = append(out, el)
		}
	}
	sortElements(out)
	return out
}

// enumSourceArtifacts enumerates the source-artifact-cache slots. A slot is any
// directory that directly holds a metadata.yaml; the recorded digest there is
// the authoritative content digest a lockfile source-artifact entry pins (the
// leaf directory name is only a 12-character prefix). A slot whose metadata
// cannot be read carries an empty digest and is retained by prune.
func enumSourceArtifacts(root string) []cacheElement {
	var out []cacheElement
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		metaPath := filepath.Join(p, "metadata.yaml")
		if _, statErr := os.Stat(metaPath); statErr != nil {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			rel = p
		}
		el := cacheElement{
			label: filepath.ToSlash(rel),
			path:  p,
			size:  dirSize(p),
		}
		if meta, merr := metadata.Read(metaPath); merr == nil {
			el.digestHex = normalizeDigestHex(meta.Digest)
		}
		out = append(out, el)
		return nil
	})
	sortElements(out)
	return out
}

// enumInputs enumerates the external-input store blobs. Each blob file is named
// by its lowercase-hex content digest, which is exactly the pin a lockfile
// external-input entry records.
func enumInputs(root string) []cacheElement {
	var out []cacheElement
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			rel = p
		}
		out = append(out, cacheElement{
			label:     filepath.ToSlash(rel),
			path:      p,
			size:      fileSize(p),
			digestHex: normalizeDigestHex(d.Name()),
		})
		return nil
	})
	sortElements(out)
	return out
}

// enumDocsBundles enumerates the documentation-bundle-cache slots. A slot is an
// <encoded-ref>/<version> directory holding a content blob and its manifest. A
// documentation bundle is a standalone data artifact, not a catalog entity, so
// no lockfile references it; the class carries no element digest and is never
// prune-eligible (only clear reclaims it). Enumeration reports each slot's
// decoded reference and version for a stable, deterministic listing.
func enumDocsBundles(root string) []cacheElement {
	var out []cacheElement
	refDirs, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, rd := range refDirs {
		if !rd.IsDir() {
			continue
		}
		encoded := rd.Name()
		display := encoded
		if dec, derr := url.PathUnescape(encoded); derr == nil {
			display = dec
		}
		versDirs, verr := os.ReadDir(filepath.Join(root, encoded))
		if verr != nil {
			continue
		}
		for _, vd := range versDirs {
			if !vd.IsDir() {
				continue
			}
			slot := filepath.Join(root, encoded, vd.Name())
			out = append(out, cacheElement{
				label: display + "@" + vd.Name(),
				path:  slot,
				size:  dirSize(slot),
			})
		}
	}
	sortElements(out)
	return out
}

// isCompileLeaf reports whether a non-directory filename beneath the
// compiled-module cache is a cache leaf — a compiled module worth listing,
// counting, and evicting. It is the single membership test both the status
// enumeration and the eviction enumeration consult, so the two agree on exactly
// which files count as cache content. Two kinds of file are excluded because
// they are not compiled modules: the transient per-guest compile-lock sidecars
// (`.compile-<digest>.lock`) that serialize concurrent compiles, and the
// half-written `*.tmp` transients an in-flight compile stages before its atomic
// rename. Both come and go with a compile in progress and never represent
// finished cache content.
func isCompileLeaf(name string) bool {
	if strings.HasPrefix(name, ".compile-") && strings.HasSuffix(name, ".lock") {
		return false
	}
	if strings.HasSuffix(name, ".tmp") {
		return false
	}
	return true
}

// enumCompileModules enumerates the compiled-module cache. The runtime keys its
// entries by its own internal hashing beneath a version/arch/os-marked
// directory, so a module carries no digest the compiler can reproduce from the
// leaf alone. The compile-attribution index (attribIndexPath), populated by
// observing cold compiles, maps each leaf to the guest content digests that
// depend on it; enumeration consults it to annotate each leaf:
//
//   - an attributed guest leaf carries its sole referrer digest (so a later
//     reference-based reclamation can map it to a lockfile) and, for display,
//     resolves that digest to a URI@version through the provenance index when
//     known, else shows the bare digest;
//   - the shared runtime kernel leaf is labelled as such and reports how many
//     distinct guests share it;
//   - a leaf absent from the index is shown as unattributed.
//
// Non-leaf files — the transient compile-lock sidecars and half-written .tmp
// transients — are skipped per isCompileLeaf; they are not cache content. The
// class remains non-prunable here: attributing leaves is a reporting concern,
// not a prune-scope change.
func enumCompileModules(root, attribIndexPath, provIndexPath string) []cacheElement {
	ax, _ := attribindex.Load(attribIndexPath)
	identities := provenanceIdentities(provIndexPath)
	var out []cacheElement
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !isCompileLeaf(d.Name()) {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			rel = p
		}
		leafKey := filepath.ToSlash(rel)
		el := cacheElement{label: leafKey, path: p, size: fileSize(p)}
		if e, ok := ax.Entry(leafKey); ok {
			switch {
			case e.Kernel:
				el.note = fmt.Sprintf("shared runtime kernel, shared by %d", len(e.Referrers))
			case len(e.Referrers) > 0:
				dg := e.Referrers[0]
				el.digestHex = dg
				if name, known := identities[dg]; known && name != "" {
					el.note = name
				} else {
					el.note = dg
				}
			default:
				el.note = "unattributed"
			}
		} else {
			el.note = "unattributed"
		}
		out = append(out, el)
		return nil
	})
	sortElements(out)
	return out
}

// provenanceIdentities builds a lookup from a normalized guest content digest to
// its display identity — the comma-joined URI@version sources the provenance
// index recorded for those guest bytes. An empty path or an unreadable index
// yields an empty map, so a compiled leaf with no known identity falls back to
// its bare digest. The digest keys are already normalized on both sides, so they
// join directly.
func provenanceIdentities(provIndexPath string) map[string]string {
	m := map[string]string{}
	if provIndexPath == "" {
		return m
	}
	ix, err := provindex.Load(provIndexPath)
	if err != nil {
		return m
	}
	for _, e := range ix.Entries() {
		names := make([]string, 0, len(e.Identities))
		for _, id := range e.Identities {
			names = append(names, id.URI+"@"+id.Version)
		}
		m[e.Digest] = strings.Join(names, ", ")
	}
	return m
}

// enumDescribe enumerates the plugin-describe cache. Each entry is a
// `sha256-<hex>.json` file whose stem is the colon-free tag form of the guest's
// content digest (the same form the describe-cache writer emits). The stem is
// reduced to the bare-hex comparison key, which equals the pinned digest a
// lockfile plugin entry records once both are normalized, so a describe entry
// maps to a lockfile reference by that key.
func enumDescribe(root string) []cacheElement {
	var out []cacheElement
	entries, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p := filepath.Join(root, e.Name())
		out = append(out, cacheElement{
			label:     e.Name(),
			path:      p,
			size:      fileSize(p),
			digestHex: normalizeDigestHex(strings.TrimSuffix(e.Name(), ".json")),
		})
	}
	sortElements(out)
	return out
}

// compiledModuleClassName is the display name of the compiled-module cache
// class. It is the single spelling both the class assembly and the prune arm
// that special-cases the class share, so the two never drift.
const compiledModuleClassName = "compiled-module cache"

// collectClasses enumerates every applicable cache class in a fixed order:
// the three workspace subtrees (present only when a workspace resolved) then
// the two global caches. The fixed order and per-class element sort make the
// whole enumeration deterministic across runs of identical cache state. This is
// the single assembly of the class set: status lists every class, prune filters
// on the prunable flag, and clear acts on all of them.
func collectClasses(loc cacheLocations) []cacheClass {
	var classes []cacheClass
	if loc.wsRoot != "" {
		classes = append(classes,
			cacheClass{name: "workspace entity cache", root: loc.entitiesRoot(), prunable: true, elements: enumEntities(loc.entitiesRoot())},
			cacheClass{name: "workspace source-artifact cache", root: loc.sourceArtifactsRoot(), prunable: true, elements: enumSourceArtifacts(loc.sourceArtifactsRoot())},
			cacheClass{name: "workspace external-input store", root: loc.inputsRoot(), prunable: true, elements: enumInputs(loc.inputsRoot())},
			cacheClass{name: "workspace docs-bundle cache", root: loc.docsBundlesRoot(), prunable: false, elements: enumDocsBundles(loc.docsBundlesRoot())},
		)
	}
	if loc.compileDir != "" {
		classes = append(classes,
			cacheClass{name: compiledModuleClassName, root: loc.compileDir, prunable: true, elements: enumCompileModules(loc.compileDir, loc.attribIndexPath, loc.provIndexPath)},
		)
	}
	if loc.describeDir != "" {
		classes = append(classes,
			cacheClass{name: "plugin-describe cache", root: loc.describeDir, prunable: true, elements: enumDescribe(loc.describeDir)},
		)
	}
	return classes
}

func newCacheStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "List cache contents and a total summary",
		Long: `List the contents of every cache, one element per line, followed by a
summary line of the form "N elements, <human size>". The listing is
deterministic: identical cache state produces byte-identical output.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCacheStatus(cmd.OutOrStdout(), resolveCacheLocations(cmd.Context()))
		},
	}
}

// pruneNeedsWorkspaceErr is returned when prune runs with no resolvable
// workspace to draw a reference set from.
var pruneNeedsWorkspaceErr = fmt.Errorf("cache prune needs a workspace, but none could be resolved")

// runCacheStatus prints every class's elements one per line and a global
// summary. Output is a pure function of on-disk cache state.
func runCacheStatus(w io.Writer, loc cacheLocations) error {
	classes := collectClasses(loc)
	var totalCount int
	var totalSize int64
	for _, cl := range classes {
		fmt.Fprintf(w, "%s:\n", cl.name)
		if len(cl.elements) == 0 {
			fmt.Fprintln(w, "  (empty)")
			continue
		}
		for _, el := range cl.elements {
			if el.note != "" {
				fmt.Fprintf(w, "  %s  %s  %s\n", el.label, humanSize(el.size), el.note)
			} else {
				fmt.Fprintf(w, "  %s  %s\n", el.label, humanSize(el.size))
			}
			totalCount++
			totalSize += el.size
		}
	}
	printProvenance(w, loc.provIndexPath)
	fmt.Fprintf(w, "\n%d elements, %s\n", totalCount, humanSize(totalSize))
	return nil
}

// printProvenance lists the resolved-plugin identities the provenance index
// holds — one line per recorded content digest, naming every {URI@version} the
// resolver loaded those exact guest bytes for. When a digest carries more than
// one identity (distinct URIs or versions that resolve to byte-identical bytes),
// the line is marked "shared by N". The listing is deterministic — digests and
// identities are enumerated in sorted order and no timestamps are printed — so
// identical index state produces byte-identical output. The identities are
// metadata about compiled modules, not reclaimable cache elements, so they are
// listed separately and excluded from the element count and size summary.
func printProvenance(w io.Writer, indexPath string) {
	fmt.Fprintf(w, "plugin provenance:\n")
	if indexPath == "" {
		fmt.Fprintln(w, "  (empty)")
		return
	}
	ix, err := provindex.Load(indexPath)
	if err != nil {
		fmt.Fprintln(w, "  (empty)")
		return
	}
	entries := ix.Entries()
	if len(entries) == 0 {
		fmt.Fprintln(w, "  (empty)")
		return
	}
	for _, e := range entries {
		names := make([]string, 0, len(e.Identities))
		for _, id := range e.Identities {
			names = append(names, id.URI+"@"+id.Version)
		}
		line := strings.Join(names, ", ")
		if len(e.Identities) > 1 {
			line += fmt.Sprintf("  shared by %d", len(e.Identities))
		}
		fmt.Fprintf(w, "  %s\n", line)
	}
}

func newCachePruneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prune",
		Short: "Remove artifact-cache entries no lockfile references",
		Long: `Remove cache entries whose content digest no workspace lockfile references,
retaining every referenced entry. The reference set is the union of the
digests pinned across every lockfile (*.lock) in the workspace.

Prune acts on the digest-mappable caches — the workspace artifact cache
(entities, source artifacts, external inputs) and the plugin-describe cache —
and on the compiled-module cache by reference: a compiled leaf attributed to a
guest is reclaimed when no lockfile still references that guest, and the shared
runtime kernel is reclaimed once its last referring guest is pruned. A compiled
leaf whose attribution cannot be determined is retained rather than removed on a
guess; that remainder is reclaimed by "aggrega cache evict" or "aggrega cache
clear".

Prune needs a pulled workspace: it fails when no lockfile is present.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCachePrune(cmd.OutOrStdout(), resolveCacheLocations(cmd.Context()))
		},
	}
}

// runCachePrune builds the lockfile reference set and removes every prunable
// element whose content digest is absent from it. It halts loudly when no
// workspace or no lockfile is present, rather than silently pruning against an
// empty reference set (which would evict every entry).
func runCachePrune(w io.Writer, loc cacheLocations) error {
	if loc.wsRoot == "" {
		return pruneNeedsWorkspaceErr
	}
	wsRoot := loc.wsRoot
	cacheDir := loc.cacheDir

	lockPaths := findLockfiles(wsRoot, cacheDir)
	if len(lockPaths) == 0 {
		return fmt.Errorf("cache prune needs a pulled workspace: no lockfiles (*.lock) found under %s; run `aggrega pull <release>` first", wsRoot)
	}
	refset, loaded := buildReferenceSet(lockPaths)
	if loaded == 0 {
		return fmt.Errorf("cache prune found %d lockfile candidate(s) under %s but none parsed as a lockfile; run `aggrega pull <release>` first", len(lockPaths), wsRoot)
	}

	// Prune every prunable class, drawn from the single class-set assembly. The
	// describe cache is included because a describe entry's filename stem is the
	// guest's content digest, which equals the pin a lockfile plugin entry
	// records — so it maps to the same reference set. The compiled-module cache is
	// prunable too, but by attribution rather than by an element digest, so it is
	// handled by a dedicated arm that consults the attribution index directly.
	classes := collectClasses(loc)
	ax, _ := attribindex.Load(loc.attribIndexPath)

	var removed, retained int
	var reclaimed int64
	// Compiled leaves whose attribution cannot be determined are retained on
	// purpose (never removed on a guess); they are reclaimable only by evict or
	// clear, and are reported separately so the retained count is read correctly.
	var unattributedN int
	var unattributedSize int64
	// Reconciliation inputs: the leaf keys removed, and the guest referrer digests
	// those removals drop, so a surviving kernel's referrer set no longer counts a
	// pruned guest.
	var removedLeafKeys, droppedReferrers []string
	for _, cl := range classes {
		if !cl.prunable {
			continue
		}
		if cl.name == compiledModuleClassName {
			for _, el := range cl.elements {
				entry, attributed := ax.Entry(el.label)
				if !attributed {
					unattributedN++
					unattributedSize += el.size
					continue
				}
				if anyReferrerInSet(entry.Referrers, refset) {
					retained++
					continue
				}
				if rmErr := os.Remove(el.path); rmErr != nil {
					return fmt.Errorf("cache prune: remove %s: %w", el.path, rmErr)
				}
				removeEmptyParents(el.path, cl.root)
				fmt.Fprintf(w, "removed %s: %s\n", cl.name, el.label)
				removed++
				reclaimed += el.size
				removedLeafKeys = append(removedLeafKeys, el.label)
				if !entry.Kernel {
					droppedReferrers = append(droppedReferrers, entry.Referrers...)
				}
			}
			continue
		}
		for _, el := range cl.elements {
			if el.digestHex == "" || refset[el.digestHex] {
				retained++
				continue
			}
			if rmErr := os.RemoveAll(el.path); rmErr != nil {
				return fmt.Errorf("cache prune: remove %s: %w", el.path, rmErr)
			}
			removeEmptyParents(el.path, cl.root)
			fmt.Fprintf(w, "removed %s: %s\n", cl.name, el.label)
			removed++
			reclaimed += el.size
		}
	}

	// Reconcile the attribution index so it no longer references removed leaves
	// and a surviving kernel's referrer set no longer counts a pruned guest. This
	// is index hygiene, not prune correctness — but it keeps the "shared by N"
	// accounting honest for the next inspection.
	if len(removedLeafKeys) > 0 && loc.attribIndexPath != "" {
		if err := attribindex.RemoveLeaves(loc.attribIndexPath, removedLeafKeys, droppedReferrers); err != nil {
			return fmt.Errorf("cache prune: reconcile attribution index: %w", err)
		}
	}

	fmt.Fprintf(w, "\npruned %d element(s), %s reclaimed; %d retained\n", removed, humanSize(reclaimed), retained)

	// Report the compiled leaves retained for lack of attribution: prune cannot
	// map them to a lockfile, so they are reclaimable only by the size-bounded
	// evict or the total clear.
	if unattributedN > 0 {
		fmt.Fprintf(w, "%d %s element(s) unattributed and not prune-eligible (~%s); reclaim with 'aggrega cache evict' or 'aggrega cache clear'\n", unattributedN, compiledModuleClassName, humanSize(unattributedSize))
	}

	// Report the classes prune cannot reclaim at all so the retained count above
	// is not read against the full set. Their size is aggregated from the same
	// class-set assembly, and clear is named as the verb that reclaims them.
	for _, cl := range classes {
		if cl.prunable || len(cl.elements) == 0 {
			continue
		}
		var n int
		var size int64
		for _, el := range cl.elements {
			n++
			size += el.size
		}
		fmt.Fprintf(w, "%d %s element(s) not prune-eligible (~%s); reclaim with 'aggrega cache clear'\n", n, cl.name, humanSize(size))
	}
	return nil
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

func newCacheEvictCmd() *cobra.Command {
	var maxSize string
	c := &cobra.Command{
		Use:   "evict --max-size <size>",
		Short: "Trim the compiled-module cache to a size cap, oldest first",
		Long: `Trim the compiled-module cache to a size cap by removing the
least-recently-compiled modules first, until the cache total is at or below
--max-size. The compiled-module cache is the one machine-global store that grows
without bound: every provider guest is compiled once and reused across every
project on the machine, so over time it can dominate on-disk cache use.

Unlike prune (which reclaims artifact-cache entries no lockfile references) and
clear (which wipes every cache), evict is size-bounded and touches only the
compiled-module cache. Evicted modules recompile on demand on the next build, so
eviction loses no irrecoverable state.

--max-size is required and accepts a binary-unit size in the same vocabulary the
status summary prints — for example 5GiB, "4.5 GiB", or 512MiB — or a bare
integer byte count. The unit suffix is case-insensitive and an optional space
may separate the number from the unit.

Eviction is explicit: no build evicts on its own. When the cache already fits
the cap, evict reports a no-op and removes nothing.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(maxSize) == "" {
				return fmt.Errorf("cache evict: --max-size is required")
			}
			capBytes, err := parseHumanSize(maxSize)
			if err != nil {
				return fmt.Errorf("cache evict: invalid --max-size: %w", err)
			}
			if capBytes <= 0 {
				return fmt.Errorf("cache evict: --max-size must be greater than zero, got %q", maxSize)
			}
			return runCacheEvict(cmd.OutOrStdout(), resolveCacheLocations(cmd.Context()), capBytes)
		},
	}
	c.Flags().StringVar(&maxSize, "max-size", "", "size cap for the compiled-module cache (e.g. 5GiB, 512MiB, or a byte count); required")
	return c
}

// evictLeaf is one compiled-module cache leaf considered for size-bounded
// eviction: its display label, filesystem path, byte size, and last
// modification time. modTime orders eviction (oldest-compiled first) but is
// never printed, so eviction output carries no raw timestamps and the selection
// stays a pure function of the on-disk leaf set.
type evictLeaf struct {
	label   string
	path    string
	size    int64
	modTime time.Time
}

// collectCompileLeaves enumerates the compiled-module cache leaves eligible for
// eviction beneath root. It applies the same membership test the status
// enumeration uses — isCompileLeaf — so the transient per-guest compile-lock
// sidecars and half-written *.tmp transients are never evicted or counted, and
// an in-flight compile is left alone. A root that does not exist is an empty
// leaf set, not an error. Each leaf carries its size and modification time for
// the oldest-first order.
func collectCompileLeaves(root string) ([]evictLeaf, error) {
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []evictLeaf
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !isCompileLeaf(d.Name()) {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			rel = p
		}
		out = append(out, evictLeaf{
			label:   filepath.ToSlash(rel),
			path:    p,
			size:    info.Size(),
			modTime: info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// runCacheEvict trims the compiled-module cache to capBytes. It enumerates the
// eligible leaves, and when their total already fits the cap prints a single
// no-op line and removes nothing. Otherwise it removes leaves oldest-compiled
// first — modification time ascending, leaf label ascending as the deterministic
// tie-break — until the remaining total fits the cap, reporting each removal and
// a closing summary. Eviction is best-effort with respect to a concurrent
// compile: the cache is re-derivable, so a race only forces a recompile. A
// per-leaf removal failure is returned loudly rather than swallowed.
func runCacheEvict(w io.Writer, loc cacheLocations, capBytes int64) error {
	if loc.compileDir == "" {
		return fmt.Errorf("cache evict: no compiled-module cache directory could be resolved")
	}
	leaves, err := collectCompileLeaves(loc.compileDir)
	if err != nil {
		return fmt.Errorf("cache evict: enumerate compiled-module cache: %w", err)
	}
	var total int64
	for _, l := range leaves {
		total += l.size
	}
	if total <= capBytes {
		fmt.Fprintf(w, "compiled-module cache is %s, within cap %s; nothing evicted\n", humanSize(total), humanSize(capBytes))
		return nil
	}
	sort.Slice(leaves, func(i, j int) bool {
		if !leaves[i].modTime.Equal(leaves[j].modTime) {
			return leaves[i].modTime.Before(leaves[j].modTime)
		}
		return leaves[i].label < leaves[j].label
	})
	remaining := total
	var removed int
	var reclaimed int64
	for _, l := range leaves {
		if remaining <= capBytes {
			break
		}
		if rmErr := os.Remove(l.path); rmErr != nil {
			return fmt.Errorf("cache evict: remove %s: %w", l.path, rmErr)
		}
		fmt.Fprintf(w, "evicted %s (%s)\n", l.label, humanSize(l.size))
		removed++
		reclaimed += l.size
		remaining -= l.size
	}
	fmt.Fprintf(w, "evicted %d leaf(s), %s reclaimed; %s retained (cap %s)\n", removed, humanSize(reclaimed), humanSize(remaining), humanSize(capBytes))
	return nil
}

// parseHumanSize parses a byte quantity written in the same binary-unit
// vocabulary humanSize renders — B, KiB, MiB, GiB, TiB, PiB, EiB — so a size the
// tool prints round-trips back through this parser; the display and parse sides
// share one unit scheme. A bare integer (or a plain "B" suffix) is an exact byte
// count; a unit-suffixed value may be fractional and is scaled then truncated to
// whole bytes. The unit suffix is case-insensitive and an optional space may
// separate the number from the unit. An empty, malformed, or unrecognized-unit
// quantity returns an error.
func parseHumanSize(s string) (int64, error) {
	str := strings.TrimSpace(s)
	if str == "" {
		return 0, fmt.Errorf("empty size value (expected a byte quantity such as 500MiB)")
	}
	// Split the leading numeric run from the trailing unit letters.
	upper := strings.ToUpper(str)
	idx := len(upper)
	for idx > 0 {
		c := upper[idx-1]
		if c >= 'A' && c <= 'Z' {
			idx--
			continue
		}
		break
	}
	numStr := strings.TrimSpace(str[:idx])
	unit := strings.TrimSpace(upper[idx:])
	if numStr == "" {
		return 0, fmt.Errorf("size %q has no numeric value", s)
	}

	var mult int64
	switch unit {
	case "", "B":
		mult = 1
	case "KIB":
		mult = 1 << 10
	case "MIB":
		mult = 1 << 20
	case "GIB":
		mult = 1 << 30
	case "TIB":
		mult = 1 << 40
	case "PIB":
		mult = 1 << 50
	case "EIB":
		mult = 1 << 60
	default:
		return 0, fmt.Errorf("size %q has an unrecognized unit %q; use B, KiB, MiB, GiB, TiB, PiB, or EiB", s, unit)
	}

	if mult == 1 {
		n, err := strconv.ParseInt(numStr, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("size %q is not a byte integer: %w", s, err)
		}
		return n, nil
	}
	f, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("size %q has a malformed number: %w", s, err)
	}
	return int64(f * float64(mult)), nil
}

func newCacheClearCmd() *cobra.Command {
	var assumeYes bool
	c := &cobra.Command{
		Use:   "clear",
		Short: "Wipe every cache, including compiled modules",
		Long: `Wipe every cache: the workspace artifact cache (entities, source artifacts,
external inputs) and the two global caches (compiled modules and plugin
descriptors). This is the total-wipe verb; unlike prune it reclaims the
compiled-module cache as well. Cleared caches repopulate on the next build.

Because the wipe is irreversible, clear asks for confirmation before removing
anything. Pass --yes to skip the prompt for non-interactive use; without --yes
on a non-interactive stdin it refuses rather than blocking on a prompt no one
can answer.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := confirmCacheClear(cmd.OutOrStdout(), cmd.InOrStdin(), assumeYes); err != nil {
				return err
			}
			return runCacheClear(cmd.OutOrStdout(), resolveCacheLocations(cmd.Context()))
		},
	}
	c.Flags().BoolVar(&assumeYes, "yes", false, "skip the confirmation prompt (for non-interactive/CI use)")
	return c
}

// confirmCacheClear gates the total cache wipe behind an interactive
// confirmation. With assumeYes it consents immediately. On a non-interactive
// stdin without assumeYes it refuses — returning an error rather than blocking
// on a prompt no one can answer. Otherwise it prompts [y/N] on w, reads a line
// from r, and consents only on an explicit yes.
func confirmCacheClear(w io.Writer, r io.Reader, assumeYes bool) error {
	if assumeYes {
		return nil
	}
	if !isTerminal() {
		return errors.New("cache clear: refusing to wipe every cache on a non-interactive stdin without --yes")
	}
	fmt.Fprint(w, "This wipes every cache, including the compiled-module store. Proceed? [y/N] ")
	line, _ := bufio.NewReader(r).ReadString('\n')
	switch strings.TrimSpace(line) {
	case "y", "Y", "yes", "Yes", "YES":
		return nil
	default:
		return errors.New("cache clear: aborted")
	}
}

// runCacheClear removes every cache subtree. Each subtree is sized before
// removal so the report states what was reclaimed. Absent subtrees are no-ops.
func runCacheClear(w io.Writer, loc cacheLocations) error {
	var reclaimed int64
	var wiped int
	for _, cl := range collectClasses(loc) {
		if cl.root == "" {
			continue
		}
		if _, err := os.Stat(cl.root); err != nil {
			continue
		}
		size := dirSize(cl.root)
		if rmErr := os.RemoveAll(cl.root); rmErr != nil {
			return fmt.Errorf("cache clear: remove %s: %w", cl.root, rmErr)
		}
		fmt.Fprintf(w, "cleared %s (%s)\n", cl.name, humanSize(size))
		reclaimed += size
		wiped++
	}
	fmt.Fprintf(w, "\ncleared %d cache(s), %s reclaimed\n", wiped, humanSize(reclaimed))
	return nil
}

// findLockfiles returns the sorted absolute paths of every `*.lock` file under
// wsRoot, skipping the artifact cache directory and version-control metadata so
// the walk does not mistake a git lock file for a Release lockfile.
func findLockfiles(wsRoot, cacheDir string) []string {
	cacheAbs := filepath.Join(wsRoot, cacheDir)
	var paths []string
	_ = filepath.WalkDir(wsRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" || p == cacheAbs {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".lock") {
			paths = append(paths, p)
		}
		return nil
	})
	sort.Strings(paths)
	return paths
}

// buildReferenceSet unions the pinned content digests across the given
// lockfiles into a lowercase, prefix-free hex set — the retain set prune keeps.
// The digests are read from the raw lockfile sections directly (entities,
// external inputs, schema libraries, mapping libraries, plugins, source
// artifacts) so every cached class has its reference, including source
// artifacts. The second return is the count of candidates that parsed as a
// lockfile.
func buildReferenceSet(lockPaths []string) (map[string]bool, int) {
	refset := map[string]bool{}
	var loaded int
	for _, p := range lockPaths {
		lf, _, err := lockfile.LoadLockfile(p)
		if err != nil || lf == nil {
			continue
		}
		loaded++
		for _, e := range lf.Entities {
			addDigest(refset, e.Digest)
		}
		for _, e := range lf.ExternalInputs {
			addDigest(refset, e.Sha256)
		}
		for _, e := range lf.SchemaLibraries {
			addDigest(refset, e.Digest)
		}
		for _, e := range lf.MappingLibraries {
			addDigest(refset, e.Digest)
		}
		for _, e := range lf.WorkloadLibraries {
			addDigest(refset, e.Digest)
		}
		for _, e := range lf.Plugins {
			addDigest(refset, e.Digest)
		}
		for _, e := range lf.SourceArtifacts {
			addDigest(refset, e.Digest)
		}
	}
	// Fold in the content digests of the providers embedded in the binary. Their
	// describe-cache entries are keyed by the guest's content digest, which appears
	// in no lockfile section, so without this the retain set would omit an
	// actively-embedded provider and prune would evict its describe entry. Sourced
	// through the single embedded-identity accessor so cache/prune never
	// re-enumerates the embedded set or re-derives its digests, and normalized
	// through the same addDigest path as the lockfile digests so both sides compare
	// identically.
	for _, d := range embedded.ContentDigests() {
		addDigest(refset, d)
	}
	return refset, loaded
}

func addDigest(set map[string]bool, d string) {
	if h := normalizeDigestHex(d); h != "" {
		set[h] = true
	}
}

// normalizeDigestHex reduces a digest to its comparable form: lowercased bare
// hex, with any algorithm prefix stripped. The lockfile records digests in both
// spellings (prefixed for entities and libraries, bare hex for external
// inputs), and the cache element digests are computed in the prefixed spelling,
// so both sides normalize here before comparison. It delegates to the single
// home of the prefix-free spelling so cache enumeration and the provenance
// index key digests identically. Retained as a named in-package delegator for
// call-site legibility.
func normalizeDigestHex(d string) string {
	return digest.BareHex(d)
}

// removeEmptyParents removes now-empty parent directories of a removed slot,
// climbing until a non-empty directory or the stop boundary is reached, so a
// prune does not leave empty scaffolding behind. Any error (a non-empty
// directory, or the boundary) ends the climb.
func removeEmptyParents(removed, stopAt string) {
	if stopAt == "" {
		return
	}
	dir := filepath.Dir(removed)
	for dir != stopAt && strings.HasPrefix(dir, stopAt) {
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// sortElements orders elements by their display label, the single sort key
// that makes every enumeration deterministic.
func sortElements(els []cacheElement) {
	sort.Slice(els, func(i, j int) bool { return els[i].label < els[j].label })
}

// dirSize returns the total byte size of every regular file beneath path, or
// the file's own size when path is a file. A path that cannot be walked
// contributes zero.
func dirSize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// fileSize returns the byte size of a single file, or zero when it cannot be
// stat'd.
func fileSize(path string) int64 {
	if info, err := os.Stat(path); err == nil {
		return info.Size()
	}
	return 0
}

// humanSize renders a byte count in binary units (B, KiB, MiB, …) with one
// decimal place above bytes, so the summary line is compact and stable.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
