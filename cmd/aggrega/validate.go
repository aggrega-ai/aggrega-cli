// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

// newValidateCmd builds the `validate` verb: CORRECTNESS checking across three
// symmetric scopes. It complements `build` (which needs a complete Aggregate +
// Manifest + Release closure and emits artifacts) and `lint` (which checks
// publishing conventions):
//
//   - No argument: whole-catalog correctness — every catalog entity is loaded
//     and its schema-pillar structure, library resolution, extends chains, and
//     provider input-kind capabilities are checked.
//   - --release <path>: release-scoped correctness — the entities a Release's
//     dependency closure reaches are validated (closure-scoped pillars, extends,
//     and pre-render authored-value conformance).
//   - <entity>: single-entity correctness — one catalog entity file is schema-
//     and shape-checked; a Component or Feature additionally has its pillar
//     structure checked, and a Recipe is driven through the real compilation
//     pipeline around a synthesized minimal deployment so an author sees the
//     isolated rendered output before wiring the entity into any Aggregate. A
//     Plugin is schema-checked only — it carries no values or pillar surface
//     and no render tier applies.
//
// Output is deterministic and the command writes nothing to disk.
func newValidateCmd() *cobra.Command {
	var catalogDirs []string
	var releasePath string
	var pluginPath string
	var pluginType string
	c := &cobra.Command{
		Use:   "validate [entity-path]",
		Short: "Check correctness across the catalog, a Release, or one entity",
		Long: "Check CORRECTNESS across three symmetric scopes.\n\n" +
			"With no argument, validate checks the WHOLE catalog: schema-pillar\n" +
			"structure, cross-document library resolution, extends chains, and provider\n" +
			"input-kind capabilities, over every catalog entity.\n\n" +
			"With --release, validate checks the entities a Release's dependency closure\n" +
			"reaches: closure-scoped pillars, extends, and pre-render authored-value\n" +
			"conformance — the same correctness a build applies, scoped to that Release.\n\n" +
			"With an entity path, validate checks one entity file. All kinds are schema-\n" +
			"and shape-checked; a Component or Feature has its pillar structure checked;\n" +
			"a Recipe is additionally driven through the real compilation pipeline around\n" +
			"a synthesized minimal deployment (resolution and guards, then a real isolated\n" +
			"render of its output); a Plugin is schema-checked only, as it has no pillar\n" +
			"or render surface. The command writes nothing to disk.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := ""
			if len(args) > 0 {
				path = args[0]
			}
			// --type declares the plugin type for a binary-only .wasm check and is
			// meaningful only alongside --plugin; reject it on its own so it never
			// silently no-ops on a catalog-, release-, or entity-scoped run.
			if pluginType != "" && pluginPath == "" {
				return fmt.Errorf("OPS_ERROR: --type applies only with --plugin; pass --plugin <path-to-.wasm> to validate a binary")
			}
			if pluginPath != "" {
				// Binary/entity plugin validation is a distinct scope: it checks a
				// .wasm against its own describe() self-report (and, with a Plugin
				// entity argument, against the entity's declared surface). It selects
				// neither the whole-catalog nor the release closure.
				if releasePath != "" {
					return fmt.Errorf("OPS_ERROR: --plugin is mutually exclusive with --release; validate a .wasm binary or a Release closure, not both")
				}
				return validatePluginBinary(pluginPath, pluginType, path, cmd.OutOrStdout())
			}
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			if releasePath != "" {
				// Release-closure mode is mutually exclusive with a positional
				// entity target — they select different closures.
				if path != "" {
					return fmt.Errorf("OPS_ERROR: --release is mutually exclusive with an entity argument; pass one or the other")
				}
				return validateRelease(rw, releasePath, catalogDirs, os.Stderr, cmd.OutOrStdout())
			}
			if path == "" {
				// No entity argument → whole-catalog correctness.
				return validateCatalogWideFromWorkspace(rw, catalogDirs)
			}
			return dispatchValidate(rw, path, catalogDirs, cmd.OutOrStdout())
		},
	}
	c.Flags().StringArrayVar(&catalogDirs, "catalog", nil,
		"Catalog directory; repeatable. Each --catalog is prepended to the workspace catalog_dirs search stack (first match wins). Defaults to the current directory.")
	c.Flags().StringVar(&releasePath, "release", "", "Path to a Release YAML to validate over its resolved dependency closure (mutually exclusive with an entity argument)")
	c.Flags().StringVar(&pluginPath, "plugin", "", "Path to a raw .wasm to validate against its describe() self-report — the pre-publish authoring check. Alone it needs --type; with a Plugin entity argument it cross-checks the binary against the entity's declared type, capability claim, command mirror, and host-function record.")
	c.Flags().StringVar(&pluginType, "type", "", "Plugin type for a binary-only --plugin check, verified against the binary's self-reported type. Only meaningful with --plugin; when a Plugin entity supplies the type, omit it (a disagreeing value halts).")
	return c
}

