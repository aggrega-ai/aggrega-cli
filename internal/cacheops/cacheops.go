// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package cacheops holds the on-disk cache inventory and the operations that
// maintain it, as data rather than as rendered text.
//
// Every caller-facing surface reaches the same functions here and renders the
// result its own way, so the answer a caller gets cannot depend on which surface
// it asked. A surface that formatted its own listing would be a second
// enumeration of the same directories, and the two would agree until the day a
// class was added to one of them.
//
// The package deliberately holds no dependency on any surface. It takes the
// resolved cache roots as a value and returns what it found or what it removed;
// deciding where those roots come from, and how to present the answer, is the
// caller's.
//
// # Reach is carried, never inferred
//
// The caches this package addresses are not all scoped to one workspace. Two of
// them are shared across every project on the machine, so removing an element of
// one reaches workspaces the caller never named and whose owners did not ask.
// Every class therefore declares its Reach, and every result names the classes it
// acted on — so a caller is told the scope of what it changed rather than left to
// infer it from a class name. A result that reported a machine-wide removal as
// though it were workspace-local would be worse than declining the operation.
package cacheops

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/attribindex"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/provindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/metadata"
)

// Reach states how far a cache class's contents are shared, and therefore how
// far an operation that removes one of its elements travels.
//
// It is a property of the class rather than of the operation, declared once
// where the class is assembled, so every result that names a class carries the
// same answer. An operation cannot describe its own blast radius more narrowly
// than the classes it touched.
type Reach string

const (
	// ReachWorkspace marks a class held under the workspace's own configured
	// cache directory. Removing one of its elements affects the workspace the
	// caller named and nothing else.
	ReachWorkspace Reach = "workspace"

	// ReachMachine marks a class shared across every project on the machine.
	// Removing one of its elements reaches workspaces the caller never named.
	ReachMachine Reach = "machine"
)

