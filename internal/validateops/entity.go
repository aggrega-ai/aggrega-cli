// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validateops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/internal/sourceref"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"

	"go.yaml.in/yaml/v3"
)

// RenderedArtifact is one emitted artifact from the single-entity render tier:
// its path key and its rendered bytes.
type RenderedArtifact struct {
	Key     string
	Content []byte
}

// EntityResult is the structured outcome of single-entity validation.
//
// Diagnostics is the ordered set of report lines — the identity header, the
// per-tier status lines (schema, pillars/shape, resolution, render), the
// declined-check line naming any required uses sockets a single-entity run
// could not cover, the not-attempted explanation when the Recipe's source is one
// a single-entity run cannot fetch, and the render-failure explanation when a
// render that WAS attempted did not complete — each verbatim and newline-free
// (a renderer emits one per line). Artifacts carries the rendered output of a
// render-bearing Recipe, ordered by key, so a caller can print the bytes or
// summarize a count. Notes
// carries advisory lines a caller routes to a diagnostic stream rather than the
// primary report. Kind is the resolved entity kind, empty when the input never
// resolved to a known kind (in which case the returned error explains why and
// Diagnostics is empty).
type EntityResult struct {
	Path        string
	Kind        string
	Valid       bool
	Diagnostics []string
	Artifacts   []RenderedArtifact
	Notes       []string
}

// ValidateEntity reads the entity file, dispatches on its kind, and runs the
// tiered checks: schema envelope + shape (all kinds), pillar structure
// (Component / Feature), and isolated render (Recipe). It returns the first
// blocking error so a schema break halts before any catalog work, alongside the
// partial result accumulated up to that point so a caller can still render the
// progress that preceded the failure.
//
// A value the kind requires that the document leaves empty is reported ahead
// of every other tier, and every such value is reported at once: each is named
// as a placeholder still owed, with what it takes read from the kind's own
// schema. See placeholderRefusal for why that reading sits beside the envelope
// gate rather than inside it.
//
// A kind this scope does not accept is refused naming the kinds it does
// accept, and the scope that checks the refused kind instead.
//
// The catalog directories are the already-resolved first-win search stack; the
// workspace root and cache directory are resolved likewise by the caller.
//
// ctx is the caller's own. The isolated-render tier drives the real compilation
// pipeline twice, loading and running provider guests, so ending the caller's
// run ends that work: a guest call whose context ends unwinds rather than being
// noticed only once the guest next returns.
func ValidateEntity(ctx context.Context, wsRoot, cacheDir string, dirs []string, path string, suppressUnsupportedFieldWarnings bool) (EntityResult, error) {
	var res EntityResult
	res.Path = path

	data, err := os.ReadFile(path)
	if err != nil {
		return res, fmt.Errorf("read %q: %w", path, err)
	}

	var preamble struct {
		Kind string `yaml:"kind"`
	}
	if err := yaml.Unmarshal(data, &preamble); err != nil {
		return res, fmt.Errorf("parse YAML preamble in %q: %w", path, err)
	}
	if preamble.Kind == "" {
		return res, fmt.Errorf("couldn't determine entity kind from %q; expected an apiVersion/kind preamble", path)
	}
	kind, ok := knownEntityKind(preamble.Kind)
	if !ok {
		return res, unacceptedKindError(preamble.Kind, path)
	}

	res.Kind = string(kind)
	res.Diagnostics = append(res.Diagnostics, "Entity: "+path, "Kind:   "+string(kind))

	// Tier 1a: envelope schema gate — the exact per-entity JSON-Schema check the
	// catalog loader runs at each parse boundary. A violation halts loudly here,
	// after the placeholder reading below has had its say about the same bytes.
	_, verr := envelope.ValidateBytes(kind, data, path)
	if perr := placeholderRefusal(kind, data, path, verr); perr != nil {
		return res, perr
	}
	if verr != nil {
		return res, fmt.Errorf("schema: %w", verr)
	}
	res.Diagnostics = append(res.Diagnostics, "Schema: valid")

	// Tier 1b: kind-specific shape validation, and the render tiers for the
	// rendering kinds.
	switch kind {
	case envelope.KindComponent:
		return res, validateComponentEntity(&res, wsRoot, cacheDir, dirs, path, data)
	case envelope.KindFeature:
		return res, validateFeatureEntity(&res, wsRoot, cacheDir, dirs, path, data)
	case envelope.KindWorkload:
		return res, validateWorkloadEntity(&res, path, data)
	case envelope.KindRecipe:
		return res, validateRecipeEntity(ctx, &res, wsRoot, cacheDir, dirs, path, data, suppressUnsupportedFieldWarnings)
	case envelope.KindFeatureRecipe:
		return res, validateFeatureRecipeEntity(&res, path, data)
	case envelope.KindPlugin:
		return res, validatePluginEntity(&res, path, data)
	}
	res.Valid = true
	return res, nil
}