// validatePluginBinary runs the plugin scope: it validates a raw .wasm against
// its own describe() self-report and, when a Plugin entity path is supplied,
// additionally cross-checks the binary against the entity's declared surface.
// The check is read-only and writes nothing to disk.
//
// Two modes select on entityPath:
//
//   - Binary-only (entityPath == ""): declaredType is required and is checked
//     against the binary's self-reported plugin type. No entity surface is read.
//   - Full (entityPath != ""): the file must be a Plugin entity. It is schema-
//     gated, then the binary is cross-checked against the entity's declared
//     capability claim, command mirror, and host-function record through the one
//     publish validation door, so `validate` and publish apply the identical
//     check. The declared type comes from the entity; a --type that disagrees
//     halts, since a Plugin carries a single declared type.
func validatePluginBinary(pluginPath, declaredType, entityPath string, out io.Writer) error {
	wasmBytes, err := os.ReadFile(pluginPath)
	if err != nil {
		return fmt.Errorf("read .wasm %q: %w", pluginPath, err)
	}
	ctx := context.Background()

	if entityPath == "" {
		if declaredType == "" {
			return fmt.Errorf("OPS_ERROR: binary-only plugin validation needs a declared type; pass --type <plugin-type>, or add a Plugin entity argument to take the type from the entity")
		}
		fmt.Fprintf(out, "Plugin: %s\n", pluginPath)
		fmt.Fprintf(out, "Declared type: %s\n", declaredType)
		if _, err := wasm.ValidatePlugin(ctx, wasmBytes, wasm.ValidateOptions{
			Identity:       pluginPath,
			DeclaredType:   declaredType,
			MaxMemoryPages: describeMaxMemoryPages,
		}); err != nil {
			return err
		}
		fmt.Fprintln(out, "Describe: valid")
		fmt.Fprintln(out, "Type: valid")
		return nil
	}

	data, err := os.ReadFile(entityPath)
	if err != nil {
		return fmt.Errorf("read %q: %w", entityPath, err)
	}
	var preamble struct {
		Kind string `yaml:"kind"`
	}
	if err := yaml.Unmarshal(data, &preamble); err != nil {
		return fmt.Errorf("parse YAML preamble in %q: %w", entityPath, err)
	}
	kind, ok := knownEntityKind(preamble.Kind)
	if !ok || kind != envelope.KindPlugin {
		return fmt.Errorf("--plugin cross-checks a Plugin entity, but %q declares kind %q", entityPath, preamble.Kind)
	}

	fmt.Fprintf(out, "Entity: %s\n", entityPath)
	fmt.Fprintf(out, "Kind:   %s\n", kind)

	if _, verr := envelope.ValidateBytes(kind, data, entityPath); verr != nil {
		return fmt.Errorf("schema: %w", verr)
	}
	fmt.Fprintln(out, "Schema: valid")

	var entity types.Plugin
	if err := yaml.Unmarshal(data, &entity); err != nil {
		return fmt.Errorf("parse Plugin at %q: %w", entityPath, err)
	}
	// One declared type: the entity's metadata.pluginType is authoritative, so a
	// --type that disagrees is an authoring contradiction, not a second source.
	if declaredType != "" && declaredType != entity.Metadata.PluginType {
		return fmt.Errorf("OPS_ERROR: --type %q disagrees with the entity's metadata.pluginType %q; a Plugin has one declared type — drop --type or fix the entity", declaredType, entity.Metadata.PluginType)
	}

	// The same entity → EntityInputs mapping the publish path builds, so validate
	// and publish can never apply a divergent cross-check. An entity declaring no
	// runtime block falls back to the describe-time memory headroom exactly as
	// publish does.
	maxPages := describeMaxMemoryPages
	if entity.Spec.Runtime != nil && entity.Spec.Runtime.MaxMemoryPages != 0 {
		maxPages = entity.Spec.Runtime.MaxMemoryPages
	}
	report, err := wasm.ValidatePlugin(ctx, wasmBytes, wasm.ValidateOptions{
		Identity:       entity.Metadata.Name + ":" + entity.Metadata.Version,
		DeclaredType:   entity.Metadata.PluginType,
		MaxMemoryPages: maxPages,
		Entity:         resolver.PluginEntityInputs(entity),
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "Plugin: valid")
	// Advisory only, exactly as publish reports: the binary offers more than the
	// entity curates. Under-declaration is legitimate and never fails the check.
	for _, notice := range report.Notices {
		fmt.Fprintf(out, "  notice: %s\n", notice)
	}
	return nil
}

// knownEntityKind maps a peeked kind string to its envelope Kind, reporting
// whether the kind names a schema-backed entity family. An unrecognized kind
// (or the embedded-only Workload named where it cannot stand alone) is handled
// by the caller with a clear diagnostic.
func knownEntityKind(kind string) (envelope.Kind, bool) {
	switch envelope.Kind(kind) {
	case envelope.KindComponent, envelope.KindFeature, envelope.KindRecipe,
		envelope.KindFeatureRecipe, envelope.KindWorkload, envelope.KindPlugin:
		return envelope.Kind(kind), true
	default:
		return "", false
	}
}

// dispatchValidate reads the entity file, dispatches on its kind, and runs the
// tiered checks: schema envelope + shape (all kinds), pillar structure
// (Component / Feature), and isolated render (Recipe / FeatureRecipe). It
// returns the first blocking error so a schema break halts loudly before any
// catalog work.
func dispatchValidate(rw *workspaceconfig.ResolvedWorkspace, path string, catalogFlagDirs []string, out io.Writer) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %q: %w", path, err)
	}

	var preamble struct {
		Kind string `yaml:"kind"`
	}
	if err := yaml.Unmarshal(data, &preamble); err != nil {
		return fmt.Errorf("parse YAML preamble in %q: %w", path, err)
	}
	if preamble.Kind == "" {
		return fmt.Errorf("couldn't determine entity kind from %q; expected an apiVersion/kind preamble", path)
	}
	kind, ok := knownEntityKind(preamble.Kind)
	if !ok {
		return fmt.Errorf("kind %q in %q is not a single-entity validation target", preamble.Kind, path)
	}

	fmt.Fprintf(out, "Entity: %s\n", path)
	fmt.Fprintf(out, "Kind:   %s\n", kind)

	// Tier 1a: envelope schema gate — the exact per-entity JSON-Schema check the
	// catalog loader runs at each parse boundary. A violation halts loudly here.
	if _, verr := envelope.ValidateBytes(kind, data, path); verr != nil {
		return fmt.Errorf("schema: %w", verr)
	}
	fmt.Fprintln(out, "Schema: valid")

	// Tier 1b: kind-specific shape validation, and the render tiers for the
	// rendering kinds.
	switch kind {
	case envelope.KindComponent:
		return validateComponentEntity(rw, path, data, catalogFlagDirs, out)
	case envelope.KindFeature:
		return validateFeatureEntity(rw, path, data, catalogFlagDirs, out)
	case envelope.KindWorkload:
		return validateWorkloadEntity(path, data, out)
	case envelope.KindRecipe:
		return validateRecipeEntity(rw, path, data, catalogFlagDirs, out)
	case envelope.KindFeatureRecipe:
		return validateFeatureRecipeEntity(rw, path, data, catalogFlagDirs, out)
	case envelope.KindPlugin:
		// A Plugin is schema-only: it declares a Wasm module reference and has no
		// values or pillar surface and no render tier, so the envelope gate above
		// is the complete check.
		return nil
	}
	return nil
}

