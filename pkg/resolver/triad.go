// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

// ErrDuplicateTriadIdentity fires when the Release's own directory holds two
// Aggregate (or two Manifest) entities that share a metadata.name — the
// same-directory analogue of the catalog's ErrDuplicateRecipeIdentity. The
// message names the directory to read distinctly from the catalog-tier duplicate.
var ErrDuplicateTriadIdentity = errors.New("duplicate Aggregate/Manifest identity (metadata.name) in the Release directory")

// ErrTriadRefUnresolvable fires when a bare-identity aggregate:/manifest:
// reference resolves to no entity in the same-directory scan, the catalog, or the
// registry.
var ErrTriadRefUnresolvable = errors.New("aggregate/manifest reference unresolvable")

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
	FromRegistry    bool
}

// ResolvedManifest is the Manifest counterpart of [ResolvedAggregate]. A Manifest
// has no catalog or registry tier (it is deployment-side operator config), so it
// resolves only from a `./`/`../` file or the same-directory scan; Path and Bytes
// are always the on-disk file and its content.
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

// ResolveAggregate resolves a Release's spec.aggregate reference to an Aggregate.
//
// A `./` or `../` reference is an explicit local file, loaded relative to
// releaseDir with no scan. A bare token is an identity resolved through a
// cascade: the tolerant same-directory scan (tier 1) wins over the catalog
// (tier 2, via catLookup); the registry tier (tier 3) is not yet active. The
// same-directory scan skips unrelated or unparseable files rather than failing
// the build (see [scanReleaseDir]); a within-tier duplicate identity halts with
// ErrDuplicateTriadIdentity.
func ResolveAggregate(releaseDir, ref string, catLookup AggregateLookup, registryFetch AggregateRegistryFetch) (ResolvedAggregate, error) {
	if isRelativePathRef(ref) {
		path := filepath.Join(releaseDir, ref)
		data, err := os.ReadFile(path)
		if err != nil {
			return ResolvedAggregate{}, &ResolverError{Entity: "Aggregate", Field: path, Message: fmt.Sprintf("failed to read file: %v", err), Err: ErrYAMLLoad}
		}
		agg, err := LoadAggregateFromBytes(data, path)
		if err != nil {
			return ResolvedAggregate{}, err
		}
		return ResolvedAggregate{Aggregate: agg, Path: path, Bytes: data}, nil
	}
	idx, err := scanReleaseDir(releaseDir)
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
// Manifest is deployment-side operator config: it is not catalog-indexed and not
// publishable, so it has no catalog or registry tier — only an explicit `./` /
// `../` file or the tolerant same-directory scan.
func ResolveManifest(releaseDir, ref string) (ResolvedManifest, error) {
	if isRelativePathRef(ref) {
		path := filepath.Join(releaseDir, ref)
		data, err := os.ReadFile(path)
		if err != nil {
			return ResolvedManifest{}, &ResolverError{Entity: "Manifest", Field: path, Message: fmt.Sprintf("failed to read file: %v", err), Err: ErrYAMLLoad}
		}
		man, err := LoadManifestFromBytes(data, path)
		if err != nil {
			return ResolvedManifest{}, err
		}
		return ResolvedManifest{Manifest: man, Path: path, Bytes: data}, nil
	}
	idx, err := scanReleaseDir(releaseDir)
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
func scanReleaseDir(releaseDir string) (*triadIndex, error) {
	idx := &triadIndex{
		aggregates: map[string]ResolvedAggregate{},
		manifests:  map[string]ResolvedManifest{},
	}
	entries, err := os.ReadDir(releaseDir)
	if err != nil {
		return nil, fmt.Errorf("scan Release directory %q: %w", releaseDir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		base := e.Name()
		if !strings.HasSuffix(base, ".yaml") && !strings.HasSuffix(base, ".yml") {
			continue
		}
		path := filepath.Join(releaseDir, base)
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
				return nil, fmt.Errorf("%w: Aggregate %q declared more than once in %s", ErrDuplicateTriadIdentity, agg.Metadata.Name, releaseDir)
			}
			idx.aggregates[agg.Metadata.Name] = ResolvedAggregate{Aggregate: agg, Path: path, Bytes: data}
		case "Manifest":
			man, lerr := LoadManifestFromBytes(data, path)
			if lerr != nil {
				idx.skipped = append(idx.skipped, skippedFile{path: path, name: env.Metadata.Name, reason: lerr})
				continue
			}
			if _, dup := idx.manifests[man.Metadata.Name]; dup {
				return nil, fmt.Errorf("%w: Manifest %q declared more than once in %s", ErrDuplicateTriadIdentity, man.Metadata.Name, releaseDir)
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
			return fmt.Errorf("%w: needed %s %q is the unparseable file %s: %w", ErrTriadRefUnresolvable, kind, ref, s.path, s.reason)
		}
	}
	for _, s := range idx.skipped {
		if s.name == "" {
			return fmt.Errorf("%w: %s %q not found; unparseable file %s could not be indexed and may be it: %w", ErrTriadRefUnresolvable, kind, ref, s.path, s.reason)
		}
	}
	return fmt.Errorf("%w: no %s named %q in the Release directory, catalog, or registry", ErrTriadRefUnresolvable, kind, ref)
}