// singleEntityKinds is the set of kinds the single-entity scope validates, in
// the order a refusal names them. It is the one statement of that set: the
// membership check and the refusal naming the accepted kinds both read it, so
// the two cannot disagree about which kinds this scope takes.
var singleEntityKinds = []envelope.Kind{
	envelope.KindComponent,
	envelope.KindFeature,
	envelope.KindRecipe,
	envelope.KindFeatureRecipe,
	envelope.KindWorkload,
	envelope.KindPlugin,
}

// SingleEntityKinds returns the kinds the single-entity scope validates, in the
// order a refusal names them. The returned slice is freshly allocated, so a
// caller may not change the set through it.
func SingleEntityKinds() []string {
	out := make([]string, 0, len(singleEntityKinds))
	for _, k := range singleEntityKinds {
		out = append(out, string(k))
	}
	return out
}

// knownEntityKind maps a peeked kind string to its envelope Kind, reporting
// whether the kind is one this scope validates. An unrecognized kind is handled
// by the caller with a clear diagnostic.
func knownEntityKind(kind string) (envelope.Kind, bool) {
	for _, k := range singleEntityKinds {
		if string(k) == kind {
			return k, true
		}
	}
	return "", false
}

// unacceptedKindError refuses a document whose kind this scope does not
// validate. It names the kinds the scope does accept, read from the one list
// knownEntityKind reads, and — for an entity kind — the scope that checks a
// document of that kind instead, so the reader is told where to go rather than
// only where not to.
func unacceptedKindError(kind, path string) error {
	msg := fmt.Sprintf("kind %q in %q is not a single-entity validation target; the kinds this scope accepts are %s",
		kind, path, joinKinds(SingleEntityKinds()))
	if route := unacceptedKindRoute(entitykind.Kind(kind)); route != "" {
		msg += ". " + route
	}
	return errors.New(msg)
}

// unacceptedKindRoute names the validation scope that checks a document of an
// entity kind the single-entity scope does not accept, or returns the empty
// string for a token that is not an entity kind at all.
//
// Each route is what that scope was measured to read. A whole-catalog run reads
// the documents inside the configured catalog directories, and reads an
// Aggregate and a library there but not a Manifest or a Release; the Release
// scope reads the Release it is given, the Manifest that Release names and the
// Aggregate it deploys.
//
// It is per-kind code rather than a field on the entity-kind vocabulary because
// it states what this package's scopes do. A test binds it to the vocabulary, so
// a kind added there is accepted or routed here before the suite passes.
func unacceptedKindRoute(kind entitykind.Kind) string {
	switch kind {
	case entitykind.Aggregate:
		return "An Aggregate is checked by the whole-catalog scope when it sits in a catalog directory, " +
			"and by the Release scope over a Release that deploys it"
	case entitykind.Manifest:
		return "A Manifest is checked by the Release scope over a Release that names it"
	case entitykind.Release:
		return "A Release is checked by the Release scope: validate it as the Release rather than as an entity"
	case entitykind.SchemaLibrary:
		return "A SchemaLibrary is checked by the whole-catalog scope when it sits in a catalog directory"
	case entitykind.MappingLibrary:
		return "A MappingLibrary is checked by the whole-catalog scope when it sits in a catalog directory"
	case entitykind.WorkloadLibrary:
		return "A WorkloadLibrary is checked by the whole-catalog scope when it sits in a catalog directory"
	case entitykind.ArtifactLibrary:
		return "An ArtifactLibrary is checked by the whole-catalog scope when it sits in a catalog directory"
	default:
		return ""
	}
}

