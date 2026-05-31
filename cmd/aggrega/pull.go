// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

var (
	pullReleasePath       string
	pullCatalogDir        string
	pullRegenerate        bool
	pullVerify            bool
	pullIncludePrerelease bool
)

var pullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Resolve catalog and source dependencies into the cache and write the per-Release lockfile",
	RunE: func(cmd *cobra.Command, args []string) error {
		if pullVerify && pullRegenerate {
			return fmt.Errorf("--verify and --regenerate are mutually exclusive")
		}
		return pullRun(pullConfig{
			ReleasePath:       pullReleasePath,
			CatalogDir:        pullCatalogDir,
			Regenerate:        pullRegenerate,
			Verify:            pullVerify,
			IncludePrerelease: pullIncludePrerelease,
		})
	},
}

func init() {
	pullCmd.Flags().StringVar(&pullReleasePath, "release", "", "Path to Release YAML file (required)")
	pullCmd.Flags().StringVar(&pullCatalogDir, "catalog", ".", "Directory containing catalog entities")
	pullCmd.Flags().BoolVar(&pullRegenerate, "regenerate", false, "Discard any existing lockfile and rebuild from current registry state")
	pullCmd.Flags().BoolVar(&pullVerify, "verify", false, "Verify the existing lockfile against the current closure without writing; exits non-zero on drift or missing lockfile")
	pullCmd.Flags().BoolVar(&pullIncludePrerelease, "include-prerelease", false, "Reserved for future version-selection logic; currently a no-op")
	if err := pullCmd.MarkFlagRequired("release"); err != nil {
		panic(err)
	}
	rootCmd.AddCommand(pullCmd)
}

// pullConfig is the input contract for the pull workflow. cobra-bound
// flag handling reads the command-line values into a pullConfig
// instance; build's auto-pull integration constructs one directly.
type pullConfig struct {
	ReleasePath       string
	CatalogDir        string
	Regenerate        bool
	Verify            bool
	IncludePrerelease bool
}

