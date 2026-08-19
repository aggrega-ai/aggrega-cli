// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// RunCatalogWide resolves the workspace's catalog directories and runs the
// structural publishing-linter rules over every catalog entity found under them.
// Catalog directories come from the repeatable catalogFlagDirs prepended to the
// workspace config's catalog_dirs; at least one source must be set — a whole-tree
// enumeration with no configured directory is refused rather than silently
// scanning the working directory. This is the conventions counterpart to
// whole-catalog correctness (`aggrega validate`): correctness halts on a broken
// entity, conventions report publishing-discipline advisories and never halt on a
// convention finding alone.
//
// collect optionally gathers the findings as data alongside the human report; nil
// collects nothing.
func RunCatalogWide(rw *workspaceconfig.ResolvedWorkspace, catalogFlagDirs []string, out, diagOut io.Writer, collect *FindingCollector) error {
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return fmt.Errorf("load workspace config: %w", err)
	}
	wsCfg := rw.Config()
	if len(catalogFlagDirs) == 0 && len(wsCfg.Workspace.CatalogDirs) == 0 {
		return fmt.Errorf("OPS_ERROR: whole-catalog conventions check needs catalog directories to scan; pass --catalog or set `workspace.catalog_dirs` in .aggrega-config.yaml (or check a specific Release with --release)")
	}
	dirs := workspaceconfig.ResolveCatalogDirs(catalogFlagDirs, wsCfg, rw.Root())
	files, err := walkCatalogEntityFiles(rw.Root(), dirs, wsCfg.ResolvedArtifactCacheDir())
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	return lintConventionsFiles(rw, catalogFlagDirs, paths, out, diagOut, collect)
}

// RunRelease computes a Release's resolved closure through the SAME build
// machinery the compiler uses (the shared closure resolver over the lazy lookup)
// and runs the structural publishing-linter rules over the catalog entity files
// that closure reaches. Entities are resolved to their author-owned catalog
// files; registry-pinned entities living in a content-addressed cache slot carry
// no local file to lint and are skipped (the publishing-linter checks author-time
// conventions, which only apply to locally-authored files). Findings are advisory
// — conventions never halt on a convention finding alone.
//
// collect optionally gathers the findings as data alongside the human report; nil
// collects nothing.
func RunRelease(rw *workspaceconfig.ResolvedWorkspace, releasePath string, catalogFlagDirs []string, out, diagOut io.Writer, collect *FindingCollector) error {
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}
	wsRoot := rw.Root()
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	dirs := workspaceconfig.ResolveCatalogDirs(catalogFlagDirs, wsCfg, wsRoot)

	lazyView := catalog.NewLazyLookup(wsRoot, cacheDir, dirs, nil)
	lf, _, lfErr := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	var lockForClosure *lockfile.Lockfile
	if lfErr == nil {
		lazyView.SetPins(catalog.PinsFromLockfile(lf))
		lockForClosure = lf
	}
	rc, cerr := compilerops.ResolveClosure(compilerops.ClosureInputs{
		View:            lazyView,
		ReleasePath:     releasePath,
		WorkspaceRoot:   wsRoot,
		WorkspaceConfig: wsCfg,
		Lockfile:        lockForClosure,
		Augment:         true,
	})
	if cerr != nil {
		return cerr
	}

	files, err := walkCatalogEntityFiles(wsRoot, dirs, cacheDir)
	if err != nil {
		return err
	}
	index := map[string]string{}
	for _, f := range files {
		index[f.Kind+"|"+f.Name] = f.Path
	}
	paths := closureEntityFiles(lazyView, rc.Closure, index)
	return lintConventionsFiles(rw, catalogFlagDirs, paths, out, diagOut, collect)
}