// joinKinds renders a list of kind names as prose: comma-separated, with the
// last joined by "and".
func joinKinds(kinds []string) string {
	switch len(kinds) {
	case 0:
		return ""
	case 1:
		return kinds[0]
	default:
		return strings.Join(kinds[:len(kinds)-1], ", ") + " and " + kinds[len(kinds)-1]
	}
}

// loadResolvedCatalog loads the catalog over the first-win search stack and
// resolves extends, returning the catalog and an advisory note (empty when
// extends resolved cleanly). It is the single catalog entry point for the
// validate tiers that need catalog context (pillar libraries for
// Component / Feature, and the implements target plus provider Plugin for the
// render tiers).
//
// The load is tolerant: a catalog file that fails to parse or index is skipped
// rather than aborting the walk, so an unrelated broken entity elsewhere in the
// catalog never blocks validating the target in isolation — the same posture
// the build path takes over a catalog it does not fully own. Extends resolution
// runs best-effort: a broken extends chain on an unrelated entity is surfaced
// as the returned note and does not fail the load, while the target's own
// merged shape still resolves.
func loadResolvedCatalog(wsRoot, cacheDir string, dirs []string) (*catalog.Catalog, string, error) {
	cat, _, err := catalog.LoadPassOneTolerant(wsRoot, cacheDir, dirs...)
	if err != nil {
		return nil, "", fmt.Errorf("catalog load: %w", err)
	}
	var note string
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		note = fmt.Sprintf("  note: %d catalog extends resolution error(s); first: %s", len(extErrs), extErrs[0].Error())
	}
	return cat, note, nil
}

// validateComponentEntity runs the Component shape and pillar checks. The
// pillar check needs the catalog's SchemaLibraries map for cross-document $ref
// expansion, so the catalog is loaded to supply it.
//
// The entity is read through the shared per-entity resolve rather than
// unmarshalled here. That is what makes this surface agree with a build about
// what the file declares: the resolve is where an authored `$import:` on the
// schema surface is substituted, so a parse of its own would judge the entity the
// author asked about against a contract it had not read — the one entity where
// that matters most, since it is the one they named.
func validateComponentEntity(res *EntityResult, wsRoot, cacheDir string, dirs []string, path string, data []byte) error {
	comp, err := catalog.ParseComponentEntity(data, path)
	if err != nil {
		return fmt.Errorf("parse Component at %q: %w", path, err)
	}
	cat, note, err := loadResolvedCatalog(wsRoot, cacheDir, dirs)
	if note != "" {
		res.Notes = append(res.Notes, note)
	}
	if err != nil {
		return err
	}
	if err := validator.ValidateSchemaPillars("Component", comp.Metadata.Name, path, comp.Spec.Extends, &comp.Spec.Schema, cat.SchemaLibraries); err != nil {
		return fmt.Errorf("pillars: %w", err)
	}
	if errs := catalog.UnusablePillarContractErrors(
		"Component", comp.Metadata.Name, path, comp.Spec.Extends, &comp.Spec.Schema, comp.Spec.UsesSchema, cat.SchemaLibraries,
	); len(errs) > 0 {
		return fmt.Errorf("pillars: %w", errors.Join(errs...))
	}
	res.Diagnostics = append(res.Diagnostics, "Pillars: valid")
	res.Valid = true
	return nil
}

