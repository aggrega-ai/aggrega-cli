// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	"github.com/cognitio/aggrega-cli/internal/compiler/topology"
	cerrors "github.com/cognitio/aggrega-cli/pkg/errors"
	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/types"
	"github.com/cognitio/aggrega-cli/pkg/validator"
	"gopkg.in/yaml.v3"
)

// Catalog holds every entity loaded from a directory walk and exposes both a
// direct-map surface and adapter callbacks for the compiler pipeline.
//
// The map fields are public so callers that need to iterate (deprecation
// scans, the inspect command's catalog dump, tests asserting entity counts)
// can read them without going through a callback. The two surfaces are not
// redundant: the callback returners (CatalogLookup, RecipeLookup,
// SubAggregateLoader, plus the ProviderLookup method used as a method
// value) conform to interfaces each consumer package defines on its own
// side. Adapting at the catalog boundary keeps consumer interfaces free of
// pkg/catalog import dependencies and lets each consumer evolve its data
// needs without forcing the map layout to change.
//
// Recipes and FeatureRecipes index by spec.implements so the renderer's
// per-component lookup ("which Recipe implements core/api-server?") is a
// single map fetch. Components, Features, and Aggregates index by
// metadata.name because that is the URI the resolver receives. Providers
// index by metadata.name where the name string IS the dispatch key the
// renderer engineFactory uses ("helm", "helm-chart", "compose", and
// future kinds; an unregistered provider name produces a fatal halt at
// dispatch time).
type Catalog struct {
	Components       map[string]types.Component
	Features         map[string]types.Feature
	Recipes          map[string]types.Recipe         // keyed by spec.implements
	FeatureRecipes   map[string]types.FeatureRecipe  // keyed by spec.implements
	Aggregates       map[string]types.Aggregate      // keyed by metadata.name
	Providers        map[string]types.Provider       // keyed by metadata.name (e.g. "helm", "helm-chart")
	SchemaLibraries  map[string]types.SchemaLibrary  // keyed by metadata.name (PascalCase library identifier)
	MappingLibraries map[string]types.MappingLibrary // keyed by metadata.name (PascalCase library identifier)

	// componentSources / featureSources track the file path each entity was
	// loaded from. Populated by Pass 1 (indexFile) and consumed by Pass 2
	// (the schema-pillar validation pass at the end of LoadAll) so the
	// validator's error messages name the offending file. Unexported because
	// the bookkeeping is internal to LoadAll; downstream callers identify
	// entities by metadata.name, not by source path.
	componentSources map[string]string
	featureSources   map[string]string
}

// Load scans a single directory recursively for .yaml / .yml files and
// indexes every recognised entity into a fresh Catalog. Convenience wrapper
// over LoadAll; see LoadAll for the merge, error, and skip semantics.
func Load(dir string) (*Catalog, error) {
	return LoadAll(dir)
}