// loadResolvedCatalog loads the catalog over the first-win search stack and
// resolves extends, returning the catalog. It is the single catalog entry point
// for the validate tiers that need catalog context (pillar libraries for
// Component / Feature, and the implements target plus provider Plugin for the
// render tiers).
//
// The load is tolerant: a catalog file that fails to parse or index is skipped
// rather than aborting the walk, so an unrelated broken entity elsewhere in the
// catalog never blocks validating the target in isolation — the same posture
// the build path takes over a catalog it does not fully own. Extends resolution
// runs best-effort: a broken extends chain on an unrelated entity is surfaced
// as a note (on stderr) and does not fail the load, while the target's own
// merged shape still resolves.
func loadResolvedCatalog(rw *workspaceconfig.ResolvedWorkspace, catalogFlagDirs []string) (*catalog.Catalog, error) {
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return nil, fmt.Errorf("workspaceconfig: %w", err)
	}
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	dirs := resolveCatalogDirs(catalogFlagDirs, wsCfg, ".")
	cat, _, err := catalog.LoadPassOneTolerant(rw.Root(), cacheDir, dirs...)
	if err != nil {
		return nil, fmt.Errorf("catalog load: %w", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		fmt.Fprintf(os.Stderr, "  note: %d catalog extends resolution error(s); first: %s\n", len(extErrs), extErrs[0].Error())
	}
	return cat, nil
}