// validateFeatureEntity runs the Feature pillar check, loading the catalog for
// the SchemaLibraries map exactly as the Component path does, and reading the
// entity through the same shared per-entity resolve for the same reason.
func validateFeatureEntity(res *EntityResult, wsRoot, cacheDir string, dirs []string, path string, data []byte) error {
	feat, err := catalog.ParseFeatureEntity(data, path)
	if err != nil {
		return fmt.Errorf("parse Feature at %q: %w", path, err)
	}
	cat, note, err := loadResolvedCatalog(wsRoot, cacheDir, dirs)
	if note != "" {
		res.Notes = append(res.Notes, note)
	}
	if err != nil {
		return err
	}
	if err := validator.ValidateSchemaPillars("Feature", feat.Metadata.Name, path, feat.Spec.Extends, &feat.Spec.Schema, cat.SchemaLibraries); err != nil {
		return fmt.Errorf("pillars: %w", err)
	}
	if errs := catalog.UnusablePillarContractErrors(
		"Feature", feat.Metadata.Name, path, feat.Spec.Extends, &feat.Spec.Schema, feat.Spec.UsesSchema, cat.SchemaLibraries,
	); len(errs) > 0 {
		return fmt.Errorf("pillars: %w", errors.Join(errs...))
	}
	res.Diagnostics = append(res.Diagnostics, "Pillars: valid")
	res.Valid = true
	return nil
}

// validateWorkloadEntity runs the standalone Workload shape check. A Workload
// normally lives embedded inside a Recipe's workload: block; validating a lone
// Workload file exercises the same shape rules that path applies.
//
// It checks the typed value only, and that is deliberate rather than an
// omission. ValidateEntity validates these same bytes against the Workload
// schema before dispatching here, so a key the document carries that the schema
// does not declare has already halted the run and never reaches this function.
// A byte-level check added here would hand the same bytes to the same schema a
// second time and could not fail — it would read as a gate while being
// incapable of firing, and it would make the Workload path the one kind that
// validates its document twice. What the typed check contributes instead is the
// complement documented on validator.ValidateWorkload: constraints that hold
// over the decoded value, which is a different instance from the document a
// field's elided zero value no longer appears in.
func validateWorkloadEntity(res *EntityResult, path string, data []byte) error {
	var wl types.Workload
	if err := yaml.Unmarshal(data, &wl); err != nil {
		return fmt.Errorf("parse Workload at %q: %w", path, err)
	}
	if err := validator.ValidateWorkload(&wl, path, "", ""); err != nil {
		return fmt.Errorf("shape: %w", err)
	}
	res.Diagnostics = append(res.Diagnostics, "Shape:  valid")
	res.Valid = true
	return nil
}

// validatePluginEntity runs the Plugin shape check: the well-known-annotation
// scope contradiction, which is the one rule a Plugin carries beyond its
// envelope schema. A Plugin has no values or pillar surface and no render tier,
// so there is nothing else to drive here.
//
// It runs the same check the catalog loader runs at its parse boundary, and
// that is the point of it being here at all: without it this command would call
// an entity valid that a catalog walk over the same file refuses, and an author
// checking one file before committing it is exactly who needs the two verdicts
// to agree.
func validatePluginEntity(res *EntityResult, path string, data []byte) error {
	var p types.Plugin
	if err := yaml.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("parse Plugin at %q: %w", path, err)
	}
	if err := types.ValidateWellKnownAnnotationScopes(p); err != nil {
		return fmt.Errorf("shape: %w", err)
	}
	res.Diagnostics = append(res.Diagnostics, "Shape:  valid")
	res.Valid = true
	return nil
}