// Element is one enumerable unit of a cache class: a slot directory, a stored
// blob, or a compiled module.
//
// Label is the deterministic display path within its class. Path is the
// filesystem entry a prune or a clear removes. Size is the element's total byte
// size. DigestHex is the lowercase, prefix-free content digest that maps the
// element to a lockfile reference, or empty when the element carries no
// reproducible content digest — a compiled module keyed by the runtime's own
// internal hashing. Note is an optional trailing annotation: a compiled module
// reports its attributed identity, the shared runtime kernel, or that it is
// unattributed, and the classes carrying no annotation leave it empty.
type Element struct {
	Label     string `json:"label"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	DigestHex string `json:"digest,omitempty"`
	Note      string `json:"note,omitempty"`
}

// Class is one named cache subtree with its enumerated elements.
//
// Prunable records whether a prune may reclaim the class: true for the
// digest-addressable classes whose elements map to a lockfile reference, false
// for a class keyed by content no lockfile reference can name, which only a
// clear reclaims.
type Class struct {
	Name     string    `json:"name"`
	Root     string    `json:"root"`
	Reach    Reach     `json:"reach"`
	Prunable bool      `json:"prunable"`
	Elements []Element `json:"elements"`
}

// Locations resolves every cache root an operation addresses.
//
// WorkspaceRoot and CacheDir are empty when no workspace resolved, in which case
// only the machine-wide caches are addressable. The machine-wide directories may
// themselves be absent when no user cache directory resolves, reported by their
// empty value.
type Locations struct {
	WorkspaceRoot string
	CacheDir      string

	CompileDir  string
	DescribeDir string

	ProvIndexPath   string
	AttribIndexPath string
}

// ResolveLocations fills the machine-wide cache roots and carries the workspace
// roots it is given.
//
// The workspace half is supplied rather than discovered because each surface
// resolves a workspace its own way — a command from its working directory, a
// session from the root it was opened over — while the machine-wide half is the
// same wherever it is asked from. Passing an empty workspace root yields a value
// addressing only the machine-wide caches, which is the honest shape for a
// caller that has no workspace.
func ResolveLocations(workspaceRoot, cacheDir string) Locations {
	loc := Locations{WorkspaceRoot: workspaceRoot, CacheDir: cacheDir}
	if dir, ok := wasm.CompileCacheDir(); ok {
		loc.CompileDir = dir
	}
	if dir, ok := wasm.PluginDescribeCacheDir(); ok {
		loc.DescribeDir = dir
	}
	if p, ok := provindex.DefaultIndexPath(); ok {
		loc.ProvIndexPath = p
	}
	if root, ok := wasm.AggregaCacheRoot(); ok {
		loc.AttribIndexPath = attribindex.DefaultIndexPath(root)
	}
	return loc
}

// entitiesRoot returns the workspace entity-cache subtree root, or "" when no
// workspace resolved.
func (l Locations) entitiesRoot() string {
	if l.WorkspaceRoot == "" {
		return ""
	}
	return oci.EntitiesRoot(l.WorkspaceRoot, l.CacheDir)
}

// sourceArtifactsRoot returns the workspace source-artifact-cache subtree root,
// or "" when no workspace resolved.
func (l Locations) sourceArtifactsRoot() string {
	if l.WorkspaceRoot == "" {
		return ""
	}
	return cacheslot.SourceArtifactsRoot(l.WorkspaceRoot, l.CacheDir)
}

// inputsRoot returns the workspace external-input store root, or "" when no
// workspace resolved.
func (l Locations) inputsRoot() string {
	if l.WorkspaceRoot == "" {
		return ""
	}
	return externalinputs.StoreRoot(filepath.Join(l.WorkspaceRoot, l.CacheDir))
}

// docsBundlesRoot returns the workspace documentation-bundle-cache subtree root,
// or "" when no workspace resolved.
func (l Locations) docsBundlesRoot() string {
	if l.WorkspaceRoot == "" {
		return ""
	}
	return docscache.DocsBundlesRoot(l.WorkspaceRoot, l.CacheDir)
}

// CompiledModuleClassName is the display name of the compiled-module cache
// class. It is the single spelling the class assembly and the prune arm that
// special-cases the class share, so the two never drift.
const CompiledModuleClassName = "compiled-module cache"

// Collect enumerates every applicable cache class in a fixed order: the four
// workspace subtrees, present only when a workspace resolved, then the two
// machine-wide caches.
//
// The fixed order and per-class element sort make the whole enumeration
// deterministic across runs over identical cache state. This is the single
// assembly of the class set: a report lists every class, a prune filters on the
// prunable flag, and a clear acts on all of them.
func Collect(loc Locations) []Class {
	var classes []Class
	if loc.WorkspaceRoot != "" {
		classes = append(classes,
			Class{Name: "workspace entity cache", Root: loc.entitiesRoot(), Reach: ReachWorkspace, Prunable: true, Elements: enumEntities(loc.entitiesRoot())},
			Class{Name: "workspace source-artifact cache", Root: loc.sourceArtifactsRoot(), Reach: ReachWorkspace, Prunable: true, Elements: enumSourceArtifacts(loc.sourceArtifactsRoot())},
			Class{Name: "workspace external-input store", Root: loc.inputsRoot(), Reach: ReachWorkspace, Prunable: true, Elements: enumInputs(loc.inputsRoot())},
			Class{Name: "workspace docs-bundle cache", Root: loc.docsBundlesRoot(), Reach: ReachWorkspace, Prunable: false, Elements: enumDocsBundles(loc.docsBundlesRoot())},
		)
	}
	if loc.CompileDir != "" {
		classes = append(classes,
			Class{Name: CompiledModuleClassName, Root: loc.CompileDir, Reach: ReachMachine, Prunable: true, Elements: enumCompileModules(loc.CompileDir, loc.AttribIndexPath, loc.ProvIndexPath)},
		)
	}
	if loc.DescribeDir != "" {
		classes = append(classes,
			Class{Name: "plugin-describe cache", Root: loc.DescribeDir, Reach: ReachMachine, Prunable: true, Elements: enumDescribe(loc.DescribeDir)},
		)
	}
	return classes
}

// enumEntities enumerates the entity-cache slots. Each slot is an
// <encoded-uri>/<version> directory holding a content blob; the slot's content
// digest is the blob's content digest, which equals the pinned digest a lockfile
// records for that entity, schema library, mapping library, or plugin artifact.
// A slot whose blob cannot be read carries an empty digest and is retained by a
// prune rather than removed on a guess.
func enumEntities(root string) []Element {
	var out []Element
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
			el := Element{
				Label: display + "@" + vd.Name(),
				Path:  slot,
				Size:  dirSize(slot),
			}
			if blob, berr := os.ReadFile(filepath.Join(slot, "blob")); berr == nil {
				el.DigestHex = NormalizeDigestHex(digest.ContentDigest(blob))
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
// cannot be read carries an empty digest and is retained by a prune.
func enumSourceArtifacts(root string) []Element {
	var out []Element
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
		el := Element{
			Label: filepath.ToSlash(rel),
			Path:  p,
			Size:  dirSize(p),
		}
		if meta, merr := metadata.Read(metaPath); merr == nil {
			el.DigestHex = NormalizeDigestHex(meta.Digest)
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
func enumInputs(root string) []Element {
	var out []Element
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			rel = p
		}
		out = append(out, Element{
			Label:     filepath.ToSlash(rel),
			Path:      p,
			Size:      fileSize(p),
			DigestHex: NormalizeDigestHex(d.Name()),
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
// prune-eligible — only a clear reclaims it. Enumeration reports each slot's
// decoded reference and version for a stable, deterministic listing.
func enumDocsBundles(root string) []Element {
	var out []Element
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
			out = append(out, Element{
				Label: display + "@" + vd.Name(),
				Path:  slot,
				Size:  dirSize(slot),
			})
		}
	}
	sortElements(out)
	return out
}

// isCompileLeaf reports whether a non-directory filename beneath the
// compiled-module cache is a cache leaf — a compiled module worth listing and
// counting. It is the membership test the enumeration applies. Two kinds of file
// are excluded because they are not compiled modules: the transient per-guest
// compile-lock sidecars (`.compile-<digest>.lock`) that serialize concurrent
// compiles, and the half-written `*.tmp` transients an in-flight compile stages
// before its atomic rename. Both come and go with a compile in progress and
// never represent finished cache content. The same shape covers the lock a
// reclamation pass holds, so that file is excluded here too.
//
// A second small predicate on the far side of the package boundary decides the
// same question for the loader and for capped reclamation. The two are
// deliberately separate rather than one exported predicate, so this package's
// reporting surface does not widen that one; keep them in step, since a change
// to which files count as compiled modules must land on both. The membership
// guard in this package's tests asserts that the listing and the shared
// reclamation enumeration return the same set, so a divergence surfaces there
// rather than as a cap that counts what the listing does not show.
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
// leaf alone. The compile-attribution index, populated by observing cold
// compiles, maps each leaf to the guest content digests that depend on it;
// enumeration consults it to annotate each leaf:
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
// class remains non-prunable by element digest here: attributing leaves is a
// reporting concern, not a prune-scope change.
func enumCompileModules(root, attribIndexPath, provIndexPath string) []Element {
	ax, _ := attribindex.Load(attribIndexPath)
	identities := provenanceIdentities(provIndexPath)
	var out []Element
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
		el := Element{Label: leafKey, Path: p, Size: fileSize(p)}
		if e, ok := ax.Entry(leafKey); ok {
			switch {
			case e.Kernel:
				el.Note = fmt.Sprintf("shared runtime kernel, shared by %d", len(e.Referrers))
			case len(e.Referrers) > 0:
				dg := e.Referrers[0]
				el.DigestHex = dg
				if name, known := identities[dg]; known && name != "" {
					el.Note = name
				} else {
					el.Note = dg
				}
			default:
				el.Note = "unattributed"
			}
		} else {
			el.Note = "unattributed"
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
func enumDescribe(root string) []Element {
	var out []Element
	entries, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p := filepath.Join(root, e.Name())
		out = append(out, Element{
			Label:     e.Name(),
			Path:      p,
			Size:      fileSize(p),
			DigestHex: NormalizeDigestHex(strings.TrimSuffix(e.Name(), ".json")),
		})
	}
	sortElements(out)
	return out
}

// ProvenanceEntry is one recorded content digest's resolved plugin identities —
// every URI@version the resolver loaded those exact guest bytes for. SharedBy is
// the identity count when more than one identity resolves to the same bytes, and
// zero otherwise, so a reader is told about sharing only where there is sharing
// to report.
//
// Identities are metadata about compiled modules rather than reclaimable cache
// elements, which is why they are carried apart from the classes and excluded
// from every count and size total.
type ProvenanceEntry struct {
	Identities []string `json:"identities"`
	SharedBy   int      `json:"sharedBy,omitempty"`
}

// Provenance lists the resolved-plugin identities the provenance index holds, in
// the index's own sorted enumeration order, so identical index state yields an
// identical listing. An empty path or an unreadable index yields no entries.
func Provenance(indexPath string) []ProvenanceEntry {
	if indexPath == "" {
		return nil
	}
	ix, err := provindex.Load(indexPath)
	if err != nil {
		return nil
	}
	entries := ix.Entries()
	out := make([]ProvenanceEntry, 0, len(entries))
	for _, e := range entries {
		names := make([]string, 0, len(e.Identities))
		for _, id := range e.Identities {
			names = append(names, id.URI+"@"+id.Version)
		}
		pe := ProvenanceEntry{Identities: names}
		if len(e.Identities) > 1 {
			pe.SharedBy = len(e.Identities)
		}
		out = append(out, pe)
	}
	return out
}

// Report is the cache inventory: every class with its elements, the recorded
// plugin provenance, and the totals across every listed element.
//
// TotalCount and TotalSize span the classes only. The provenance entries are
// metadata rather than reclaimable content, so including them would report a
// size nothing can reclaim.
type Report struct {
	Classes    []Class           `json:"classes"`
	Provenance []ProvenanceEntry `json:"provenance"`
	TotalCount int               `json:"totalCount"`
	TotalSize  int64             `json:"totalSize"`
}

// Status enumerates every cache class and totals what it found. It is a pure
// function of on-disk cache state and removes nothing.
func Status(loc Locations) Report {
	classes := Collect(loc)
	r := Report{Classes: classes, Provenance: Provenance(loc.ProvIndexPath)}
	for _, cl := range classes {
		for _, el := range cl.Elements {
			r.TotalCount++
			r.TotalSize += el.Size
		}
	}
	return r
}

// MachineWideClasses names the classes in the given set whose contents are
// shared across every project on the machine, in the set's own order.
//
// It exists so a surface reporting what an operation changed states the scope
// from the classes it acted on rather than from a sentence somebody wrote about
// the operation. An operation's reach is the union of its classes' reach, and
// that union is computed here rather than asserted anywhere.
func MachineWideClasses(classes []Class) []string {
	var out []string
	for _, cl := range classes {
		if cl.Reach == ReachMachine {
			out = append(out, cl.Name)
		}
	}
	return out
}

// sortElements orders elements by their display label, the single sort key that
// makes every enumeration deterministic.
func sortElements(els []Element) {
	sort.Slice(els, func(i, j int) bool { return els[i].Label < els[j].Label })
}

// dirSize returns the total byte size of every regular file beneath path, or the
// file's own size when path is a file. A path that cannot be walked contributes
// zero.
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

// HumanSize renders a byte count in binary units (B, KiB, MiB, …) with one
// decimal place above bytes, so a summary line is compact and stable.
//
// It is exported because it is the display half of a round trip: ParseHumanSize
// reads back exactly what this writes, and a surface that spelled sizes its own
// way would produce a quantity this package could not parse.
func HumanSize(n int64) string {
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

// ParseHumanSize parses a byte quantity written in the same binary-unit
// vocabulary HumanSize renders — B, KiB, MiB, GiB, TiB, PiB, EiB — so a size the
// tool prints round-trips back through this parser; the display and parse sides
// share one unit scheme. A bare integer (or a plain "B" suffix) is an exact byte
// count; a unit-suffixed value may be fractional and is scaled then truncated to
// whole bytes. The unit suffix is case-insensitive and an optional space may
// separate the number from the unit. An empty, malformed, or unrecognized-unit
// quantity returns an error.
func ParseHumanSize(s string) (int64, error) {
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

// NormalizeDigestHex reduces a digest to its comparable form: lowercased bare
// hex, with any algorithm prefix stripped. A lockfile records digests in both
// spellings — prefixed for entities and libraries, bare hex for external inputs
// — and the cache element digests are computed in the prefixed spelling, so both
// sides normalize here before comparison. It delegates to the single home of the
// prefix-free spelling so cache enumeration and the provenance index key digests
// identically.
func NormalizeDigestHex(d string) string {
	return digest.BareHex(d)
}

// FindLockfiles returns the sorted absolute paths of every `*.lock` file under
// workspaceRoot, skipping the artifact cache directory and version-control
// metadata so the walk does not mistake a source-control lock file for a Release
// lockfile.
func FindLockfiles(workspaceRoot, cacheDir string) []string {
	cacheAbs := filepath.Join(workspaceRoot, cacheDir)
	var paths []string
	_ = filepath.WalkDir(workspaceRoot, func(p string, d fs.DirEntry, err error) error {
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

// BuildReferenceSet unions the pinned content digests across the given lockfiles
// into a lowercase, prefix-free hex set — the retain set a prune keeps. The
// digests are read from the raw lockfile sections directly so every cached class
// has its reference. The second return is the count of candidates that parsed as
// a lockfile.
func BuildReferenceSet(lockPaths []string) (map[string]bool, int) {
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
		for _, e := range lf.ArtifactsLibraries {
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
	// in no lockfile section, so without this the retain set would omit an actively
	// embedded provider and a prune would evict its describe entry. Sourced through
	// the single embedded-identity accessor so this never re-enumerates the embedded
	// set or re-derives its digests, and normalized through the same path as the
	// lockfile digests so both sides compare identically.
	for _, d := range embedded.ContentDigests() {
		addDigest(refset, d)
	}
	return refset, loaded
}

func addDigest(set map[string]bool, d string) {
	if h := NormalizeDigestHex(d); h != "" {
		set[h] = true
	}
}

// removeEmptyParents removes now-empty parent directories of a removed slot, so
// a prune does not leave empty scaffolding behind. Without a stop boundary to
// climb towards it does nothing.
//
// Containment is the shared predicate's, asked of each candidate directory as a
// path relative to the boundary. Three answers end the climb before any removal:
// a directory that shares no common path with the boundary; one the predicate
// places outside it, which is where a sibling whose name merely extends the
// boundary's lands — an `aggrega-test` beside an `aggrega` relativises to a
// climbing path and is outside; and the boundary itself, which relativises to
// `.` and so is never removed, however the caller spelled it. A failed removal
// ends the climb as well, a directory that still holds entries being the
// ordinary case.
func removeEmptyParents(removed, stopAt string) {
	if stopAt == "" {
		return
	}
	dir := filepath.Dir(removed)
	for {
		rel, err := filepath.Rel(stopAt, dir)
		if err != nil || rel == "." {
			return
		}
		if _, ok := pathscope.Resolve(stopAt, rel); !ok {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}
