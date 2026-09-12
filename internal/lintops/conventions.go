// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
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
//
// ctx ends the pass where the caller stops wanting it. This is the pass whose
// cost scales with the catalog — one guest dispatch per entity over every entity
// the configured directories hold — so it is the one where abandoning the work
// and discarding the answer are materially different outcomes. Both the tree
// walk and the per-entity loop observe it, so a stop is honoured whichever of
// the two the pass is in.
func RunCatalogWide(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, catalogFlagDirs []string, out, diagOut io.Writer, collect *FindingCollector) error {
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return fmt.Errorf("load workspace config: %w", err)
	}
	wsCfg := rw.Config()
	if len(catalogFlagDirs) == 0 && len(wsCfg.Workspace.CatalogDirs) == 0 {
		return fmt.Errorf("OPS_ERROR: whole-catalog conventions check needs catalog directories to scan; pass --catalog or set `workspace.catalog_dirs` in .aggrega-config.yaml (or check a specific Release with --release)")
	}
	dirs := workspaceconfig.ResolveCatalogDirs(catalogFlagDirs, wsCfg, rw.Root())
	files, passedOver, err := walkCatalogEntityFiles(ctx, rw.Root(), dirs, wsCfg.ResolvedArtifactCacheDir())
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	// No Release names this pass, and the record of the plugin identities trusted
	// for the workspace's verdict attachments sits beside a Release — so there is
	// no record to locate here and the linter attachments resolve pin-free, exactly
	// as they did before that record existed. The release-scoped pass below is the
	// lint path that does hold them to it, and it is the same path that already
	// reads the lockfile beside the Release, so both files are read from one place.
	if err := lintConventionsFiles(ctx, rw, catalogFlagDirs, paths, pluginpath.PinMaps{}, out, diagOut, collect); err != nil {
		return err
	}
	// This is the pass whose summary claims a corpus — "every catalog entity under
	// the catalog directories" — so it is the pass that owes the reader the part of
	// the tree the claim does not cover. Without this line the count above is the
	// only thing said about coverage, and a kind the walk declines is indistinguishable
	// from a kind that was checked and found clean. The release-scoped pass makes no
	// such claim: it reports on one Release's dependency closure, and membership of
	// that closure, not the kind gate, is what decides which files it reaches.
	writePassedOverKinds(out, passedOver)
	return nil
}

