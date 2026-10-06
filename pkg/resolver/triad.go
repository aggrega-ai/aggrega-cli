// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
	"go.yaml.in/yaml/v3"
)

// ErrDuplicateTriadIdentity fires when the Release's own directory holds two
// Aggregate (or two Manifest) entities that share a metadata.name — the
// same-directory analogue of the catalog's ErrDuplicateRecipeIdentity. The
// message names the directory to read distinctly from the catalog-tier duplicate.
func ErrDuplicateTriadIdentity() error { return errDuplicateTriadIdentity }

var errDuplicateTriadIdentity = cerrors.Register("duplicate-triad-identity", cerrors.Meta{
	ShortCode: "E0152",
	Message:   "duplicate Aggregate/Manifest identity (metadata.name) in the Release directory",
	RulePlace: cerrors.RuleNoStageReaches("The same-directory scan evaluates the rule, while it is indexing the " +
		"Release directory's Aggregates and Manifests by metadata.name for a bare-identity reference to resolve " +
		"against, when two entities of one kind claim one name. The scan reads the directory in order to decide " +
		"which documents a build will open, and it refuses a collision even between two files neither reference " +
		"names, so it happens ahead of every declared stage and no stage holds the files it walked."),
})

// ErrTriadRefUnresolvable fires when a bare-identity aggregate:/manifest:
// reference resolves to no entity in the same-directory scan, the catalog, or the
// registry.
func ErrTriadRefUnresolvable() error { return errTriadRefUnresolvable }

var errTriadRefUnresolvable = cerrors.Register("triad-ref-unresolvable", cerrors.Meta{
	ShortCode: "E0170",
	Message:   "aggregate/manifest reference unresolvable",
	RulePlace: cerrors.RuleNoStageReaches("Triad resolution evaluates the rule, when a Release's bare-identity aggregate or manifest " +
		"reference matches nothing in the Release directory, the catalog, or the registry. The resolution " +
		"chooses which documents a build will read, so it happens ahead of every declared stage and no " +
		"stage holds the candidates it searched."),
})

// ResolvedAggregate is the result of resolving a Release's spec.aggregate
// reference: the loaded entity plus the file path it resolved to and that file's
// raw bytes. Path and Bytes carry the data callers consume beyond the entity —
// the relative-to-workspace path, the externals map, and the parameter-contract
// bytes — which a bare-identity reference would otherwise be unable to surface
// (the matched file is known only to the same-directory scan). For a
// same-directory or `./`/`../` resolution Path is the file on disk and Bytes its
// content; for a catalog-tier resolution they are the catalog entity's source
// path and retained bytes, supplied by the lookup.
type ResolvedAggregate struct {
	Aggregate *types.Aggregate
	Path      string
	Bytes     []byte
	// ResolvedVersion and Digest are populated only for a registry (Tier-3)
	// resolution — the fetched entity's metadata.version (its identity) and
	// content digest — so the caller pins it as a real registry entity rather
	// than a version-less external. FromRegistry marks that path; a same-dir,
	// ./file, or catalog resolution leaves them zero and FromRegistry false.
	ResolvedVersion string
	Digest          string
	// ManifestDigest is the fetched aggregate's OCI manifest digest — the
	// verify subject for its signature, distinct from the content Digest above.
	// Populated only for a registry (Tier-3) resolution.
	ManifestDigest string
	FromRegistry   bool
}

// ResolvedManifest is the Manifest counterpart of [ResolvedAggregate]. A Manifest
// has no catalog or registry tier (it is operator configuration authored beside
// the Release), so it resolves only from a `./`/`../` file or the
// same-directory scan; Path and Bytes are always the on-disk file and its
// content.
type ResolvedManifest struct {
	Manifest *types.Manifest
	Path     string
	Bytes    []byte
}

