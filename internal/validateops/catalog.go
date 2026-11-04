// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validateops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/enginefactory"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/declaredselector"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// CatalogResult is the structured outcome of whole-catalog validation.
//
// Exactly one failing category is populated on failure, in the halt-at-first
// order the pass applies: ExtendsErrs, then SelectorErrs, then OCICollision,
// then InputKindErrs. Each carries the full ordered diagnostic detail for its
// category so a caller renders it faithfully; the count-summary phrasing is the
// caller's to compose. On a clean pass, Valid is true and Notices carries the
// advisory lines (schema-pillar overlap, narrowing-on-extend) in deterministic
// order. A load failure is returned as the function's error, not in this result.
//
// SelectorErrs carries one error per label selector a Component or Feature
// document under the catalog directories declares on a dependency socket or an
// attachment slot that the selector grammar refuses, each naming its document,
// the socket or slot, and the grammar's reason — the findings the single-entity
// check reports for that document, in the order the documents are walked.
type CatalogResult struct {
	Valid         bool
	ExtendsErrs   []error
	SelectorErrs  []error
	OCICollision  error
	InputKindErrs []error
	Notices       []string
}

// ValidateCatalog runs the whole-catalog correctness pass over the catalog under
// dirs: it loads every entity and applies the schema-pillar (Pass-2) and library
// resolution/rejection checks, resolves the extends chains, refuses a socket or
// slot selector the selector grammar does not read, rejects OCI-location
// collisions, and reports provider input-kind capability mismatches. This is the
// catalog-wide counterpart to a build's per-Release closure validation — the home
// where a broken catalog entity is meant to surface.
//
// It halts at the first failing category and reports that category's full detail
// in the result; the categories are checked in order (extends, selector,
// oci-location, input-kind). A catalog that cannot load at all is returned as an
// error. The
// caller supplies the already-resolved workspace root, cache directory, catalog
// search directories, and the part of them somebody named.
//
// Before anything is loaded, every document under dirs is read and an Aggrega
// document that cannot be loaded halts the pass, naming each such file — see
// unloadableCatalogDocuments. The load on its own passes over a file whose YAML
// does not parse, as a build's load does, so without that walk the pass would
// report a clean catalog over a document it never read.
func ValidateCatalog(wsRoot, cacheDir string, dirs, named []string) (CatalogResult, error) {
	if err := unloadableCatalogDocuments(wsRoot, cacheDir, dirs, named); err != nil {
		return CatalogResult{}, err
	}
	cat, err := catalog.LoadAllWithNamedDirs(wsRoot, cacheDir, named, dirs...)
	if err != nil {
		if remedy := cachedLibraryRemedy(err, wsRoot, cacheDir, dirs, named); remedy != "" {
			return CatalogResult{}, &remediedLoadError{err: err, remedy: remedy}
		}
		return CatalogResult{}, err
	}
	// Read before the extends chains are merged, so each finding names the entity
	// whose document authors the keyword rather than every entity extending it.
	schemaNotices := catalogSchemaNotices(cat)
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		errs := make([]error, len(extErrs))
		for i, e := range extErrs {
			errs[i] = e
		}
		return CatalogResult{ExtendsErrs: errs}, nil
	}
	if errs := declaredSelectorRefusals(wsRoot, cacheDir, dirs, named); len(errs) > 0 {
		return CatalogResult{SelectorErrs: errs}, nil
	}
	if err := VerifyNoOCILocationCollision(cat); err != nil {
		return CatalogResult{OCICollision: err}, nil
	}
	catRecipes := make([]types.Recipe, 0, len(cat.Recipes))
	for _, r := range cat.Recipes {
		catRecipes = append(catRecipes, r)
	}
	catFeatureRecipes := make([]types.FeatureRecipe, 0, len(cat.FeatureRecipes))
	for _, fr := range cat.FeatureRecipes {
		catFeatureRecipes = append(catFeatureRecipes, fr)
	}
	if errs := InputKindCapabilityErrors(cat.CatalogLookup().ProviderPlugin, catRecipes, catFeatureRecipes); len(errs) > 0 {
		return CatalogResult{InputKindErrs: errs}, nil
	}
	// Surface the whole-catalog advisory notices accumulated during the eager
	// passes above — schema-pillar overlap (Pass-2) and narrowing-on-extend (from
	// ResolveAllExtends) — then the x-aggrega-* discouragement over each entity's
	// own schema content. Informational only: advisories never escalate the exit
	// code. Emission order is deterministic — each source walks sorted keys.
	// A fresh slice: the lint accessor may hand back the catalog's own.
	notices := append(append([]string(nil), cat.LintWarnings()...), schemaNotices...)
	return CatalogResult{Valid: true, Notices: notices}, nil
}