// pullRun executes the pull workflow against the supplied
// configuration. Loads workspace config + Release + Manifest +
// Aggregate, walks the catalog and source closures, and writes the
// per-Release lockfile.
//
// Returns wrapped errors on workspace-config validation failure,
// Release/Manifest/Aggregate load failure, closure walk failure
// (typically a missing catalog entity surfaced by closure.ErrUnresolvableURI),
// source descent failure (closure.ErrSourceTemplateAtLockTime), and
// lockfile write failure.
func pullRun(cfg pullConfig) error {
	releaseDir := filepath.Dir(cfg.ReleasePath)

	wsCfg, err := workspaceconfig.Load(workspaceConfigPath())
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}

	rel, err := resolver.LoadRelease(cfg.ReleasePath)
	if err != nil {
		return fmt.Errorf("load release: %w", err)
	}
	aggPath := filepath.Join(releaseDir, rel.Spec.Aggregate+".yaml")
	manifestPath := filepath.Join(releaseDir, rel.Spec.Manifest+".yaml")
	manifest, err := resolver.LoadManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("load manifest: %w", err)
	}
	aggregate, err := resolver.LoadAggregate(aggPath)
	if err != nil {
		return fmt.Errorf("load aggregate: %w", err)
	}

	// Workspace-agnostic: pull walks the closure to populate the
	// lockfile and never emits IR, so the catalog's source-file
	// accessors are unused. Pass "" for workspace root.
	cat, err := catalog.Load("", cfg.CatalogDir)
	if err != nil {
		return fmt.Errorf("catalog load: %w", err)
	}

	lockfilePath := lockfile.PathFor(cfg.ReleasePath)
	if cfg.Regenerate {
		if err := os.Remove(lockfilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("regenerate: remove existing lockfile: %w", err)
		}
	}
	if cfg.Verify {
		// Verify-only: a missing lockfile is fatal — the flag
		// presumes one exists. The no-lockfile case is what the
		// default pull (no --verify) addresses.
		if _, statErr := os.Stat(lockfilePath); statErr != nil {
			return fmt.Errorf("verify: lockfile %s not found: %w", lockfilePath, statErr)
		}
	}

	workspaceRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("workspace root: %w", err)
	}
	cacheDir := wsCfg.Workspace.CacheDir
	if cacheDir == "" {
		cacheDir = ".aggrega/cache"
	}

	externals := lockfile.LoadExternalEntities(rel, aggPath, manifestPath)
	lookup := cat.ClosureLookupWithExternals(catalog.NewDefaultOCIPuller(wsCfg), workspaceRoot, cacheDir, externals)
	cl, err := closure.ComputeClosure(rel, manifest, aggregate, wsCfg, lookup)
	if err != nil {
		return fmt.Errorf("closure: %w", err)
	}

	if cfg.Verify {
		// Verify-only path: load the existing lockfile and check
		// drift against the freshly-computed closure. No disk writes.
		// Source-artifact descent is skipped — lockfile.Verify covers
		// the entity/library/plugin axes; source artifacts have their
		// own digest gate at build-time read.
		lf, _, lerr := lockfile.LoadLockfile(lockfilePath)
		if lerr != nil {
			return fmt.Errorf("verify: load lockfile %s: %w", lockfilePath, lerr)
		}
		if verr := lockfile.Verify(lf, cl); verr != nil {
			return fmt.Errorf("verify: %w", verr)
		}
		fmt.Printf("Verify: lockfile %s matches current closure (entities=%d)\n",
			lockfilePath, len(cl.Entities))
		return nil
	}

	// Per-component source resolution. Run the shared resolve+flatten pass —
	// the same path `aggrega build` uses (pipeline.ResolveAndFlatten) — so each
	// component's source resolves against that component's own cascade-resolved
	// infra (not a flattened Release-wide bag), and the component-to-Recipe join
	// uses the same Manifest mapping the closure walker recorded (lock-time
	// cannot drift from build-time). aggBytes feeds ResolveFromInputs, which
	// re-parses internally.
	aggBytes, err := os.ReadFile(aggPath)
	if err != nil {
		return fmt.Errorf("read aggregate: %w", err)
	}
	rr, topo, err := pipeline.ResolveAndFlatten(
		context.Background(),
		pipeline.Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: aggBytes,
			AggregatePath:     catalog.RelativeToWorkspace(workspaceRoot, aggPath),
		},
		cat.CatalogLookup(),
		cat.SubAggregateLoader(),
	)
	if err != nil {
		return fmt.Errorf("resolve: %w", err)
	}
	defer resolver.PurgeParameters(&rr.Parameters)

	// Key per-component infra by the Manifest-mapped Recipe URI — the same
	// verbatim string the closure walker records as the source-descent entity
	// (catalog.ComponentRecipeURI resolves it flat-shape-first, legacy-fallback),
	// so this map joins against closure entities regardless of short-form vs
	// full-path mapping authoring. A component with no Manifest mapping carries
	// no source-descent entity and is skipped.
	perComponentInfra := map[string][]closure.ComponentSourceInfra{}
	for _, node := range topo.Nodes.All() {
		recURI, found := catalog.ComponentRecipeURI(manifest, node.ComponentType)
		if !found {
			continue
		}
		var infra map[string]any
		if bi, ok := rr.BoundaryInfra[node.InstanceID]; ok && bi != nil {
			infra = bi.Infra.TemplateAlphabeticalMapDeep()
		}
		perComponentInfra[recURI] = append(perComponentInfra[recURI],
			closure.ComponentSourceInfra{Component: node.InstanceID, Infra: infra})
	}

	sourceArtifacts, err := closure.DescendRecipeSources(
		perComponentInfra, cl, cat.ClosureRecipeLoader(), sourceResolverShim{},
		workspaceRoot, cacheDir,
	)
	if err != nil {
		return fmt.Errorf("source descent: %w", err)
	}
	cl.SourceArtifacts = sourceArtifacts

	lf := buildLockfile(cfg.ReleasePath, cl, wsCfg)
	if err := lockfile.WriteLockfile(lockfilePath, lf); err != nil {
		return fmt.Errorf("write lockfile: %w", err)
	}

	fmt.Printf("Pull complete: %s (entities=%d, source_artifacts=%d)\n",
		lockfilePath, len(cl.Entities), len(cl.SourceArtifacts))
	return nil
}