// LoadAll walks dirs in argument order, recursively reads every .yaml / .yml
// file under each, and indexes all six catalog-resident entity kinds into a
// single Catalog. Files are dispatched by their top-level "kind:" field; see
// indexFile for the dispatch table.
//
// Layered-catalog merge. Later directories override earlier ones on key
// collisions — last write wins on the entity's index key (spec.implements
// for Recipes / FeatureRecipes, metadata.name for the others). The pattern
// supports layered catalog authoring like `base/` + `recipes/<variant>/`
// where the variant directory's Recipe replaces the base entry under the
// same `implements` URI without further ceremony. Within a single directory
// the merge order is filesystem-defined (filepath.WalkDir order); duplicate
// keys inside one directory are not currently flagged.
//
// Error model. A read or unmarshal error on a recognised entity kind aborts
// the walk and surfaces as a "catalog load %q: ..." wrapping the underlying
// error. YAML files whose top-level structure can't be unmarshalled into
// the entityHeader peek (malformed YAML lacking a parseable "kind:" field)
// are silently skipped — the catalog tree may legitimately contain
// non-entity YAML (catalog-conventions docs, fixture configs, build
// metadata) and the loader does not assume every .yaml file is an entity.
// Unknown kinds (e.g. "Manifest", "Release", or a typo'd kind) parse cleanly
// and are also silently skipped — Manifest and Release are deployment-side
// entities loaded through different code paths and are expected to coexist
// in some directory layouts.
//
// Recipe-side side effects. The Recipe dispatch arm runs validator.ValidateRecipe
// at load time (load-time shape rules from chapter 16: valuesMapping ⇔
// source pairing, workload mutex, at-least-one-input-kind, artifacts
// basename collision, eval-provider input-kind ban) and rewrites a relative
// string spec.source to an absolute path against the Recipe file's
// directory so co-located charts stay resolvable when the consuming code
// is run from a different working directory.
//
// Two-pass load model. Pass 1 walks dirs and hydrates every entity into the
// typed maps via indexFile. Pass 2 runs at the end of LoadAll once Pass 1
// has populated the full SchemaLibraries map, and validates each Component
// and Feature's spec.schema body against the canonical pillar contract via
// validator.ValidateSchemaPillars. Splitting the work lets cross-document
// $ref expansion succeed regardless of filesystem walk order: a Feature
// authored before its SchemaLibrary in walk order still validates because
// the library is in the map by the time Pass 2 runs. Pass 1 errors are
// fail-fast (a YAML parse failure aborts the walk); Pass 2 errors are
// collected across entities and surfaced as a single aggregate CatalogError
// so authors fix all schema failures in one editing pass.
func LoadAll(dirs ...string) (*Catalog, error) {
	c := &Catalog{
		Components:       make(map[string]types.Component),
		Features:         make(map[string]types.Feature),
		Recipes:          make(map[string]types.Recipe),
		FeatureRecipes:   make(map[string]types.FeatureRecipe),
		Aggregates:       make(map[string]types.Aggregate),
		Providers:        make(map[string]types.Provider),
		SchemaLibraries:  make(map[string]types.SchemaLibrary),
		MappingLibraries: make(map[string]types.MappingLibrary),
		componentSources: make(map[string]string),
		featureSources:   make(map[string]string),
	}
	for _, dir := range dirs {
		if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || (!strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml")) {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, "read catalog file")
			}
			return c.indexFile(data, path)
		}); err != nil {
			return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: dir}, "catalog load")
		}
	}

	if err := c.validateSchemaPillarsPass(); err != nil {
		return nil, err
	}
	return c, nil
}

// validateSchemaPillarsPass is Pass 2 of LoadAll: walks every Component and
// Feature in sorted-name order, runs validator.ValidateSchemaPillars against
// the entity's spec.schema yaml.Node with the now-fully-populated
// SchemaLibraries map for cross-document $ref expansion, collects every
// failure, and returns a single aggregated CatalogError when at least one
// entity failed. Sorted-name iteration is required for diagnostic stability:
// a map-iteration order would flap across runs and make CI logs unstable
// against the deterministic-output invariant.
func (c *Catalog) validateSchemaPillarsPass() error {
	var validationErrors []error

	componentNames := make([]string, 0, len(c.Components))
	for name := range c.Components {
		componentNames = append(componentNames, name)
	}
	sort.Strings(componentNames)
	for _, name := range componentNames {
		comp := c.Components[name]
		if err := validator.ValidateSchemaPillars(
			"Component",
			name,
			c.componentSources[name],
			&comp.Spec.Schema,
			c.SchemaLibraries,
		); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}

	featureNames := make([]string, 0, len(c.Features))
	for name := range c.Features {
		featureNames = append(featureNames, name)
	}
	sort.Strings(featureNames)
	for _, name := range featureNames {
		feat := c.Features[name]
		if err := validator.ValidateSchemaPillars(
			"Feature",
			name,
			c.featureSources[name],
			&feat.Spec.Schema,
			c.SchemaLibraries,
		); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}

	if len(validationErrors) == 0 {
		return nil
	}
	parts := make([]string, 0, len(validationErrors))
	for _, e := range validationErrors {
		parts = append(parts, e.Error())
	}
	return cerrors.New(
		cerrors.CatalogError,
		cerrors.SourceLocation{},
		fmt.Sprintf("catalog load: %d schema pillar validation error(s):\n  - %s",
			len(validationErrors), strings.Join(parts, "\n  - ")),
		nil,
		nil,
	)
}