// writePassedOverKinds names the entity kinds the whole-catalog walk recognised
// and did not check, so the summary's count is read as the corpus it actually
// covers. It writes nothing when the walk passed nothing over, which is the
// ordinary case for a catalog tree holding catalog entities alone.
//
// Kinds are named in the entity-kind vocabulary's own declared order, so the line
// is byte-identical across runs over the same tree without this file holding an
// ordering of its own.
func writePassedOverKinds(out io.Writer, passedOver map[string]int) {
	if len(passedOver) == 0 {
		return
	}
	parts := make([]string, 0, len(passedOver))
	for _, k := range entitykind.Kinds() {
		n := passedOver[string(k)]
		if n == 0 {
			continue
		}
		noun := string(k)
		if n != 1 {
			noun += "s"
		}
		parts = append(parts, fmt.Sprintf("%d %s", n, noun))
	}
	fmt.Fprintf(out, "not checked: %s — this pass covers catalog entity kinds; check one of these directly with `aggrega lint <path>`\n",
		strings.Join(parts, ", "))
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
//
// ctx ends the pass where the caller stops wanting it, at the same two places
// the whole-catalog pass observes it — the tree walk and the per-entity loop —
// plus once ahead of the closure resolution, which is the step this pass adds
// and the one that reads the most before any entity is checked.
func RunRelease(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, releasePath string, catalogFlagDirs []string, out, diagOut io.Writer, collect *FindingCollector) error {
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return err
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

	// The deprecation report over the lockfile's recorded markers — the same report
	// the build reads, so this pass names the same references rather than a subset.
	// A pass that said nothing about a withdrawn dependency would read as though
	// there were none.
	//
	// It is reported BEFORE the gates and the pass below, because it depends on
	// neither: a recorded marker is a fact about the lockfile alone, so a workspace
	// whose closure does not resolve — or whose gate refuses — still learns that a
	// pin has been withdrawn, which is often the explanation. It does not move the
	// verdict, for the reason the build's own default records: a marker is registry
	// state applied after the version was pinned, and the dial that turns it into a
	// halt governs the build alone. A workspace with no lockfile has no recorded
	// markers either, so reporting nothing there skips nothing.
	for _, f := range lockForClosure.DeprecationFindings() {
		fmt.Fprintf(diagOut, "deprecation: %s\n", f.Notice())
	}

	// The same report over the other store, for the same reason and with the same
	// verdict — none. A plugin attached at a verdict role is recorded beside the
	// lockfile rather than pinned in it, so it has no row the report above can reach;
	// a pass silent about a withdrawn linter would read as though there were none,
	// and this is the pass that runs those linters. It is read before the record's
	// own dropped-gate refusal below, so a withdrawal is stated even where that
	// refusal stops the pass — a withdrawal is often the explanation.
	trustRecord, _, trustErr := pluginstrust.LoadOrStart(pluginstrust.PathFor(releasePath))
	if trustErr != nil {
		return trustErr
	}
	for _, f := range trustRecord.DeprecationFindings() {
		fmt.Fprintf(diagOut, "deprecation: %s\n", f.Notice())
	}

	// Hold this Release's configuration to the record of the plugin identities
	// trusted for its verdict attachments, and carry the recorded digests into the
	// linter socket's resolution. It runs before any entity is linted, so a gate
	// that was reviewed and has since been dropped from the configuration refuses
	// the pass rather than being discovered after the findings are printed.
	//
	// This is the lint path that names a Release, and therefore the one that can
	// locate the record — the same reason it is the lint path that reads the
	// lockfile above. The single-entity pass and the whole-catalog pass name none,
	// and say so where they resolve their attachments.
	trusted, terr := compilerops.VerdictPluginTrust(lazyView, wsCfg, releasePath)
	if terr != nil {
		return terr
	}
	// Checked ahead of the closure resolution, which is the heaviest read this
	// pass performs before any entity is checked. The resolution carries the same
	// context on into its own registry steps, so this check is the cheap early
	// exit rather than the only one: a caller that gives up mid-resolution stops
	// the fetches underneath it too.
	if err := stopped(ctx); err != nil {
		return err
	}
	rc, cerr := compilerops.ResolveClosure(ctx, compilerops.ClosureInputs{
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

	// The kinds the walk passes over are not reported here. This pass reports on
	// one Release's dependency closure, and the closure decides which files it
	// reaches; naming the kinds the walk declined would suggest everything else in
	// the walked tree was checked, when most of it was simply outside the closure.
	files, _, err := walkCatalogEntityFiles(ctx, wsRoot, dirs, cacheDir)
	if err != nil {
		return err
	}
	index := map[string]string{}
	for _, f := range files {
		index[f.Kind+"|"+f.Name] = f.Path
	}
	paths := closureEntityFiles(lazyView, rc.Closure, index)
	lintErr := lintConventionsFiles(ctx, rw, catalogFlagDirs, paths, trusted, out, diagOut, collect)

	// A Manifest mapping the catalog contradicts is reported here rather than
	// halting ahead of the pass, so the conventions report the author came for
	// arrives whole. It is not a convention finding, though, and it does not
	// inherit the advisory exit code conventions carry: the mapping names a recipe
	// that does not implement the mapped type, which is wrong however the author
	// feels about conventions, so it moves the verdict.
	//
	// A real lint failure — a plugin that would not load, a file that would not
	// read — wins the returned error, because it means the pass itself did not
	// happen and reporting a mapping verdict over an incomplete pass would overstate
	// what ran.
	for _, f := range rc.ManifestConsistencyFindings {
		fmt.Fprintf(diagOut, "mapping: %s\n", f)
	}
	if lintErr != nil {
		return lintErr
	}
	if n := len(rc.ManifestConsistencyFindings); n > 0 {
		return fmt.Errorf("release manifest mapping: %d diagnostic(s)", n)
	}
	return nil
}

// lintConventionsFiles runs the structural publishing-linter rules (the default
// rule set minus schema-evolution — that rule needs a previous registry version
// and stays single-entity, --pre-publish only) over each entity file in paths, at
// warn severity, at every linter attachment the workspace configures and under
// its publishing-discipline policy defaults. It is the shared core of the
// whole-catalog and release-scoped conventions passes: both resolve a set of
// entity files, then hand them here, reusing the same per-entity dispatch the
// single-entity lint runs (buildRuleInputs + dispatchLinterAttachments). A
// convention finding is advisory and never halts; a plugin-load, read, or parse
// failure is a real error and does halt. Findings are written to diagOut
// (os.Stderr in production); the final summary is written to out (os.Stdout).
// collect optionally gathers the same findings as data; nil collects nothing.
//
// trusted carries the digests recorded as trusted for the plugins that return an
// accept-or-reject verdict, threaded from the caller rather than read here,
// because only one of the two callers can locate the record: it sits beside a
// Release, and the whole-catalog pass names none. Both arms of the linter socket's
// resolution receive it, so a recorded digest governs the bytes whichever tier
// serves them. An empty set is the ordinary case and resolves pin-free.
//
// ctx is observed once per entity, at the top of the loop, which is the seam
// that makes a stop reach the work: this loop is where both passes spend their
// time, one guest dispatch per entity, and an entity not yet reached is an
// entity never read, never parsed and never dispatched.
//
// A stop returns the caller's own reason and no summary. That is deliberate: the
// summary line claims a corpus — "conventions checked: N entities" — and
// printing it after a stop would report a partial walk in the words of a complete
// one, which is the one thing a reader cannot recover from the output.
func lintConventionsFiles(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, catalogFlagDirs []string, paths []string, trusted pluginpath.PinMaps, out, diagOut io.Writer, collect *FindingCollector) error {
	wsCfg := rw.Config()
	dirs := workspaceconfig.ResolveCatalogDirs(catalogFlagDirs, wsCfg, rw.Root())
	lazyView := catalog.NewLazyLookup(rw.Root(), wsCfg.ResolvedArtifactCacheDir(), dirs, nil)
	attachments, err := ResolveLinterPlugins("", wsCfg, trusted, lazyView.PluginByURI,
		linterCacheResolve(rw.Root(), wsCfg, trusted, lazyView.PluginFromCacheByURI))
	if err != nil {
		return err
	}
	policies := effectivePolicies(wsCfg.Lint.PublishingDiscipline, func(_ string, ws bool) bool { return ws })

	findingCount := 0
	for _, path := range paths {
		if err := stopped(ctx); err != nil {
			return err
		}
		proposedBytes, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("aggrega lint: read %s: %w", path, err)
		}
		proposedJSON, header, err := yamlToLinterJSON(proposedBytes)
		if err != nil {
			return fmt.Errorf("aggrega lint: parse %s: %w", path, err)
		}
		inputs := buildRuleInputs(ruleInputSources{
			Kind:                 header.Kind,
			Proposed:             proposedJSON,
			ImplementedComponent: implementedComponentJSON(header, lazyView.Component),
			Policies:             policies,
			EntityPath:           path,
		})
		rawFindings, err := dispatchLinterAttachments(ctx, attachments, inputs)
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
// its file enters the name index, so that file reaches no convention rule. A kind
// missing from a list written here would therefore be unlinted, which is exactly
// what happened to the two library kinds added most recently; reading the declared
// vocabulary is what keeps a kind's coverage a property of the vocabulary rather
// than of anyone remembering to extend a list.
func isLintableCatalogKind(kind string) bool {
	return entitykind.IsConventionsLinted(entitykind.Kind(kind))
}

// walkCatalogEntityFiles walks dirs in order, reads every .yaml / .yml file, and
// returns one record per file whose top-level kind names a published catalog
// entity. The result is sorted by path for deterministic iteration and reporting.
//
// The second return counts, per kind, the files the walk recognised as entities
// and did not return: a top-level kind that IS in the declared entity-kind
// vocabulary but that the publishing conventions do not cover. A caller reporting
// on the walked tree owes those to the reader, because a declined file reaches no
// convention rule and would otherwise be indistinguishable from a file that was
// checked and found clean.
//
// Anything else the walk passes over is absent from that count by design and is
// skipped silently: a file that does not parse as an entity header, and a file
// whose top-level kind is not one of the entity kinds at all. A catalog tree
// legitimately carries YAML that is not an Aggrega document — chart values,
// workflow definitions, editor configuration — and every such file names some
// kind or none. Counting those would report the tree's ordinary contents as a
// coverage gap.
//
// cacheDir is the workspace's artifact cache location, interpreted relative to
// wsRoot; the walk steps over it, so an entity resolved from a cache slot is
// never returned as an author-owned file. That is what lets the conventions
// passes keep their promise to report only against files the author can edit —
// a catalog directory commonly IS the workspace root, which puts the cache
// inside the walked tree, but it may equally sit above or beside it, which is
// why the cache is anchored on wsRoot rather than on each walked directory.
// Passing "" for cacheDir disables the skip.
//
// ctx ends the walk where the caller stops wanting it. The walk reads and parses
// the YAML header of every file it meets, so over a real catalog it is work in
// its own right and not merely a directory listing; the stop is returned from
// the callback, which is how filepath.WalkDir is asked to abandon a traversal,
// and it is the caller's own reason rather than fs.SkipAll — skipping the rest
// would return a SHORT file list as though the walk had completed, which is a
// partial answer wearing a complete one's shape.
func walkCatalogEntityFiles(ctx context.Context, wsRoot string, dirs []string, cacheDir string) ([]catalogEntityFile, map[string]int, error) {
	var files []catalogEntityFile
	passedOver := map[string]int{}
	seen := map[string]struct{}{}
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if serr := stopped(ctx); serr != nil {
				return serr
			}
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
			// Identity is established before the kind gate so that a file reached
			// twice — catalog directories are allowed to overlap — is counted once
			// whichever side of the gate it falls on.
			abs, aerr := filepath.Abs(path)
			if aerr != nil {
				abs = path
			}
			if _, dup := seen[abs]; dup {
				return nil
			}
			seen[abs] = struct{}{}
			if !isLintableCatalogKind(header.Kind) {
				if entitykind.IsKind(entitykind.Kind(header.Kind)) {
					passedOver[header.Kind]++
				}
				return nil
			}
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
			// A stop is returned as the caller's own reason, not as a walk failure:
			// naming the directory would read as a problem with that directory, when
			// what happened is that the caller withdrew. Every other error genuinely
			// is about the directory and keeps the location.
			if ctx.Err() != nil {
				return nil, nil, err
			}
			return nil, nil, fmt.Errorf("walk catalog directory %s: %w", dir, err)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, passedOver, nil
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