// workspaceConfigPath returns the conventional path of the
// per-workspace configuration file in the current working directory.
// Returns ".aggrega-config.yaml" — Load tolerates absence.
func workspaceConfigPath() string {
	return ".aggrega-config.yaml"
}

// buildLockfile materialises a *lockfile.Lockfile from the closure
// result. Field-for-field copy across the closure / lockfile package
// boundary; both types intentionally mirror the same shape.
func buildLockfile(releasePath string, cl closure.Closure, wsCfg workspaceconfig.Config) *lockfile.Lockfile {
	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     filepath.Base(releasePath),
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}

	for _, e := range cl.Entities {
		lf.Entities = append(lf.Entities, lockfile.Entity{
			URI:             e.URI,
			ResolvedVersion: e.ResolvedVersion,
			Digest:          e.Digest,
			ResolutionPath:  e.ResolutionPath,
			ReferencedFrom:  append([]string(nil), e.ReferencedFrom...),
		})
	}
	sort.Slice(lf.Entities, func(i, j int) bool { return lf.Entities[i].URI < lf.Entities[j].URI })

	for _, l := range cl.SchemaLibraries {
		lf.SchemaLibraries = append(lf.SchemaLibraries, lockfile.SchemaLibrary{
			URI:             l.URI,
			ResolvedVersion: l.ResolvedVersion,
			Digest:          l.Digest,
			ResolutionPath:  l.ResolutionPath,
			Consumers:       copyConsumers(l.Consumers),
		})
	}
	sort.Slice(lf.SchemaLibraries, func(i, j int) bool { return lf.SchemaLibraries[i].URI < lf.SchemaLibraries[j].URI })

	for _, l := range cl.MappingLibraries {
		lf.MappingLibraries = append(lf.MappingLibraries, lockfile.MappingLibrary{
			URI:             l.URI,
			ResolvedVersion: l.ResolvedVersion,
			Digest:          l.Digest,
			ResolutionPath:  l.ResolutionPath,
			Consumers:       copyConsumers(l.Consumers),
		})
	}
	sort.Slice(lf.MappingLibraries, func(i, j int) bool { return lf.MappingLibraries[i].URI < lf.MappingLibraries[j].URI })

	for _, w := range cl.WasmPlugins {
		lf.WasmPlugins = append(lf.WasmPlugins, lockfile.WasmPlugin{
			URI:             w.URI,
			ResolvedVersion: w.ResolvedVersion,
			Digest:          w.Digest,
			ResolutionPath:  w.ResolutionPath,
			ReferencedFrom:  append([]string(nil), w.ReferencedFrom...),
		})
	}
	sort.Slice(lf.WasmPlugins, func(i, j int) bool { return lf.WasmPlugins[i].URI < lf.WasmPlugins[j].URI })

	for _, s := range cl.SourceArtifacts {
		refs := make([]lockfile.SourceArtifactReference, 0, len(s.ReferencedBy))
		for _, r := range s.ReferencedBy {
			refs = append(refs, lockfile.SourceArtifactReference{
				Entity:       r.Entity,
				URI:          r.URI,
				Field:        r.Field,
				RequestedRef: r.RequestedRef,
			})
		}
		lf.SourceArtifacts = append(lf.SourceArtifacts, lockfile.SourceArtifact{
			Kind:          s.Kind,
			URL:           s.URL,
			PinnedVersion: s.PinnedVersion,
			Digest:        s.Digest,
			Mediatype:     s.Mediatype,
			ReferencedBy:  refs,
		})
	}

	return lf
}

func copyConsumers(in []closure.LibraryConsumer) []lockfile.LibraryConsumer {
	out := make([]lockfile.LibraryConsumer, 0, len(in))
	for _, c := range in {
		out = append(out, lockfile.LibraryConsumer{
			ConsumerURI: c.ConsumerURI,
			Fragments:   append([]string(nil), c.Fragments...),
		})
	}
	return out
}