// entityHeader peeks at the top-level "kind:" field before deciding which
// pkg/types struct to unmarshal into. The peek is cheap (a partial YAML
// decode that only touches one field) and lets indexFile dispatch through
// a single switch on Kind without paying the cost of attempting six
// speculative full-struct unmarshals per file.
type entityHeader struct {
	Kind string `yaml:"kind"`
}

// indexFile is the single point of dispatch over the six catalog-resident
// entity kinds — the SSOT for "what counts as a catalog entity" in this
// package. The switch arms enumerate exactly the eight catalog entity
// kinds minus Manifest and Release (which are deployment-side and are
// silently skipped — see LoadAll's error model). Adding a new
// catalog-resident kind requires extending the switch here AND adding
// the matching map field on Catalog.
func (c *Catalog) indexFile(data []byte, path string) error {
	var header entityHeader
	if err := yaml.Unmarshal(data, &header); err != nil {
		return nil // skip non-entity YAML files silently
	}

	switch header.Kind {
	case "Component":
		var e types.Component
		if err := yaml.Unmarshal(data, &e); err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, "unmarshal Component")
		}
		// DiscourageDirectXAggregaInSchema runs at Pass 1 (per-file) because
		// it walks for x-aggrega-* extension keys statically — it needs
		// neither SchemaLibrary access nor compactform expansion. The
		// pillar-shape + body validation runs at Pass 2 via
		// validateSchemaPillarsPass; keeping the two checks in separate
		// passes keeps each pass's responsibility clear.
		validator.DiscourageDirectXAggregaInSchema(e.Metadata.Name, &e.Spec.Schema)
		c.Components[e.Metadata.Name] = e
		c.componentSources[e.Metadata.Name] = path
	case "Feature":
		var e types.Feature
		if err := yaml.Unmarshal(data, &e); err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, "unmarshal Feature")
		}
		validator.DiscourageDirectXAggregaInSchema(e.Metadata.Name, &e.Spec.Schema)
		c.Features[e.Metadata.Name] = e
		c.featureSources[e.Metadata.Name] = path
	case "Recipe":
		var e types.Recipe
		if err := yaml.Unmarshal(data, &e); err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, "unmarshal Recipe")
		}
		// Load-time shape rules (chapter 16): valuesMapping ⇔ source pairing,
		// workload/workloadFrom mutex, at-least-one-input-kind, artifacts
		// basename collision, eval-provider input-kind ban. Returns a
		// CATALOG_ERROR-shaped error on violation.
		if err := validator.ValidateRecipe(&e, path); err != nil {
			return err
		}
		// Resolve relative string sources against the Recipe file's directory so
		// co-located charts stay bundled with their Recipe (on disk today, OCI
		// artifact tomorrow). Absolute paths and non-string sources pass through.
		if s, ok := e.Spec.Source.(string); ok && s != "" && !filepath.IsAbs(s) {
			abs, err := filepath.Abs(filepath.Join(filepath.Dir(path), s))
			if err != nil {
				return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, fmt.Sprintf("resolve Recipe source %q", s))
			}
			e.Spec.Source = abs
		}
		// Resolve relative $import: paths in artifacts entries against the
		// Recipe file's directory so co-located artifact files stay bundled
		// with their Recipe. Inline entries (no $import: target) are
		// unaffected.
		for _, name := range e.Spec.Artifacts.Keys() {
			entry, _ := e.Spec.Artifacts.Get(name)
			if entry.Import == "" || filepath.IsAbs(entry.Import) {
				continue
			}
			abs, err := filepath.Abs(filepath.Join(filepath.Dir(path), entry.Import))
			if err != nil {
				return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, fmt.Sprintf("resolve Recipe artifact %q $import target %q", name, entry.Import))
			}
			entry.Import = abs
			e.Spec.Artifacts.Set(name, entry)
		}
		c.Recipes[e.Spec.Implements] = e
	case "FeatureRecipe":
		var e types.FeatureRecipe
		if err := yaml.Unmarshal(data, &e); err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, "unmarshal FeatureRecipe")
		}
		// Resolve relative string sources against the FR file's directory so
		// co-located fragments (compose-bridge's bridge-fragment.yaml,
		// secret-FR's chart dir, etc.) stay bundled with their FR. Mirrors the
		// Recipe-source rewrite above.
		if s, ok := e.Spec.Source.(string); ok && s != "" && !filepath.IsAbs(s) {
			abs, err := filepath.Abs(filepath.Join(filepath.Dir(path), s))
			if err != nil {
				return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, fmt.Sprintf("resolve FeatureRecipe source %q", s))
			}
			e.Spec.Source = abs
		}
		c.FeatureRecipes[e.Spec.Implements] = e
	case "Aggregate":
		var e types.Aggregate
		if err := yaml.Unmarshal(data, &e); err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, "unmarshal Aggregate")
		}
		c.Aggregates[e.Metadata.Name] = e
	case "Provider":
		var e types.Provider
		if err := yaml.Unmarshal(data, &e); err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, "unmarshal Provider")
		}
		c.Providers[e.Metadata.Name] = e
	case "SchemaLibrary":
		var e types.SchemaLibrary
		if err := yaml.Unmarshal(data, &e); err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, "unmarshal SchemaLibrary")
		}
		// Validate each schema entry's content + each template body for
		// forbidden JSON Schema keywords ($dynamicAnchor / $dynamicRef)
		// at parse time. Authors who use these keywords get a clear
		// error citing the file path; the publishing-linter (separate
		// WI) layers a redundant guard at publish time. The
		// InstantiateTemplate post-substitution scan closes the
		// parametric-introduction route at instantiation time.
		for name, entry := range e.Spec.Schemas.All() {
			if err := types.ValidateSchemaEntry(&entry); err != nil {
				return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, fmt.Sprintf("SchemaLibrary %q schema %q: %v", e.Metadata.Name, name, err))
			}
		}
		for name, tmpl := range e.Spec.Templates.All() {
			if err := types.ValidateTemplateBody(&tmpl); err != nil {
				return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, fmt.Sprintf("SchemaLibrary %q template %q: %v", e.Metadata.Name, name, err))
			}
		}
		c.SchemaLibraries[e.Metadata.Name] = e
	case "MappingLibrary":
		var e types.MappingLibrary
		if err := yaml.Unmarshal(data, &e); err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, "unmarshal MappingLibrary")
		}
		// No template-body content validation analogous to SchemaLibrary's
		// $dynamicAnchor / $dynamicRef scan — MappingLibrary entries are
		// opaque Go template strings; the compiler does not parse them
		// at entity load. Content semantics (including any cross-library
		// $import: references) resolve at Recipe / FeatureRecipe render
		// time via the Go template engine.
		c.MappingLibraries[e.Metadata.Name] = e
	}
	// Unknown kinds silently skipped (Manifest, Release handled separately)
	return nil
}