// validateComponentEntity runs the Component shape and pillar checks. The
// pillar check needs the catalog's SchemaLibraries map for cross-document $ref
// expansion, so the catalog is loaded to supply it.
func validateComponentEntity(rw *workspaceconfig.ResolvedWorkspace, path string, data []byte, catalogFlagDirs []string, out io.Writer) error {
	var comp types.Component
	if err := yaml.Unmarshal(data, &comp); err != nil {
		return fmt.Errorf("parse Component at %q: %w", path, err)
	}
	cat, err := loadResolvedCatalog(rw, catalogFlagDirs)
	if err != nil {
		return err
	}
	if err := validator.ValidateSchemaPillars("Component", comp.Metadata.Name, path, &comp.Spec.Schema, cat.SchemaLibraries); err != nil {
		return fmt.Errorf("pillars: %w", err)
	}
	fmt.Fprintln(out, "Pillars: valid")
	return nil
}

// validateFeatureEntity runs the Feature pillar check, loading the catalog for
// the SchemaLibraries map exactly as the Component path does.
func validateFeatureEntity(rw *workspaceconfig.ResolvedWorkspace, path string, data []byte, catalogFlagDirs []string, out io.Writer) error {
	var feat types.Feature
	if err := yaml.Unmarshal(data, &feat); err != nil {
		return fmt.Errorf("parse Feature at %q: %w", path, err)
	}
	cat, err := loadResolvedCatalog(rw, catalogFlagDirs)
	if err != nil {
		return err
	}
	if err := validator.ValidateSchemaPillars("Feature", feat.Metadata.Name, path, &feat.Spec.Schema, cat.SchemaLibraries); err != nil {
		return fmt.Errorf("pillars: %w", err)
	}
	fmt.Fprintln(out, "Pillars: valid")
	return nil
}

// validateWorkloadEntity runs the standalone Workload shape check. A Workload
// normally lives embedded inside a Recipe's workload: block; validating a lone
// Workload file exercises the same shape rules that path applies.
func validateWorkloadEntity(path string, data []byte, out io.Writer) error {
	var wl types.Workload
	if err := yaml.Unmarshal(data, &wl); err != nil {
		return fmt.Errorf("parse Workload at %q: %w", path, err)
	}
	if err := validator.ValidateWorkload(&wl, path, "", ""); err != nil {
		return fmt.Errorf("shape: %w", err)
	}
	fmt.Fprintln(out, "Shape:  valid")
	return nil
}

