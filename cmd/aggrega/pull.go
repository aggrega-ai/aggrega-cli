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
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

var (
	pullReleasePath       string
	pullCatalogDir        string
	pullRegenerate        bool
	pullIncludePrerelease bool
)

var pullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Resolve catalog and source dependencies into the cache and write the per-Release lockfile",
	RunE: func(cmd *cobra.Command, args []string) error {
		return pullRun(pullConfig{
			ReleasePath:       pullReleasePath,
			CatalogDir:        pullCatalogDir,
			Regenerate:        pullRegenerate,
			IncludePrerelease: pullIncludePrerelease,
		})
	},
}

func init() {
	pullCmd.Flags().StringVar(&pullReleasePath, "release", "", "Path to Release YAML file (required)")
	pullCmd.Flags().StringVar(&pullCatalogDir, "catalog", ".", "Directory containing catalog entities")
	pullCmd.Flags().BoolVar(&pullRegenerate, "regenerate", false, "Discard any existing lockfile and rebuild from current registry state")
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

	cat, err := catalog.Load(cfg.CatalogDir)
	if err != nil {
		return fmt.Errorf("catalog load: %w", err)
	}

	lockfilePath := lockfilePathFor(cfg.ReleasePath)
	if cfg.Regenerate {
		if err := os.Remove(lockfilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("regenerate: remove existing lockfile: %w", err)
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

	externals := loadExternalEntities(rel, aggPath, manifestPath)
	lookup := cat.ClosureLookupWithExternals(newDefaultOCIPuller(wsCfg), workspaceRoot, cacheDir, externals)
	cl, err := closure.ComputeClosure(rel, manifest, aggregate, wsCfg, lookup)
	if err != nil {
		return fmt.Errorf("closure: %w", err)
	}

	sourceArtifacts, err := closure.DescendRecipeSources(
		rel, cl, cat.ClosureRecipeLoader(), sourceResolverShim{},
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

// loadExternalEntities reads the Release's referenced Aggregate and
// Manifest files as raw bytes and returns them keyed by the URI the
// closure walker uses to reference them (the Release's spec.aggregate
// / spec.manifest values). The bytes are passed verbatim into
// ClosureLookupWithExternals so the walker hits these URIs at the
// "external" tier instead of falling through to Tier 3.
//
// Aggregate and Manifest are deployment-side entities (loaded by
// resolver.LoadAggregate / resolver.LoadManifest) and are not part
// of the in-memory catalog (catalog.Load silently skips Manifest
// kinds). Reading the raw bytes here keeps the digest computation
// consistent with the Tier-1 catalog path (sha256 over the on-disk
// YAML).
func loadExternalEntities(rel *types.Release, aggPath, manifestPath string) map[string][]byte {
	out := map[string][]byte{}
	if data, err := os.ReadFile(aggPath); err == nil {
		out[rel.Spec.Aggregate] = data
	}
	if data, err := os.ReadFile(manifestPath); err == nil {
		out[rel.Spec.Manifest] = data
	}
	return out
}

// lockfilePathFor returns the conventional lockfile path for a given
// Release file path. Replaces a trailing ".yaml" with ".lock"; falls
// back to appending ".lock" when the stem is non-yaml.
func lockfilePathFor(releasePath string) string {
	ext := filepath.Ext(releasePath)
	if ext == ".yaml" || ext == ".yml" {
		return strings.TrimSuffix(releasePath, ext) + ".lock"
	}
	return releasePath + ".lock"
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

	lf.CueRegistry = lockfile.CueRegistry{
		Default: wsCfg.Workspace.CueRegistry.Default,
	}
	for _, ov := range wsCfg.Workspace.CueRegistry.Overrides {
		lf.CueRegistry.Overrides = append(lf.CueRegistry.Overrides,
			lockfile.CueRegistryOverride{Prefix: ov.Prefix, Registry: ov.Registry})
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

// newDefaultOCIPuller returns the production catalog.OCIPuller backed
// by pkg/oci.NewRemoteRepository per Pull-call. Returns nil when the
// workspace is configured offline, so closure_lookup's Tier-3 path
// returns NotFound without attempting any network access.
func newDefaultOCIPuller(wsCfg workspaceconfig.Config) catalog.OCIPuller {
	if wsCfg.Workspace.Offline {
		return nil
	}
	return defaultOCIPuller{}
}

type defaultOCIPuller struct{}

func (defaultOCIPuller) Pull(ctx context.Context, reference, tag string) (*oci.PullResult, error) {
	repo, err := oci.NewRemoteRepository(reference)
	if err != nil {
		return nil, err
	}
	return oci.Pull(ctx, repo, tag)
}