// ProviderLookup returns the loaded Provider whose metadata.name matches
// name, plus a found flag. Names track the renderer's engineFactory
// dispatch keys ("helm", "helm-chart", "compose", and future kinds; an
// unregistered provider name produces a fatal halt at dispatch time).
//
// The method's signature `(string) → (types.Provider, bool)` matches the
// renderer's renderer.ProviderLookup function type exactly, so callers pass
// the method as a method value (`cat.ProviderLookup`) directly into
// renderer.RenderInput without an explicit adapter. The tuple-return shape
// (zero value + false on miss, not a sentinel error) lets callers branch
// cheaply when the provider is optional — the renderer treats an absent
// Provider as "no baseSchema validation available for sourceless mode" and
// continues, where a sentinel error would force the caller to either
// distinguish miss-vs-other-error or swallow both cases.
func (c *Catalog) ProviderLookup(name string) (types.Provider, bool) {
	p, ok := c.Providers[name]
	return p, ok
}

// DeprecationWarnings returns non-fatal notices about deprecated catalog
// shapes — formatted as a string slice the caller is free to print, log,
// or escalate to errors as fits its UX. The build command currently prints
// each entry to stderr after catalog load; tooling that wants stricter
// behaviour can match on the slice and exit non-zero.
//
// Contract. The function never returns an error and never mutates the
// catalog. An empty slice means "no deprecated shapes detected"; the
// presence of a warning never blocks Catalog use. Iteration is sorted by
// entity name within each kind so the message stream is deterministic.
//
// Current scope. Flags Components and Features that declare the legacy
// secretNames field while leaving the richer secretsSchema empty —
// authors should migrate to secretsSchema. New deprecation rules attach
// here: each rule reads its own catalog state, formats one message per
// affected entity, and appends to msgs. The function is the single
// point where "what counts as a deprecated catalog shape" lives; rules
// living elsewhere risk drifting out of the build command's warning
// stream.
func (c *Catalog) DeprecationWarnings() []string {
	var msgs []string
	compNames := make([]string, 0, len(c.Components))
	for name := range c.Components {
		compNames = append(compNames, name)
	}
	sort.Strings(compNames)
	for _, name := range compNames {
		comp := c.Components[name]
		if len(comp.Spec.SecretNames) > 0 && comp.Spec.SecretsSchema.Len() == 0 {
			msgs = append(msgs, fmt.Sprintf("Component %q uses deprecated secretNames; migrate to secretsSchema", name))
		}
	}
	featNames := make([]string, 0, len(c.Features))
	for name := range c.Features {
		featNames = append(featNames, name)
	}
	sort.Strings(featNames)
	for _, name := range featNames {
		feat := c.Features[name]
		if len(feat.Spec.SecretNames) > 0 && feat.Spec.SecretsSchema.Len() == 0 {
			msgs = append(msgs, fmt.Sprintf("Feature %q uses deprecated secretNames; migrate to secretsSchema", name))
		}
	}
	return msgs
}