// AggregateLookup resolves a catalog- or registry-published Aggregate by its
// metadata.name to a fully-populated [ResolvedAggregate] (entity, source path,
// and retained bytes). It is the catalog/registry tier seam for
// [ResolveAggregate]: the resolver knows only name -> ResolvedAggregate, so it
// stays agnostic to how the caller's catalog is organised (a co-located
// per-deployment catalog or a shared registry-backed one resolve identically). A
// nil lookup means no catalog tier is available; the identity cascade then stops
// after the same-directory scan.
//
// The seam is a function rather than a *catalog.Catalog because pkg/catalog
// imports pkg/resolver — a direct dependency would cycle. Callers wrap their
// catalog's name index (e.g. cat.Aggregates[name]) together with its source-path
// and entity-bytes accessors.
type AggregateLookup func(name string) (ResolvedAggregate, bool)

// AggregateRegistryFetch is the optional registry (Tier-3) seam for a bare-name
// spec.aggregate that both the same-directory scan and the catalog miss. It is
// supplied (non-nil) at pull, where it fetches the published Aggregate by
// identity, caches it, and returns it carrying its metadata.version + digest
// (FromRegistry true). At build it is nil — Tier-3 stays inert, so a
// registry-published top-level resolves only through the build-side
// cache→catalog bridge and a genuine miss is a loud ErrTriadRefUnresolvable
// (pull-first remediation), never a network call. found is false when the
// reference is not in the registry, letting the cascade fall through to the
// unresolvable error.
type AggregateRegistryFetch func(ref string) (ResolvedAggregate, bool, error)

// isRelativePathRef reports whether ref is an explicit relative-path file
// reference — the relative-path discriminator (`./…` / `../…`). A bare token
// is an identity, not a path. This is deliberately distinct from the
// source-pointer isLocalSource, which treats a bare token as a local path: a
// recipe source: addresses a file, whereas an aggregate:/manifest: bare token
// addresses an entity by metadata.name.
func isRelativePathRef(ref string) bool {
	return strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "../")
}

// IsRelativePathRef reports whether ref is an explicit relative-path file
// reference, for callers outside this package: the same discriminator entity
// resolution applies when it decides that a reference addresses a file beside
// the authoring document rather than an entity a catalog holds.
//
// It is the exported entry to one predicate rather than a second statement of
// the prefix rule. A consumer that spelled the prefixes out for itself would
// carry a copy of the rule that agrees until one of them is edited, and the
// disagreement would be invisible: the two would simply route a reference to
// different tiers.
func IsRelativePathRef(ref string) bool { return isRelativePathRef(ref) }