// validateRecipeEntity runs the Recipe shape check then the render tiers: it
// resolves the Recipe's implements Component from the catalog, synthesizes a
// minimal deployment around a single instance of that Component bound to the
// Recipe under test, and drives the real pipeline — first resolution and guards
// (render skipped), then a real isolated render.
func validateRecipeEntity(rw *workspaceconfig.ResolvedWorkspace, path string, data []byte, catalogFlagDirs []string, out io.Writer) error {
	var rec types.Recipe
	if err := yaml.Unmarshal(data, &rec); err != nil {
		return fmt.Errorf("parse Recipe at %q: %w", path, err)
	}
	if err := validator.ValidateRecipe(&rec, path); err != nil {
		return fmt.Errorf("shape: %w", err)
	}
	fmt.Fprintln(out, "Shape:  valid")

	cat, err := loadResolvedCatalog(rw, catalogFlagDirs)
	if err != nil {
		return err
	}
	// The recipe render tier resolves providers the way a real build does: core
	// providers resolve from the binary's embedded entity set (embedded-wins), so
	// validating a core-provider recipe in a workspace with no local catalog
	// resolves the provider offline exactly as `aggrega build` would. This is
	// scoped to the render-tier catalog; the shape and pillar validate paths that
	// share loadResolvedCatalog read only SchemaLibraries and are unaffected.
	cat.UseEmbeddedProviderResolution()
	lookup := cat.CatalogLookup()
	if lookup.Component == nil {
		return fmt.Errorf("render: catalog has no Component index")
	}
	comp, found := lookup.Component(rec.Spec.Implements)
	if !found {
		return fmt.Errorf("render: implements target %q not found in catalog", rec.Spec.Implements)
	}

	recipeURI := identifierVersion(rec.Metadata.Name, rec.Metadata.Version)
	harness, err := buildEntityHarness(rec.Spec.Implements, recipeURI, &comp, lookup)
	if err != nil {
		return err
	}
	recipeLookup := renderer.RecipeLookup{
		// The synthesized deployment carries a single component instance, so the
		// Recipe under test is the recipe for every node — return it directly
		// rather than resolving through the catalog, which lets the loose
		// (possibly not-yet-published or edited-in-place) file drive the render.
		Recipe: func(string, string) (*types.Recipe, bool) { return &rec, true },
	}
	return runRenderTiers(harness, lookup, recipeLookup, cat.SubAggregateLoader(), out)
}

// validateFeatureRecipeEntity runs the FeatureRecipe shape check. The render
// tiers for a FeatureRecipe need a host component the attached Feature applies
// to; synthesizing a guard-passing host for an arbitrary Feature is not
// attempted here, so the FeatureRecipe path stops after shape validation.
func validateFeatureRecipeEntity(rw *workspaceconfig.ResolvedWorkspace, path string, data []byte, catalogFlagDirs []string, out io.Writer) error {
	var fr types.FeatureRecipe
	if err := yaml.Unmarshal(data, &fr); err != nil {
		return fmt.Errorf("parse FeatureRecipe at %q: %w", path, err)
	}
	if err := validator.ValidateFeatureRecipe(&fr, path); err != nil {
		return fmt.Errorf("shape: %w", err)
	}
	fmt.Fprintln(out, "Shape:  valid")
	fmt.Fprintln(out, "Render: skipped (a FeatureRecipe renders only when attached to a host component)")
	return nil
}

// entityHarness is the synthesized minimal deployment the render tiers drive:
// an in-memory Aggregate (raw bytes), Manifest, and Release around a single
// component instance of the implements type.
type entityHarness struct {
	aggregateBytes []byte
	manifest       *types.Manifest
	release        *types.Release
	aggregatePath  string
}

// syntheticInstanceName is the single component-instance name the synthesized
// deployment uses. It is a fixed, target-agnostic label so the harness — and
// thus the rendered output an author inspects — is deterministic.
const syntheticInstanceName = "instance"