// ResolveAllExtends walks every Component and Feature in the catalog and
// resolves its extends chain in-place via the validator package's
// ResolveComponentExtends / ResolveFeatureExtends. The merge follows the
// two-tiered inheritance pattern: deep-merge the YAML envelope, compose
// the Schema with parent under JSON Schema narrowing semantics (a child
// may narrow a parent's constraint, never relax it). Cycles are detected
// by the validator and surface as errors rather than infinite loops.
//
// Precondition. Must be called after Load / LoadAll and before any
// downstream consumer reads from the catalog (CatalogLookup, RecipeLookup,
// the inspect command's render, the resolver's guard checks). Catalog
// entries with non-empty Spec.Extends are stored unresolved by the loader;
// downstream code that reads those entries directly will see the child's
// own metadata + Schema only. There is no compiler-side guard that
// catches "ResolveAllExtends was skipped" — caller discipline at the
// command-line entry points (cmd/aggrega/build.go and inspect.go each
// call this immediately after Load) carries the contract today.
//
// Determinism. Both Component and Feature passes iterate sorted-name keys
// before mutating the underlying maps. The mutation order is sorted; the
// final map state is order-independent (resolved entities replace the
// child key, not the parent key, so the order one sibling-or-cousin
// resolves before another does not affect what a third resolves to). The
// sorted iteration is the catalog-side contribution to the byte-for-byte
// deterministic-output contract — error messages and any logged trace of
// resolution order both come out byte-identical across runs.
//
// Error model. Errors are collected into the returned slice rather than
// short-circuiting on the first failure, so a malformed parent in one
// chain does not mask broken extends elsewhere in the catalog. Each error
// carries its own entity context via validator.ValidationError. Callers
// (cmd/aggrega/build.go, cmd/aggrega/inspect.go) print every error and
// exit non-zero when the slice is non-empty.
func (c *Catalog) ResolveAllExtends() []*validator.ValidationError {
	var errs []*validator.ValidationError

	compLookup := func(uri string) (types.Component, bool) {
		comp, ok := c.Components[uri]
		return comp, ok
	}
	featLookup := func(uri string) (types.Feature, bool) {
		feat, ok := c.Features[uri]
		return feat, ok
	}

	compKeys := make([]string, 0, len(c.Components))
	for k := range c.Components {
		compKeys = append(compKeys, k)
	}
	sort.Strings(compKeys)
	for _, name := range compKeys {
		comp := c.Components[name]
		if comp.Spec.Extends == "" {
			continue
		}
		resolved, vErrs := validator.ResolveComponentExtends(comp, compLookup)
		if len(vErrs) > 0 {
			errs = append(errs, vErrs...)
			continue
		}
		c.Components[name] = resolved
	}

	featKeys := make([]string, 0, len(c.Features))
	for k := range c.Features {
		featKeys = append(featKeys, k)
	}
	sort.Strings(featKeys)
	for _, name := range featKeys {
		feat := c.Features[name]
		if feat.Spec.Extends == "" {
			continue
		}
		resolved, vErrs := validator.ResolveFeatureExtends(feat, featLookup)
		if len(vErrs) > 0 {
			errs = append(errs, vErrs...)
			continue
		}
		c.Features[name] = resolved
	}

	return errs
}