// ResolveAggregate resolves a Release's spec.aggregate reference to an Aggregate.
//
// A `./` or `../` reference is an explicit local file, loaded relative to
// releaseDir with no scan, once the reference has been held to the root
// readscope.CheckReference picks for releaseDir — the workspace, or the Release's
// own directory where the Release was named from outside the workspace: a
// reference leading out of that root — climbing out as written, or through a
// link — halts with readscope.ErrReferenceLeadsOutOfWorkspace and nothing is
// read. A bare token is an identity resolved through a
// cascade: the tolerant same-directory scan (tier 1) wins over the catalog
// (tier 2, via catLookup); the registry tier (tier 3) is not yet active. The
// same-directory scan skips unrelated or unparseable files rather than failing
// the build (see [scanReleaseDir]); a within-tier duplicate identity halts with
// ErrDuplicateTriadIdentity, and a link in the directory leading out of the root
// it is held to halts with readscope.ErrReadLeadsOutOfWorkspace. workspaceRoot is
// the workspace the Release is read in, which decides that root.
//
// origin is the file the resolution started at — the Release a command or a
// request names — or "" where there is none. The scan does not judge that one
// entry of the directory: it was admitted where it was named, and a Release named
// through a link is read where its name leads (see [scanReleaseDir]).
func ResolveAggregate(workspaceRoot, releaseDir, origin, ref string, catLookup AggregateLookup, registryFetch AggregateRegistryFetch) (ResolvedAggregate, error) {
	if isRelativePathRef(ref) {
		path := filepath.Join(releaseDir, ref)
		if err := readscope.CheckReference(workspaceRoot, readscope.Reference{
			Anchor: releaseDir, From: releaseDir, Key: ".spec.aggregate", Value: ref, Target: path,
		}); err != nil {
			return ResolvedAggregate{}, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return ResolvedAggregate{}, &ResolverError{Entity: "Aggregate", Field: path, Message: fmt.Sprintf("failed to read file: %v", err), Err: ErrYAMLLoad()}
		}
		agg, err := LoadAggregateFromBytes(data, path)
		if err != nil {
			return ResolvedAggregate{}, err
		}
		return ResolvedAggregate{Aggregate: agg, Path: path, Bytes: data}, nil
	}
	idx, err := scanReleaseDir(workspaceRoot, releaseDir, origin)
	if err != nil {
		return ResolvedAggregate{}, err
	}
	if ra, ok := idx.aggregates[ref]; ok { // tier 1 — same-directory (wins)
		return ra, nil
	}
	if catLookup != nil { // tier 2 — catalog (agnostic lookup seam)
		if ra, ok := catLookup(ref); ok {
			return ra, nil
		}
	}
	// A bare identity the directory and the catalog both missed, and whose version
	// pin is malformed, is reported as that malformation before the registry is
	// asked: the pin names no version, so no published Aggregate could answer it,
	// and a not-found would send the author looking for an entity when the fault
	// is the pin's text.
	if pe, malformed := types.ParseVersionPin(ref).Malformed(); malformed {
		return ResolvedAggregate{}, pe.At("in the Release's spec.aggregate")
	}
	if registryFetch != nil { // tier 3 — registry (pull-only; nil at build)
		ra, found, ferr := registryFetch(ref)
		if ferr != nil {
			return ResolvedAggregate{}, ferr
		}
		if found {
			return ra, nil
		}
	}
	return ResolvedAggregate{}, idx.unresolvable("Aggregate", ref)
}

// ResolveManifest resolves a Release's spec.manifest reference to a Manifest. A
// Manifest is operator configuration authored beside the Release: it is not
// catalog-indexed and not publishable, so it has no catalog or registry tier —
// only an explicit `./` / `../` file or the tolerant same-directory scan, each
// held to the root workspaceRoot picks for releaseDir as [ResolveAggregate]'s is,
// and origin is the file the resolution started at, as it is there.
func ResolveManifest(workspaceRoot, releaseDir, origin, ref string) (ResolvedManifest, error) {
	return resolveManifest(workspaceRoot, releaseDir, origin, ref, "", ".spec.manifest")
}

// resolveManifest is [ResolveManifest] for a reference written at key in
// document — a Release's spec.manifest, where the document is no longer known
// once its extends chain is folded, or a Manifest's spec.extends, where the link
// that declares it is — so a refusal names where the reference was written.
func resolveManifest(workspaceRoot, releaseDir, origin, ref, document, key string) (ResolvedManifest, error) {
	if isRelativePathRef(ref) {
		path := filepath.Join(releaseDir, ref)
		if err := readscope.CheckReference(workspaceRoot, readscope.Reference{
			Document: document, Anchor: releaseDir, From: releaseDir, Key: key, Value: ref, Target: path,
		}); err != nil {
			return ResolvedManifest{}, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return ResolvedManifest{}, &ResolverError{Entity: "Manifest", Field: path, Message: fmt.Sprintf("failed to read file: %v", err), Err: ErrYAMLLoad()}
		}
		man, err := LoadManifestFromBytes(data, path)
		if err != nil {
			return ResolvedManifest{}, err
		}
		return ResolvedManifest{Manifest: man, Path: path, Bytes: data}, nil
	}
	idx, err := scanReleaseDir(workspaceRoot, releaseDir, origin)
	if err != nil {
		return ResolvedManifest{}, err
	}
	if rm, ok := idx.manifests[ref]; ok {
		return rm, nil
	}
	return ResolvedManifest{}, idx.unresolvable("Manifest", ref)
}