// buildEntityHarness synthesizes the minimal Aggregate + Manifest + Release the
// render tiers drive. The Aggregate carries one instance of the implements
// Component with each required value filled by a typed placeholder; schema
// defaults for the remaining values and for infra materialize through the real
// resolver rather than being hand-written here. The Release routes the instance
// into a synthesized default boundary; it declares no values: block and no
// compute the Aggregate did not declare.
func buildEntityHarness(implements, recipeURI string, comp *types.Component, lookup resolver.CatalogLookup) (entityHarness, error) {
	values, err := requiredValuePlaceholders(comp, lookup.SchemaLibraries)
	if err != nil {
		return entityHarness{}, fmt.Errorf("render: synthesize values: %w", err)
	}

	instance := map[string]any{"type": implements}
	if len(values) > 0 {
		instance["values"] = values
	}
	aggregate := map[string]any{
		"apiVersion": "aggrega.ai/v1alpha1",
		"kind":       "Aggregate",
		"metadata":   map[string]any{"name": "validate-probe", "version": "1.0.0"},
		"spec": map[string]any{
			"components": map[string]any{syntheticInstanceName: instance},
		},
	}
	aggBytes, err := yaml.Marshal(aggregate)
	if err != nil {
		return entityHarness{}, fmt.Errorf("render: marshal synthesized aggregate: %w", err)
	}

	const aggregatePath = "validate://aggregate.yaml"
	manifestBytes, err := yaml.Marshal(map[string]any{
		"apiVersion": "aggrega.ai/v1alpha1",
		"kind":       "Manifest",
		"metadata":   map[string]any{"name": "validate-probe", "version": "1.0.0"},
		"spec": map[string]any{
			"mappings": map[string]any{
				"components": map[string]any{
					"type": map[string]any{implements: recipeURI},
				},
			},
		},
	})
	if err != nil {
		return entityHarness{}, fmt.Errorf("render: marshal synthesized manifest: %w", err)
	}
	manifest, err := resolver.LoadManifestFromBytes(manifestBytes, "validate://manifest.yaml")
	if err != nil {
		return entityHarness{}, fmt.Errorf("render: synthesized manifest: %w", err)
	}

	releaseBytes, err := yaml.Marshal(map[string]any{
		"apiVersion": "aggrega.ai/v1alpha1",
		"kind":       "Release",
		"metadata":   map[string]any{"name": "validate-probe", "environment": "validate", "version": "1.0.0"},
		"spec": map[string]any{
			"aggregate":  aggregatePath,
			"manifest":   "validate://manifest.yaml",
			"components": map[string]any{syntheticInstanceName: map[string]any{}},
		},
	})
	if err != nil {
		return entityHarness{}, fmt.Errorf("render: marshal synthesized release: %w", err)
	}
	release, err := resolver.LoadReleaseFromBytes(releaseBytes, "validate://release.yaml")
	if err != nil {
		return entityHarness{}, fmt.Errorf("render: synthesized release: %w", err)
	}

	return entityHarness{
		aggregateBytes: aggBytes,
		manifest:       manifest,
		release:        release,
		aggregatePath:  aggregatePath,
	}, nil
}

// runRenderTiers drives the synthesized harness through the real pipeline: Tier
// 2 runs resolution, guards, and IR construction with render skipped; Tier 3
// runs the real render and reports the emitted artifacts. Pipeline diagnostics
// (guard violations, render warnings) surface on stderr through the pipeline
// itself; stdout carries only this command's deterministic report.
func runRenderTiers(h entityHarness, lookup resolver.CatalogLookup, recipeLookup renderer.RecipeLookup, subAggregateLoader topology.SubAggregateLoader, out io.Writer) error {
	inputs := pipeline.Inputs{
		Release:           h.release,
		Manifest:          h.manifest,
		AggregateRawBytes: h.aggregateBytes,
		AggregatePath:     h.aggregatePath,
		WorkspaceConfig:   workspaceconfig.Default(),
	}
	ctx := context.Background()

	// Tier 2: resolution, guards, IR construction — no render. A missing
	// required input, a guard halt, or a resolution error surfaces here.
	if _, err := pipeline.RunPipeline(ctx, inputs, lookup, recipeLookup, subAggregateLoader, pipeline.Options{SkipRender: true}); err != nil {
		return fmt.Errorf("resolution: %w", err)
	}
	fmt.Fprintln(out, "Resolution: valid")

	// Tier 3: real render. Provider resolution is embedded-wins (the render-tier
	// catalog enables it upstream), so a core-provider recipe resolves its
	// provider from the binary's embedded set and renders even in a workspace
	// with no local catalog — matching how a real build resolves the provider.
	result, err := pipeline.RunPipeline(ctx, inputs, lookup, recipeLookup, subAggregateLoader, pipeline.Options{})
	if err != nil {
		// The isolated render did not complete. This is a genuine finding — the
		// entity does not render standalone — but the cause may be a harness
		// limitation rather than an entity defect: a single-entity harness fills
		// only required values (with schema defaults flowing), and supplies no
		// attached credential or topology edge. An entity whose template reads a
		// value delivered only by such an input cannot render in isolation. The
		// underlying render error surfaces on stderr through the pipeline.
		fmt.Fprintln(out, "Render: not completed in isolation")
		fmt.Fprintln(out, "  The render requires deployment inputs a single-entity harness does not")
		fmt.Fprintln(out, "  synthesize (for example an attached credential or a topology edge). See the")
		fmt.Fprintln(out, "  render diagnostic below.")
		return fmt.Errorf("render: %w", err)
	}
	keys := artifactKeysSorted(result.Artifacts)
	fmt.Fprintf(out, "Render: %d artifact(s)\n", len(keys))
	for _, k := range keys {
		fmt.Fprintf(out, "\n--- %s ---\n", k)
		content := result.Artifacts[k]
		out.Write(content)
		if len(content) > 0 && content[len(content)-1] != '\n' {
			fmt.Fprintln(out)
		}
	}
	return nil
}