// CatalogLookup returns a resolver.CatalogLookup whose Component and Feature
// closures read from this catalog's Components / Features maps.
//
// The returned shape is defined on the resolver side — pkg/resolver owns
// the resolver.CatalogLookup struct and the function signatures it expects.
// Returning that struct from here, populated with closures over c, is the
// adapter pattern the package uses to keep the resolver free of any
// pkg/catalog import dependency: the resolver's guard checks reach back
// into the catalog through these closures rather than through a typed
// reference to *Catalog. Same pattern repeats in RecipeLookup
// (renderer.RecipeLookup) and SubAggregateLoader (topology.SubAggregateLoader).
func (c *Catalog) CatalogLookup() resolver.CatalogLookup {
	return resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			comp, ok := c.Components[typeURI]
			return comp, ok
		},
		Feature: func(typeURI string) (types.Feature, bool) {
			feat, ok := c.Features[typeURI]
			return feat, ok
		},
	}
}

// RecipeLookup returns a renderer.RecipeLookup whose Recipe and FeatureRecipe
// closures resolve a component or feature type URI to the matching catalog
// entry. The closures key on spec.implements with the language's documented
// three-form precedence and return a pointer-plus-bool so callers can either
// use the returned Recipe (typically copying or borrowing) or branch cleanly
// on miss.
//
// The lookup walks three forms in order: exact-string match first, then
// caret-range match (the caller's version satisfies a "<base>:^<version>"
// constraint declared by some Recipe in the catalog), then unversioned
// fallback ("<base>" alone). When multiple caret-form entries match the
// caller's version, the one with the highest baseline version wins. See
// matchImplements in version_match.go for the helper that walks the
// precedence; the lookup closures here just dispatch through it.
func (c *Catalog) RecipeLookup() renderer.RecipeLookup {
	return renderer.RecipeLookup{
		Recipe: func(componentType string) (*types.Recipe, bool) {
			r, ok := matchImplements(c.Recipes, componentType)
			if !ok {
				return nil, false
			}
			return &r, true
		},
		FeatureRecipe: func(featureType string) (*types.FeatureRecipe, bool) {
			fr, ok := matchImplements(c.FeatureRecipes, featureType)
			if !ok {
				return nil, false
			}
			return &fr, true
		},
	}
}

// SubAggregateLoader returns a topology.SubAggregateLoader closure that the
// topology builder calls when it encounters a sub-aggregate reference
// during graph construction. The closure looks up the named Aggregate in
// this catalog's Aggregates map.
//
// The signature here returns `(*types.Aggregate, error)` rather than the
// `(value, bool)` shape used by CatalogLookup / RecipeLookup / ProviderLookup
// because the topology package treats a sub-aggregate miss as a fatal
// graph-construction failure (the parent Aggregate references a
// sub-aggregate that does not exist; the build cannot continue). The
// error carries the missing type name so callers can surface it without
// needing to recompose the diagnostic.
func (c *Catalog) SubAggregateLoader() topology.SubAggregateLoader {
	return func(typeName string) (*types.Aggregate, error) {
		agg, ok := c.Aggregates[typeName]
		if !ok {
			return nil, cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{}, fmt.Sprintf("sub-aggregate %q not found in catalog", typeName), nil, nil)
		}
		return &agg, nil
	}
}