// triadIndex is the result of a same-directory scan: the Aggregate and Manifest
// entities that loaded cleanly, keyed by metadata.name (each carrying its path
// and bytes), plus the files that were skipped (unreadable / unparseable /
// load-failed) so an unresolvable reference can replay a skip that might have
// been the missing entity.
type triadIndex struct {
	aggregates map[string]ResolvedAggregate
	manifests  map[string]ResolvedManifest
	skipped    []skippedFile
}

// skippedFile records a file the scan could not index. name is the best-effort
// recovered metadata.name, or "" when even the envelope did not parse.
type skippedFile struct {
	path   string
	name   string
	reason error
}

// triadEnvelope is the minimal shape the tolerant scan parses to classify a file
// by kind and recover its identity without a full typed unmarshal — so a file
// that is malformed deeper than its envelope still yields a kind+name for the
// duplicate check and the skip record.
type triadEnvelope struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
}

// scanReleaseDir tolerantly indexes the Aggregate and Manifest entities in
// releaseDir by metadata.name. It is non-recursive (the Release's own directory
// is a deployment authoring unit, not a catalog). Only kind Aggregate and
// Manifest are indexed: Releases (the one being built and any siblings) and
// every other kind are ignored. A file that cannot be read or parsed is
// recorded in skipped rather than failing the scan — a stray broken `.yaml`
// in an app directory must never fail an unrelated build (the closure-scoped
// tolerant-load discipline). The one loud failure is a within-tier
// duplicate identity: two cleanly-loaded Aggregates (or Manifests) sharing a
// metadata.name halt with ErrDuplicateTriadIdentity.
//
// Each file is read once: the bytes parsed for the envelope are reused for the
// full typed load (via the *FromBytes loaders) and retained on the indexed
// result, so a resolved entity carries its bytes without a second read.
//
// A file the scan would read is held to the root workspaceRoot picks for the
// directory (writescope.RootFor): the workspace root where the Release's directory
// lies inside the workspace, and the directory itself where a Release typed on the
// command line, or named by the configuration, lies outside it. A link among those
// files leading out of that root is not read, and halts the scan with
// readscope.ErrReadLeadsOutOfWorkspace naming it — the second loud failure, beside
// the duplicate identity. Passing over it instead could let the catalog resolve
// another entity of the name the link stands for, or hide a duplicate of one the
// scan did index. A link leading inside is read where the scan meets it, as a file
// is.
//
// One entry is not judged: origin, the file the resolution started at — the Release
// a command or a request names, or a Manifest named on its own. It was admitted
// where it was named, by the class that named it — typed on the command line, named
// by the configuration, or judged as a request names it — and the caller has read it
// there already; meeting it again among its neighbours is not a read of workspace
// content on the scan's own account. So a Release named through a link leading out
// resolves its names beside the place it was named, the directory as spelled, with
// every other entry held as before: a neighbouring Release, Aggregate or Manifest that
// is a link leading out still halts the scan. The entry is read as every entry is,
// so the index and its duplicate check are those of any other directory.
func scanReleaseDir(workspaceRoot, releaseDir, origin string) (*triadIndex, error) {
	idx := &triadIndex{
		aggregates: map[string]ResolvedAggregate{},
		manifests:  map[string]ResolvedManifest{},
	}
	entries, err := os.ReadDir(releaseDir)
	if err != nil {
		return nil, fmt.Errorf("scan Release directory %q: %w", releaseDir, err)
	}
	root := writescope.RootFor(workspaceRoot, releaseDir)
	named := spelledPlace(origin)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		base := e.Name()
		if !strings.HasSuffix(base, ".yaml") && !strings.HasSuffix(base, ".yml") {
			continue
		}
		path := filepath.Join(releaseDir, base)
		if e.Type()&fs.ModeSymlink != 0 && spelledPlace(path) != named {
			if lerr := readscope.CheckRead(root, path); lerr != nil {
				return nil, lerr
			}
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			idx.skipped = append(idx.skipped, skippedFile{path: path, reason: rerr})
			continue
		}
		var env triadEnvelope
		if uerr := yaml.Unmarshal(data, &env); uerr != nil {
			// Envelope did not parse — kind and name are unknown; record an
			// unrecoverable skip so a later unresolvable reference can hedge.
			idx.skipped = append(idx.skipped, skippedFile{path: path, reason: uerr})
			continue
		}
		switch env.Kind {
		case "Aggregate":
			agg, lerr := LoadAggregateFromBytes(data, path)
			if lerr != nil {
				idx.skipped = append(idx.skipped, skippedFile{path: path, name: env.Metadata.Name, reason: lerr})
				continue
			}
			if _, dup := idx.aggregates[agg.Metadata.Name]; dup {
				return nil, fmt.Errorf("%w: Aggregate %q declared more than once in %s", ErrDuplicateTriadIdentity(), agg.Metadata.Name, releaseDir)
			}
			idx.aggregates[agg.Metadata.Name] = ResolvedAggregate{Aggregate: agg, Path: path, Bytes: data}
		case "Manifest":
			man, lerr := LoadManifestFromBytes(data, path)
			if lerr != nil {
				idx.skipped = append(idx.skipped, skippedFile{path: path, name: env.Metadata.Name, reason: lerr})
				continue
			}
			if _, dup := idx.manifests[man.Metadata.Name]; dup {
				return nil, fmt.Errorf("%w: Manifest %q declared more than once in %s", ErrDuplicateTriadIdentity(), man.Metadata.Name, releaseDir)
			}
			idx.manifests[man.Metadata.Name] = ResolvedManifest{Manifest: man, Path: path, Bytes: data}
		default:
			// Not a triad entity (Release, Component, Recipe, …) — not indexed,
			// not a skip; it is simply irrelevant to triad resolution.
			continue
		}
	}
	return idx, nil
}