// lintConventionsFiles runs the structural publishing-linter rules (the default
// rule set minus schema-evolution — that rule needs a previous registry version
// and stays single-entity, --pre-publish only) over each entity file in paths, at
// warn severity, using the workspace's configured linter plugin and
// publishing-discipline policy defaults. It is the shared core of the
// whole-catalog and release-scoped conventions passes: both resolve a set of
// entity files, then hand them here, reusing the same per-entity dispatch the
// single-entity lint runs (buildRuleInputs + DispatchPublishingLinterBatch). A
// convention finding is advisory and never halts; a plugin-load, read, or parse
// failure is a real error and does halt. Findings are written to diagOut
// (os.Stderr in production); the final summary is written to out (os.Stdout).
// collect optionally gathers the same findings as data; nil collects nothing.
func lintConventionsFiles(rw *workspaceconfig.ResolvedWorkspace, catalogFlagDirs []string, paths []string, out, diagOut io.Writer, collect *FindingCollector) error {
	wsCfg := rw.Config()
	dirs := workspaceconfig.ResolveCatalogDirs(catalogFlagDirs, wsCfg, rw.Root())
	lazyView := catalog.NewLazyLookup(rw.Root(), wsCfg.ResolvedArtifactCacheDir(), dirs, nil)
	resolvedPlugin, err := ResolveLinterPlugin("", wsCfg, lazyView.PluginByURI,
		linterCacheResolve(rw.Root(), wsCfg, lazyView.PluginFromCacheByURI))
	if err != nil {
		return err
	}
	pluginSpec := resolver.PluginSpec("publishing-linter", resolvedPlugin)
	policies := effectivePolicies(wsCfg.Lint.PublishingDiscipline, func(_ string, ws bool) bool { return ws })
	export := ""
	if len(wsCfg.Plugins.Linter) > 0 {
		export = wsCfg.Plugins.Linter[0].Function
	}

	findingCount := 0
	for _, path := range paths {
		proposedBytes, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("aggrega lint: read %s: %w", path, err)
		}
		proposedJSON, header, err := yamlToLinterJSON(proposedBytes)
		if err != nil {
			return fmt.Errorf("aggrega lint: parse %s: %w", path, err)
		}
		inputs := buildRuleInputs(header.Kind, proposedJSON, nil, policies, path, false)
		rawFindings, err := validator.DispatchPublishingLinterBatch(pluginSpec, export, inputs)
		if err != nil {
			return fmt.Errorf("aggrega lint: dispatch %s: %w", path, err)
		}
		aggregated := downgradeToWarning(aggregateFindings(rawFindings))
		collect.Add(aggregated, path)
		if len(aggregated) == 0 {
			continue
		}
		fmt.Fprintf(diagOut, "%s:\n", path)
		printFindings(aggregated, diagOut)
		findingCount += len(aggregated)
	}
	collect.Entities(len(paths))
	fmt.Fprintf(out, "conventions checked: %d entit%s, %d advisory finding(s)\n",
		len(paths), plural(len(paths), "y", "ies"), findingCount)
	return nil
}

// plural picks the singular or plural suffix for n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// catalogEntityFile records a catalog entity file discovered by walking the
// catalog directories, with the identity fields peeled from its YAML header. It
// drives the conventions passes: the whole-catalog pass lints every Path; the
// release pass indexes by Kind+Name to map a closure entity back to its file.
type catalogEntityFile struct {
	Path    string
	Kind    string
	Name    string
	Version string
	Target  string
}

// isLintableCatalogKind reports whether a peeked kind names a published catalog
// entity the publishing-linter rules apply to. Deployment- and authoring-side
// kinds are not published catalog entities and are excluded from the conventions
// passes.
//
// It reads the declared entity-kind vocabulary rather than naming the kinds here.
// This is the gate the whole walk turns on — a kind it declines is dropped before
// its file enters the name index, so that file reaches no convention rule and
// nothing reports that it was skipped. A kind missing from a list written here is
// therefore silently unlinted, which is exactly what happened to the two library
// kinds added most recently.
func isLintableCatalogKind(kind string) bool {
	return entitykind.IsConventionsLinted(entitykind.Kind(kind))
}