// validateRecipeEntity runs the Recipe shape check then the render tiers: it
// resolves the Recipe's implements Component from the catalog, synthesizes a
// minimal build around a single instance of that Component bound to the
// Recipe under test, and drives the real pipeline — first resolution and guards
// (render skipped), then a real isolated render.
//
// Two checks of a real build are out of reach here, and each is declined rather
// than failed.
//
// The first is whether the Component's `required: true` uses sockets are
// populated. The synthesized build holds a single instance, so it contains
// no second component an edge could target, and no authoring of the synthesized
// documents can change that.
//
// The second is the render itself, when the Recipe's source is one that has to
// be fetched. A single-entity run reads no lockfile and no cache, so it holds
// neither a record of which bytes that source resolves to nor a store to resolve
// them from, and the infrastructure code the Recipe names exists nowhere it can
// reach. That is asked of sourceref.NeedsFetch, which is the one home for the
// question, so this surface and the resolution it would otherwise drive draw the
// line in the same place. A source the workspace already holds is NOT declined:
// it needs no fetch, so a render over it is attempted and a failure in it is a
// real finding.
//
// Both declines are named in Diagnostics, so the report states which surface it
// did not cover instead of leaving a silent difference between what validate
// checks and what a build checks.
//
// The Recipe is read through the shared per-entity resolve rather than decoded
// here, for the reason the Component path gives: that resolve is where the local
// source and every file an import names are anchored to the directory holding the
// Recipe, and refused where a path leaves it. Decoded here, the render would read
// those paths against the directory the command runs in, so the same Recipe would
// validate from one directory and fail from another, and a path escaping the
// Recipe's directory would meet no refusal at all. The render is handed that same
// document, so a source written as a template is anchored against it once it has
// a value.
func validateRecipeEntity(ctx context.Context, res *EntityResult, wsRoot, cacheDir string, dirs []string, path string, data []byte, suppressUnsupportedFieldWarnings bool) error {
	rec, err := catalog.ParseRecipeEntity(data, path)
	if err != nil {
		return fmt.Errorf("shape: %w", err)
	}
	res.Diagnostics = append(res.Diagnostics, "Shape:  valid")

	cat, note, err := loadResolvedCatalog(wsRoot, cacheDir, dirs)
	if note != "" {
		res.Notes = append(res.Notes, note)
	}
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

	recipeURI := catalog.RecipeMappingURI(rec.Metadata.Name, rec.Metadata.Version)
	harness, err := buildEntityHarness(rec.Spec.Implements, recipeURI, &comp, lookup)
	if err != nil {
		return err
	}

	// Decline the required-socket check for this run and say so, rather than
	// report the author's own valid Recipe as broken. The narrowing is applied
	// to the catalog view THIS harness hands the pipeline and to nothing else,
	// so the rule itself is untouched: a build asks it about the Component
	// exactly as authored.
	tierLookup := lookup
	if declined := declaredRequiredSockets(&comp); len(declined) > 0 {
		tierLookup = lookupWithSocketsOptional(lookup, rec.Spec.Implements, declined)
		res.Diagnostics = append(res.Diagnostics,
			fmt.Sprintf("Sockets: required uses sockets not checked (%s)", strings.Join(declined, ", ")),
			"  Validating a Recipe on its own places its Component in a build of one,",
			"  so no other component exists for a required socket to be wired to. Whether",
			"  each of these sockets is populated is checked when the Component is deployed",
			"  in an Aggregate.")
	}

	recipeLookup := renderer.RecipeLookup{
		// The synthesized build carries a single component instance, so the
		// Recipe under test is the recipe for every node — return it directly
		// rather than resolving through the catalog, which lets the loose
		// (possibly not-yet-published or edited-in-place) file drive the render.
		Recipe:         func(string, string) (*types.Recipe, bool) { return &rec, true },
		RecipeDocument: func(*types.Recipe) (string, bool) { return path, true },
	}
	return runRenderTiers(ctx, res, harness, tierLookup, recipeLookup, cat.SubAggregateLoader(),
		suppressUnsupportedFieldWarnings, sourceref.NeedsFetch(rec.Spec.Source))
}