// spelledPlace is the one spelling a file's path is compared under: absolute and
// cleaned, with no link resolved — the entry the scan meets for a file it was handed
// is the same file however either was spelled, while a link is never taken for the
// place it leads to. An empty path stays empty.
func spelledPlace(path string) string {
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}

// unresolvable builds the not-found error for a bare-identity reference. It
// replays a skipped file's reason only when that file might be the missing
// entity: definitively when a skipped file's recovered name equals ref (the
// match is proven), and hedged when a skipped file's name could not be recovered
// (it could be ref, but that cannot be proven, so the wording does not assert
// it). A reference that simply names nothing — with no plausibly-matching skip —
// gets the plain not-found.
func (idx *triadIndex) unresolvable(kind, ref string) error {
	for _, s := range idx.skipped {
		if s.name == ref {
			// Wrap the skip reason with %w (not %v) so a sentinel the loader
			// attached — notably envelope.ErrEntityEnvelope — stays
			// errors.Is-recoverable through the by-name triad scan, matching
			// the by-path loaders' recoverability.
			return fmt.Errorf("%w: needed %s %q is the unparseable file %s: %w", ErrTriadRefUnresolvable(), kind, ref, s.path, s.reason)
		}
	}
	for _, s := range idx.skipped {
		if s.name == "" {
			return fmt.Errorf("%w: %s %q not found; unparseable file %s could not be indexed and may be it: %w", ErrTriadRefUnresolvable(), kind, ref, s.path, s.reason)
		}
	}
	return fmt.Errorf("%w: no %s named %q in the Release directory, catalog, or registry", ErrTriadRefUnresolvable(), kind, ref)
}