// catalogSchemaNotices is the x-aggrega-* discouragement over every Component's
// and Feature's schema content in cat, as advisory lines: Components first, then
// Features, each in identity order so the lines do not depend on the order the
// load met the files. cat is read before its extends chains are merged.
func catalogSchemaNotices(cat *catalog.Catalog) []string {
	var notices []string
	componentKeys := make([]string, 0, len(cat.Components))
	for k := range cat.Components {
		componentKeys = append(componentKeys, k)
	}
	sort.Strings(componentKeys)
	for _, k := range componentKeys {
		e := cat.Components[k]
		for _, d := range catalog.SchemaAdvisoriesOf(e.Metadata.Name, &e.Spec.Schema) {
			notices = append(notices, d.Message)
		}
	}
	featureKeys := make([]string, 0, len(cat.Features))
	for k := range cat.Features {
		featureKeys = append(featureKeys, k)
	}
	sort.Strings(featureKeys)
	for _, k := range featureKeys {
		e := cat.Features[k]
		for _, d := range catalog.SchemaAdvisoriesOf(e.Metadata.Name, &e.Spec.Schema) {
			notices = append(notices, d.Message)
		}
	}
	return notices
}

// cachedLibraryRemedy returns the remediation line for a failure of a check that
// reads libraries from catalog directories only — this package's whole-catalog
// and single-entity checks — or "" where there is nothing to say: the failure is
// not a schema reference that resolved to nothing, or the artifact cache holds no
// library the catalog directories do not supply.
//
// It is asked only on the failing path, because naming the libraries reads the
// cache and the catalog directories once more.
func cachedLibraryRemedy(err error, wsRoot, cacheDir string, dirs, named []string) string {
	if !errors.Is(err, compactform.ErrUnresolvedSchemaLibraryRef()) {
		return ""
	}
	return catalog.UnpinnedCachedLibraryRemedy(wsRoot, cacheDir, dirs, named...)
}

// remediedLoadError is a whole-catalog load failure followed by the remediation
// line for the cached libraries it could not use. Unwrap returns the failure, so
// its condition and its findings are matched as they were.
//
// Error renders the failure's whole cause chain and then the line. A
// whole-catalog report prints a failure as its flattened cause chain, where each
// frame's text is followed by the frames beneath it; a line carried in an outer
// frame's text would therefore be followed on the same line by the findings it
// explains. Rendering the chain here first leaves every frame beneath already
// said, so the flattened report ends with the line rather than continuing past it.
type remediedLoadError struct {
	err    error
	remedy string
}

// Error renders the failure's cause chain, then the remediation line beneath it.
func (e *remediedLoadError) Error() string {
	return cerrors.CauseChain(e.err) + "\n  " + e.remedy
}

// Unwrap returns the load failure the line follows.
func (e *remediedLoadError) Unwrap() error { return e.err }

// unloadableCatalogDocuments walks dirs as the catalog's own walk reads them,
// reads every document beneath them once, and returns the halt naming each
// Aggrega document among them that cannot be loaded, or nil when there is none.
// Which documents those are is [catalog.UnloadableDocument]'s answer, the same
// one whole-catalog lint reads at its walk. The walk completes before it
// halts, so every such file is named in one run rather than the first alone. A
// file reached twice — catalog directories may overlap — is read and reported
// once.
//
// A walk that stops early — on a link leading out of a directory's root — has
// not read every document, so it raises nothing of its own and leaves the
// refusal to the load that follows, which walks the same directories, stops on
// the same link and reports it in the words a whole-catalog load always has.
func unloadableCatalogDocuments(wsRoot, cacheDir string, dirs, named []string) error {
	var found []error
	complete := readCatalogDocuments(wsRoot, cacheDir, dirs, named, func(path string, data []byte, readErr error) {
		if f := catalog.UnloadableDocument(path, data, readErr, nil); f != nil {
			found = append(found, f)
		}
	})
	if !complete {
		return nil
	}
	return catalog.UnloadableDocumentsHalt(found)
}