// requiredValuePlaceholders compiles the Component's schema and returns a typed
// placeholder for each required values field that carries no default. Fields
// with a default are omitted so the real resolver materializes the default; the
// placeholder types mirror the declared JSON-Schema type (string, integer,
// number, boolean, object, array).
func requiredValuePlaceholders(comp *types.Component, libraries map[string]types.SchemaLibrary) (map[string]any, error) {
	if comp.Spec.Schema.Kind == 0 {
		return nil, nil
	}
	opts := []compactform.CompileOption{}
	if libraries != nil {
		opts = append(opts, compactform.WithSchemaLibraries(libraries))
	}
	compiled, err := compactform.Compile(&comp.Spec.Schema, opts...)
	if err != nil {
		return nil, err
	}
	valuesPillar, ok := mapAt(compiled, "properties", "values")
	if !ok {
		return nil, nil
	}
	required := toStringSlice(valuesPillar["required"])
	props, _ := valuesPillar["properties"].(map[string]any)
	if len(required) == 0 || props == nil {
		return nil, nil
	}
	out := map[string]any{}
	for _, name := range required {
		if name == "" {
			continue
		}
		field, _ := props[name].(map[string]any)
		if field == nil {
			out[name] = "example"
			continue
		}
		if _, hasDefault := field["default"]; hasDefault {
			continue
		}
		out[name] = placeholderForField(field)
	}
	return out, nil
}

// placeholderForField returns a typed placeholder for one compiled schema field
// keyed by its declared type. An integer or number uses the declared minimum
// when present so a min-constrained field passes; other types use a neutral
// zero-shaped value.
func placeholderForField(field map[string]any) any {
	typ, _ := field["type"].(string)
	switch typ {
	case "integer", "number":
		if m, ok := numericMinimum(field); ok {
			return m
		}
		return 0
	case "boolean":
		return false
	case "object":
		return map[string]any{}
	case "array":
		return []any{}
	default:
		return "example"
	}
}

// numericMinimum extracts a field's `minimum` constraint as a plain number when
// present, tolerating the int and float shapes a compiled schema may carry.
func numericMinimum(field map[string]any) (any, bool) {
	switch m := field["minimum"].(type) {
	case int:
		return m, true
	case int64:
		return m, true
	case float64:
		return m, true
	default:
		return nil, false
	}
}

// toStringSlice normalizes a compiled schema's `required` value into a string
// slice, tolerating both the []string and []any shapes a compiled schema may
// carry for the keyword.
func toStringSlice(v any) []string {
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		out := make([]string, 0, len(s))
		for _, e := range s {
			if str, ok := e.(string); ok {
				out = append(out, str)
			}
		}
		return out
	default:
		return nil
	}
}

// mapAt walks a nested string-keyed map along keys and returns the map at the
// final key, reporting whether every step resolved to a map.
func mapAt(m map[string]any, keys ...string) (map[string]any, bool) {
	cur := m
	for _, k := range keys {
		next, ok := cur[k].(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

// artifactKeysSorted returns the artifact paths in lexical order for a
// deterministic report regardless of the render's internal map ordering.
func artifactKeysSorted(files map[string][]byte) []string {
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