// walkCatalogEntityFiles walks dirs in order, reads every .yaml / .yml file, and
// returns one record per file whose top-level kind names a published catalog
// entity. Files that do not parse as an entity header, or whose kind is not a
// published catalog kind, are skipped silently — the catalog tree legitimately
// carries non-entity YAML. The result is sorted by path for deterministic
// iteration and reporting.
//
// cacheDir is the workspace's artifact cache location, interpreted relative to
// wsRoot; the walk steps over it, so an entity resolved from a cache slot is
// never returned as an author-owned file. That is what lets the conventions
// passes keep their promise to report only against files the author can edit —
// a catalog directory commonly IS the workspace root, which puts the cache
// inside the walked tree, but it may equally sit above or beside it, which is
// why the cache is anchored on wsRoot rather than on each walked directory.
// Passing "" for cacheDir disables the skip.
func walkCatalogEntityFiles(wsRoot string, dirs []string, cacheDir string) ([]catalogEntityFile, error) {
	var files []catalogEntityFile
	seen := map[string]struct{}{}
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				// A missing catalog directory is reported; a per-file read error is
				// surfaced when the file is later read for linting.
				if os.IsNotExist(err) {
					return nil
				}
				return nil
			}
			if d.IsDir() {
				return catalog.SkipDirIfArtifactCache(wsRoot, path, cacheDir)
			}
			ext := filepath.Ext(path)
			if ext != ".yaml" && ext != ".yml" {
				return nil
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			var header lintEntityHeader
			if uerr := yaml.Unmarshal(data, &header); uerr != nil {
				return nil
			}
			if !isLintableCatalogKind(header.Kind) {
				return nil
			}
			abs, aerr := filepath.Abs(path)
			if aerr != nil {
				abs = path
			}
			if _, dup := seen[abs]; dup {
				return nil
			}
			seen[abs] = struct{}{}
			files = append(files, catalogEntityFile{
				Path:    path,
				Kind:    header.Kind,
				Name:    header.Metadata.Name,
				Version: header.Metadata.Version,
				Target:  header.Spec.Capability,
			})
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk catalog directory %s: %w", dir, err)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// closureEntityFiles maps the entities a Release's closure reaches back to their
// author-owned catalog files. Recipes and FeatureRecipes resolve through the lazy
// view's local-path bridge; Components, Features, Plugins, and libraries resolve
// through the Kind+Name index built from the catalog walk. A closure entity with
// no local file (a registry-pinned cache slot) or one absent from the index is
// skipped — the conventions rules apply to locally-authored files. Sub-aggregate
// references (KindHint "aggregate") are authoring-side and are not conventions-
// linted. The returned paths are deduplicated and sorted for determinism.
func closureEntityFiles(lazyView *catalog.LazyLookup, cl closure.Closure, index map[string]string) []string {
	set := map[string]struct{}{}
	add := func(p string) {
		if p != "" {
			set[p] = struct{}{}
		}
	}
	byName := func(kind, name string) string { return index[kind+"|"+name] }
	for _, e := range cl.Entities {
		switch e.KindHint {
		case "recipe":
			if r, ok := lazyView.RecipeByURI(e.URI); ok {
				if p, ok := lazyView.LocalRecipePath(r.Spec.Capability, r.Metadata.Name, r.Metadata.Version); ok {
					add(p)
				}
			}
		case "feature_recipe":
			if fr, ok := lazyView.FeatureRecipeByURI(e.URI); ok {
				if p, ok := lazyView.LocalFeatureRecipePath(fr.Spec.Capability, fr.Metadata.Name, fr.Metadata.Version); ok {
					add(p)
				}
			}
		case "aggregate":
			// Authoring-side; not conventions-linted.
		default:
			if c, ok := lazyView.Component(e.URI); ok {
				add(byName("Component", c.Metadata.Name))
				continue
			}
			if f, ok := lazyView.Feature(e.URI); ok {
				add(byName("Feature", f.Metadata.Name))
			}
		}
	}
	for _, p := range cl.Plugins {
		if pl, ok := lazyView.PluginByURI(p.URI); ok {
			add(byName("Plugin", pl.Metadata.Name))
		}
	}
	// Every declared library axis, not a chosen subset: a locally-authored library
	// on an axis this loop skipped would reach no convention rule at all, and
	// nothing else would report that it had been skipped.
	for _, axis := range closure.LibraryAxes() {
		for _, lib := range axis.Libraries(&cl) {
			if name, ok := lazyView.LibraryName(axis, lib.URI); ok {
				add(byName(axis.Kind, name))
			}
		}
	}
	paths := make([]string, 0, len(set))
	for p := range set {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}