// readCatalogDocuments walks dirs as the catalog's own walk reads them and hands
// visit every document beneath them once, with what reading it returned. A file
// reached twice — catalog directories may overlap — is read and handed over once.
//
// It reports whether every walk completed. A walk stops early on a link leading
// out of a directory's root, and the whole-catalog checks reading documents this
// way then raise nothing of their own, for the reason unloadableCatalogDocuments
// gives.
func readCatalogDocuments(wsRoot, cacheDir string, dirs, named []string, visit func(path string, data []byte, readErr error)) bool {
	seen := map[string]struct{}{}
	for _, dir := range dirs {
		err := catalog.WalkDocuments(wsRoot, dir, cacheDir, named, func(path string) error {
			abs, aerr := filepath.Abs(path)
			if aerr != nil {
				abs = path
			}
			if _, dup := seen[abs]; dup {
				return nil
			}
			seen[abs] = struct{}{}
			data, rerr := os.ReadFile(path)
			visit(path, data, rerr)
			return nil
		})
		if err != nil {
			return false
		}
	}
	return true
}

// declaredSelectorRefusals reads dirs as unloadableCatalogDocuments does and
// returns, for every Component and Feature document beneath them, the selectors
// that document declares that the selector grammar refuses — the findings the
// single-entity check reports for the same document, in walk order and, within
// one document, in the order it reports them.
//
// It reads the documents rather than the loaded catalog, for two reasons that
// point the same way. A finding names the file to edit, which the document in
// hand carries and an entry in the catalog does not; and a selector belongs to
// the document that writes it, so reading each document as authored reports one
// fault once — against the base that declares it — rather than once for every
// entity extending that base.
//
// It runs after the load has succeeded, so a document it reads but cannot parse
// is one the load passed over by design — a file under a catalog directory that
// is not an Aggrega document — and it is passed over here too. A walk that stops
// early raises nothing of its own, as unloadableCatalogDocuments explains, and a
// file that cannot be read is passed over: unloadableCatalogDocuments has already
// halted the pass on one.
func declaredSelectorRefusals(wsRoot, cacheDir string, dirs, named []string) []error {
	var refusals []error
	complete := readCatalogDocuments(wsRoot, cacheDir, dirs, named, func(path string, data []byte, readErr error) {
		if readErr == nil {
			refusals = append(refusals, documentSelectorRefusals(path, data)...)
		}
	})
	if !complete {
		return nil
	}
	return refusals
}

// documentSelectorRefusals is what the single-entity check reports about the
// selectors of one document under a catalog directory: a Component's or a
// Feature's refused selectors, and nothing for a document of any other kind or
// one that does not parse as either.
func documentSelectorRefusals(path string, data []byte) []error {
	kind, err := catalog.DocumentKindOf(data)
	if err != nil {
		return nil
	}
	switch envelope.Kind(kind) {
	case envelope.KindComponent:
		comp, err := catalog.ParseComponentEntity(data, path)
		if err != nil {
			return nil
		}
		return declaredselector.ComponentRefusals(path, &comp)
	case envelope.KindFeature:
		feat, err := catalog.ParseFeatureEntity(data, path)
		if err != nil {
			return nil
		}
		return declaredselector.FeatureRefusals(path, &feat)
	}
	return nil
}