// declaredRequiredSockets returns the names of every uses socket the Component
// declares `required: true`, in the entity's author order.
//
// The synthesized build carries a single component instance: it authors no
// `uses` entry and its Release wires no edge, because there is no second node
// for an edge to target. Every required socket is therefore one this harness
// cannot populate, which is why the declared set and the out-of-reach set
// coincide here and no population state is consulted.
//
// This reads a declared flag to build the report and the harness view; it
// decides nothing. Whether an unpopulated required socket is an error stays the
// resolver's rule, asked in one place, and a build asks it about the Component
// as authored.
func declaredRequiredSockets(comp *types.Component) []string {
	var out []string
	for name, entry := range comp.Spec.UsesSchema.All() {
		if entry.Required {
			out = append(out, name)
		}
	}
	return out
}

// lookupWithSocketsOptional returns a copy of lookup whose Component accessor
// reports typeURI's named uses sockets as optional. Every other type, and every
// other accessor, passes through untouched.
//
// The copy is what the single-entity render tiers read, so the narrowing reaches
// only this in-memory validation run — the catalog itself is never modified, and
// no other command shares the view. The socket entries survive intact apart from
// the flag, so the checks that read a socket's name or its per-edge schema still
// see exactly what the author declared, and the entries are rebuilt in author
// order so the run stays byte-identical across invocations.
func lookupWithSocketsOptional(lookup resolver.CatalogLookup, typeURI string, sockets []string) resolver.CatalogLookup {
	if lookup.Component == nil || len(sockets) == 0 {
		return lookup
	}
	optional := make(map[string]bool, len(sockets))
	for _, s := range sockets {
		optional[s] = true
	}

	inner := lookup.Component
	narrowed := lookup
	narrowed.Component = func(t string) (types.Component, bool) {
		comp, found := inner(t)
		if !found || t != typeURI {
			return comp, found
		}
		// A fresh map, not a struct copy: OrderedMap holds its entries behind a
		// shared backing map, so writing through a copy would reach the catalog's
		// own component.
		relaxed := types.OrderedMap[types.UsesSchemaEntry]{}
		for name, entry := range comp.Spec.UsesSchema.All() {
			if optional[name] {
				entry.Required = false
			}
			relaxed.Set(name, entry)
		}
		comp.Spec.UsesSchema = relaxed
		return comp, true
	}
	return narrowed
}

// validateFeatureRecipeEntity runs the FeatureRecipe shape check. The render
// tiers for a FeatureRecipe need a host component the attached Feature applies
// to; synthesizing a guard-passing host for an arbitrary Feature is not
// attempted here, so the FeatureRecipe path stops after shape validation.
//
// It reads the entity through the shared per-entity resolve, as the Recipe path
// does, so a path the FeatureRecipe names outside its own directory is refused
// here exactly as a catalog read refuses it.
func validateFeatureRecipeEntity(res *EntityResult, path string, data []byte) error {
	if _, err := catalog.ParseFeatureRecipeEntity(data, path); err != nil {
		return fmt.Errorf("shape: %w", err)
	}
	res.Diagnostics = append(res.Diagnostics,
		"Shape:  valid",
		"Render: skipped (a FeatureRecipe renders only when attached to a host component)")
	res.Valid = true
	return nil
}

// entityHarness is the synthesized minimal build the render tiers drive:
// an in-memory Aggregate (raw bytes), Manifest, and Release around a single
// component instance of the implements type.
type entityHarness struct {
	aggregateBytes []byte
	manifest       *types.Manifest
	release        *types.Release
	aggregatePath  string
}