// InputKindCapabilityErrors reports each Recipe / FeatureRecipe that declares a
// workload: block (synthesized mode) bound to a provider whose plugin cannot
// synthesize — a guaranteed render-time halt, surfaced here at validation time.
//
// The workload is checked whatever else the same document declares. A Recipe
// pairing a workload with a source or with artifacts still cannot have that
// workload rendered, so another, honourable input kind alongside it does not
// excuse the one the provider cannot honour.
//
// It is an early, non-authoritative detector, and non-authoritative carries two
// consequences that decide what a clean result from it is worth.
//
// A build does not run it. It is reached from the validate path, the editor tier,
// and the assistant-facing catalog and release scopes; no build command reaches
// it. So a clean result here is not a prediction that a build will pass — and it
// does not need to be, because a build refuses this same fault on its own at the
// render-time central input-kind gate, which is the single enforcement chokepoint
// and is reached identically from the Recipe and FeatureRecipe render paths.
//
// And it is silently skipped for any provider it cannot read a capability claim
// for: one whose serving provider Plugin does not resolve in scope, whose
// resolution errors, or whose resolved Plugin carries no contract: block
// (ContractFor ok=false). Skipping is correct in each of those cases — there is no
// capability claim to contradict, and forward-looking catalog scaffolding must not
// be flagged — but a skip and a pass are indistinguishable in the output, so over a
// catalog whose provider Plugin entities declare no contract this reports nothing
// at all, and nothing in the result says so.
//
// The detector reads a provider's input-kind support from the authoring-time
// capabilities claim on the provider-rendering Plugin entity that serves it,
// resolved through providerPlugin — the same provider→Plugin resolution the render
// route follows — and reuses the gate's own message clause
// (renderer.UnsupportedInputKindMessage) so the early and late diagnostics read
// identically. The workload predicate is the gate's own
// (!Spec.Workload.IsEmpty()), so presence is classified identically.
//
// Each finding is an occurrence of the gate's own condition,
// renderer.ErrUnsupportedInputKind, built through cerrors.Occurrence: it matches
// with errors.Is and carries that condition's codes to every surface reporting
// this detector's findings, as the halt at render does, while its text stays
// exactly what the detector composed.
func InputKindCapabilityErrors(providerPlugin func(ref string) (types.Plugin, bool, error), recipes []types.Recipe, featureRecipes []types.FeatureRecipe) []error {
	// The accessor publishes the condition as an error, for matching; the value it
	// returns is the one the condition's registration produced, which is what
	// cerrors.Occurrence takes. Were the assertion ever to fail, Occurrence refuses
	// the nil it leaves rather than building a finding that names no condition.
	unsupported, _ := renderer.ErrUnsupportedInputKind().(cerrors.Identity)
	var errs []error
	check := func(kind, identity, provider string, workload types.WorkloadTemplate) {
		if workload.IsEmpty() {
			return
		}
		caps, ok := enginefactory.ContractFor(providerPlugin, provider)
		if !ok || caps.Synthesized {
			return
		}
		errs = append(errs, cerrors.Occurrence(unsupported, fmt.Sprintf("%s %q: %s", kind, identity,
			renderer.UnsupportedInputKindMessage(provider, "synthesized", caps))))
	}
	for _, r := range recipes {
		check("Recipe", r.Metadata.Name+":"+r.Metadata.Version, r.Spec.Capability, r.Spec.Workload)
	}
	for _, fr := range featureRecipes {
		check("FeatureRecipe", fr.Metadata.Name+":"+fr.Metadata.Version, fr.Spec.Capability, fr.Spec.Workload)
	}
	// Sort by message so the diagnostic order is deterministic regardless of the
	// caller's iteration order (the whole-catalog mode ranges an unordered map).
	sort.Slice(errs, func(i, j int) bool { return errs[i].Error() < errs[j].Error() })
	return errs
}

// VerifyNoOCILocationCollision halts when two DISTINCT catalog identities map to
// the same OCI repository location (the lowercased, identity-derived coordinate
// from OCILocation). Such a collision would silently overwrite one entity's
// published bytes with another's at population time — the loud counterpart to the
// publish-path location encoding. Grouping is by version-independent identity
// (provider/name for recipes, metadata.name otherwise), so multiple versions of
// one identity sharing a location — distinguished by tag — are not a collision.
func VerifyNoOCILocationCollision(cat *catalog.Catalog) error {
	byLoc := map[string]map[string]struct{}{}
	add := func(kind, provider, name string) {
		loc := catalog.OCILocation(kind, provider, name)
		// The version-independent identity, composed by the catalog's own identity
		// formula: a recipe's capability with any version pin removed, then its
		// name; the bare name for every other kind.
		ident := catalog.RecipeIdentityURI(provider, name, "")
		if byLoc[loc] == nil {
			byLoc[loc] = map[string]struct{}{}
		}
		byLoc[loc][ident] = struct{}{}
	}
	for _, e := range cat.Components {
		add("Component", "", e.Metadata.Name)
	}
	for _, e := range cat.Features {
		add("Feature", "", e.Metadata.Name)
	}
	for _, e := range cat.Aggregates {
		add("Aggregate", "", e.Metadata.Name)
	}
	for _, e := range cat.Recipes {
		add("Recipe", e.Spec.Capability, e.Metadata.Name)
	}
	for _, e := range cat.FeatureRecipes {
		add("FeatureRecipe", e.Spec.Capability, e.Metadata.Name)
	}
	for _, e := range cat.SchemaLibraries {
		add("SchemaLibrary", "", e.Metadata.Name)
	}
	for _, e := range cat.MappingLibraries {
		add("MappingLibrary", "", e.Metadata.Name)
	}

	var collisions []string
	for loc, idents := range byLoc {
		if len(idents) <= 1 {
			continue
		}
		names := make([]string, 0, len(idents))
		for id := range idents {
			names = append(names, id)
		}
		sort.Strings(names)
		collisions = append(collisions, fmt.Sprintf("%s <- %s", loc, strings.Join(names, ", ")))
	}
	if len(collisions) == 0 {
		return nil
	}
	sort.Strings(collisions)
	return fmt.Errorf("%w:\n  - %s", oci.ErrOCILocationCollision(), strings.Join(collisions, "\n  - "))
}