// syntheticInstanceName is the single component-instance name the synthesized
// build uses. It is a fixed, target-agnostic label so the harness — and
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
// runs the real render and records the emitted artifacts. Pipeline diagnostics
// (guard violations, render warnings) surface on the process diagnostic stream
// through the pipeline itself; the accumulated result carries only this scope's
// deterministic report.
//
// sourceNeedsFetch declines Tier 3 and reports it as not attempted. Tier 2 still
// runs and still fails the run on a resolution error, so declining the render
// narrows what is covered without softening anything that is.
func runRenderTiers(ctx context.Context, res *EntityResult, h entityHarness, lookup resolver.CatalogLookup, recipeLookup renderer.RecipeLookup, subAggregateLoader topology.SubAggregateLoader, suppressUnsupportedFieldWarnings, sourceNeedsFetch bool) error {
	// A single-entity harness runs against no workspace at all — it synthesizes
	// its Release in memory — so the defaults are the configuration in force, and
	// they are supplied explicitly rather than left unset: an unsupplied
	// configuration is refused at the graph entry, and the two facts are not the
	// same. Nothing is attached under these defaults, so the transform roles are
	// a no-op here for the right reason.
	wsCfg := workspaceconfig.Default()
	inputs := pipeline.Inputs{
		Release:           h.release,
		Manifest:          h.manifest,
		AggregateRawBytes: h.aggregateBytes,
		AggregatePath:     h.aggregatePath,
		WorkspaceConfig:   &wsCfg,
		// No workspace means no recorded closure, so nothing was ever going to
		// pin what this harness resolves. Written rather than omitted: the
		// pin-free source is what this probe MEANS, and an omission would be a
		// different fact wearing the same face.
		PluginPins: pluginpath.NoPins(),
	}

	// Tier 2: resolution, guards, IR construction — no render. A missing
	// required input, a guard halt, or a resolution error surfaces here.
	if _, err := pipeline.RunPipeline(ctx, inputs, lookup, recipeLookup, subAggregateLoader, pipeline.Options{SkipRender: true}); err != nil {
		return fmt.Errorf("resolution: %w", err)
	}
	res.Diagnostics = append(res.Diagnostics, "Resolution: valid")

	// Decline the render for this run and say so, rather than report the
	// author's own valid Recipe as broken. Everything above was checked; what is
	// left unchecked is named, so the report is narrower than a build's rather
	// than quieter about the same ground.
	if sourceNeedsFetch {
		res.Diagnostics = append(res.Diagnostics,
			"Render: not attempted (this Recipe's source is fetched)",
			"  Validating one entity on its own reads no lockfile, reads and writes no",
			"  cache, and makes no network request, so the chart or module this Recipe",
			"  names is never retrieved and there is nothing to render from. To render",
			"  it, build a Release that deploys a component this Recipe implements.")
		res.Valid = true
		return nil
	}

	// Tier 3: real render. Provider resolution is embedded-wins (the render-tier
	// catalog enables it upstream), so a core-provider recipe resolves its
	// provider from the binary's embedded set and renders even in a workspace
	// with no local catalog — matching how a real build resolves the provider.
	outcome, err := pipeline.RunPipeline(ctx, inputs, lookup, recipeLookup, subAggregateLoader, pipeline.Options{
		SuppressUnsupportedFieldWarnings: suppressUnsupportedFieldWarnings,
	})
	if err != nil {
		// The isolated render did not complete. This is a genuine finding — the
		// entity does not render standalone — but the cause may be a harness
		// limitation rather than an entity defect: a single-entity harness fills
		// only required values (with schema defaults flowing), and supplies no
		// attached credential or topology edge. An entity whose template reads a
		// value delivered only by such an input cannot render in isolation. The
		// underlying render error surfaces on the diagnostic stream through the
		// pipeline.
		res.Diagnostics = append(res.Diagnostics,
			"Render: not completed in isolation",
			"  The render requires build inputs a single-entity harness does not",
			"  synthesize (for example an attached credential or a topology edge). See the",
			"  render diagnostic below.")
		return fmt.Errorf("render: %w", err)
	}
	// The render completed, so the finished payload is what this reports from.
	result := outcome.Completed()
	keys := artifactKeysSorted(result.Artifacts)
	res.Diagnostics = append(res.Diagnostics, fmt.Sprintf("Render: %d artifact(s)", len(keys)))
	for _, k := range keys {
		res.Artifacts = append(res.Artifacts, RenderedArtifact{Key: k, Content: result.Artifacts[k]})
	}
	res.Valid = true
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
