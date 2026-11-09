// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validateops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/apiversion"
	"github.com/aggrega-ai/aggrega-cli/internal/authoredshape"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/internal/sourceref"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/declaredselector"
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
// declined-check lines naming any required uses sockets, required attachment
// slots and presence mandates a single-entity run could not cover, the
// not-attempted explanation when the Recipe's source is one
// a single-entity run cannot fetch or when its render stopped while its
// templates read what a build of one cannot supply, the explanation of a run that
// stopped on a failed assertion, and the render-failure explanation when a render
// that WAS attempted did not complete for any other reason — each verbatim and
// newline-free (a renderer emits one per line). Artifacts carries the rendered output of a
// render-bearing Recipe, ordered by key, so a caller can print the bytes or
// summarize a count. Notes
// carries advisory lines a caller routes to a diagnostic stream rather than the
// primary report. Kind is the resolved entity kind, empty when the input never
// resolved to a known kind (in which case the returned error explains why and
// Diagnostics is empty).
//
// Warnings carries what the check reports without failing: a run that stopped on
// a failed assertion, which this check reports as a warning for the reason
// failedAssertion gives. Each entry is the error the run stopped with — its text
// is that stop's own followed by the sentence saying a Release must satisfy the
// assertion, and its chain is the stop's — so a caller places it and reads its
// condition exactly as it would a blocking error's, and routes it to the stream
// its advisories go to. A result carrying one is Valid, and the error returned
// beside it is nil.
//
// Refused is the record of the catalog files the tolerant catalog load refused,
// in the order the load recorded them, and nil where the check loaded no catalog
// or the load refused nothing. It is filled on every scope that loads a catalog —
// Component, Feature and Recipe — and this check reports nothing from it itself.
// The one failure it is read for today is a Recipe's spec.implements target the
// catalog does not hold, which is returned carrying that reference as a
// closure.UnresolvedEntityError; the account of which refused file explains it
// is composed by the caller that renders the failure, so it reads as every other
// surface's account of a refused file does.
type EntityResult struct {
	Path        string
	Kind        string
	Valid       bool
	Diagnostics []string
	Artifacts   []RenderedArtifact
	Notes       []string
	Warnings    []error
	Refused     []catalog.SkippedEntity
}

// ValidateEntity reads the entity file, dispatches on its kind, and runs the
// tiered checks: schema envelope + shape (all kinds), pillar structure
// (Component / Feature), and isolated render (Recipe). It returns the first
// blocking error so a schema break halts before any catalog work, alongside the
// partial result accumulated up to that point so a caller can still render the
// progress that preceded the failure.
//
// A failed assertion is not blocking here: it is returned in the result's
// Warnings, with the result valid and no error, for the reason failedAssertion
// gives. Every other stop keeps its place as the returned error.
//
// A value the kind requires that the document leaves empty is reported ahead
// of every other tier, and every such value is reported at once: each is named
// as a placeholder still owed, with what it takes read from the kind's own
// schema. See placeholderRefusal for why that reading sits beside the envelope
// gate rather than inside it.
//
// A kind this scope does not accept is refused with an [UnacceptedKindError],
// naming the kinds it does accept and the scope that checks the refused kind
// instead.
//
// The catalog directories are the already-resolved first-win search stack, and
// named the part of it somebody named, as the resolver returned both; the
// workspace root and cache directory are resolved likewise by the caller.
//
// ctx is the caller's own. The isolated-render tier drives the real compilation
// pipeline twice, loading and running provider guests, so ending the caller's
// run ends that work: a guest call whose context ends unwinds rather than being
// noticed only once the guest next returns.
//
// A surface that reports the result calls compilerops.ValidateSingleEntity
// rather than this. The result hands back the catalog load's refusals raw, in
// Refused, and the failure they explain carries only its reference: the
// account naming the refused file is composed there, so a surface calling this
// directly reports that failure without naming the file that caused it.
func ValidateEntity(ctx context.Context, wsRoot, cacheDir string, dirs, named []string, path string, suppressUnsupportedFieldWarnings bool, opts ...EntityOption) (EntityResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return EntityResult{Path: path}, fmt.Errorf("read %q: %w", path, err)
	}
	return ValidateEntityContent(ctx, wsRoot, cacheDir, dirs, named, path, data, suppressUnsupportedFieldWarnings, opts...)
}

// EntityOption supplies an input the single-entity check can run without.
type EntityOption func(*entityOptions)

// entityOptions is what the options supply.
type entityOptions struct {
	// display is where the check's isolated runs display what they raise as they
	// raise it — the lines a build shows a reader at a terminal. Nil shows nothing.
	display io.Writer
}

// WithDisplay hands the check the writer its isolated runs display on: a
// surface whose reader looks at a terminal hands its error stream. Without it
// the runs show nothing as they go, and what the check reports is the result
// it returns.
func WithDisplay(w io.Writer) EntityOption {
	return func(o *entityOptions) { o.display = w }
}

// ValidateEntityContent runs the checks ValidateEntity runs, over data as the
// content of the entity file at path rather than over the bytes path holds on
// disk — which is what an editor holds before the file is saved.
//
// path still anchors everything the entity names relative to itself, as it does
// for ValidateEntity: a Recipe's local source, a file an import names, and the
// directory a path may not leave are all read against the directory holding
// path. Only the entity's own bytes come from data. The catalog the checks
// resolve through is read from the directories handed in, as it stands on disk.
//
// It is the one implementation of the single-entity check; ValidateEntity reads
// the file and calls it, so a check of a saved file and a check of the same
// bytes held unsaved cannot come to differ.
//
// A surface that reports the result calls compilerops.ValidateSingleEntityContent
// rather than this, for the reason ValidateEntity gives: Refused is handed back
// raw, and the account naming a refused file is composed there.
func ValidateEntityContent(ctx context.Context, wsRoot, cacheDir string, dirs, named []string, path string, data []byte, suppressUnsupportedFieldWarnings bool, opts ...EntityOption) (EntityResult, error) {
	var o entityOptions
	for _, opt := range opts {
		opt(&o)
	}
	var res EntityResult
	res.Path = path

	declaredKind, err := catalog.DocumentKindOf(data)
	if err != nil {
		return res, fmt.Errorf("parse YAML preamble in %q: %w", path, authoredshape.DescribeYAMLDecodeErrorIn(err, data))
	}
	if declaredKind == "" {
		return res, fmt.Errorf("couldn't determine entity kind from %q; expected an apiVersion/kind preamble", path)
	}
	kind, ok := knownEntityKind(declaredKind)
	if !ok {
		return res, &UnacceptedKindError{kind: declaredKind, path: path}
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
		return res, fmt.Errorf("%s: %w", envelopeStageLabel(verr), verr)
	}
	res.Diagnostics = append(res.Diagnostics, "Schema: valid")

	// Tier 1b: kind-specific shape validation, and the render tiers for the
	// rendering kinds.
	switch kind {
	case envelope.KindComponent:
		return res, validateComponentEntity(&res, wsRoot, cacheDir, dirs, named, path, data)
	case envelope.KindFeature:
		return res, validateFeatureEntity(&res, wsRoot, cacheDir, dirs, named, path, data)
	case envelope.KindWorkload:
		return res, validateWorkloadEntity(&res, path, data)
	case envelope.KindRecipe:
		return res, validateRecipeEntity(ctx, &res, wsRoot, cacheDir, dirs, named, path, data, suppressUnsupportedFieldWarnings, o.display)
	case envelope.KindFeatureRecipe:
		return res, validateFeatureRecipeEntity(&res, path, data)
	case envelope.KindPlugin:
		return res, validatePluginEntity(&res, path, data)
	}
	res.Valid = true
	return res, nil
}

// envelopeStageLabel names the stage a refusal by the document-envelope gate is
// reported under, read off the condition the refusal reports.
//
// The gate refuses most documents for failing their kind's schema, and those are
// labelled schema. It also applies rules a schema cannot state — a step id
// repeated within one pipeline is one — under conditions of their own, and the
// schema accepts such a document, so labelling that refusal schema would tell the
// reader the schema refused something it accepts. Those are labelled shape, the
// stage this report already uses for a kind's rules beyond its schema. A
// document declaring an API version this compiler no longer reads is refused by
// its schema too, so it keeps the schema label under the condition of its own. A
// refusal that is not a violation at all — a document that will not parse, an
// engine that could not run — keeps the schema label it has always had.
func envelopeStageLabel(refusal error) string {
	var violation *envelope.Error
	if errors.As(refusal, &violation) && violation != nil {
		switch violation.Condition() {
		case envelope.ErrEntityEnvelope(), envelope.ErrRetiredAPIVersion():
			return "schema"
		}
		return "shape"
	}
	return "schema"
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

// UnacceptedKindError refuses a document whose kind the single-entity scope
// does not validate. Its message names the kinds the scope does accept, read
// from the one list the scope's membership check reads, and — for an entity
// kind — the scope that checks a document of that kind instead, so the reader
// is told where to go rather than only where not to.
//
// It is a type rather than a plain error so a caller can tell this refusal,
// which is about the document's kind, from a refusal of the document itself,
// without reading the message: recognise it with errors.As. The message is
// prose for the person who ran the check and may be reworded.
type UnacceptedKindError struct {
	kind string
	path string
}

// Error renders the refusal: the kind and document refused, the kinds this
// scope accepts, and where a document of the refused kind is checked instead.
func (e *UnacceptedKindError) Error() string {
	msg := fmt.Sprintf("kind %q in %q is not a single-entity validation target; the kinds this scope accepts are %s",
		e.kind, e.path, joinKinds(SingleEntityKinds()))
	if route := unacceptedKindRoute(entitykind.Kind(e.kind)); route != "" {
		msg += ". " + route
	}
	return msg
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
// resolves extends, returning the catalog, the load's record of the files it
// refused, and an advisory note (empty when extends resolved cleanly). It is the
// single catalog entry point for the validate tiers that need catalog context
// (pillar libraries for Component / Feature, and the implements target plus
// provider Plugin for the render tiers).
//
// The load is tolerant: a catalog file that fails to parse or index is skipped
// rather than aborting the walk, so an unrelated broken entity elsewhere in the
// catalog never blocks validating the target in isolation — the same posture
// the build path takes over a catalog it does not fully own. What it skipped is
// returned rather than dropped, because a reference to an entity whose only
// document was refused resolves to nothing, and that record is the one place
// the reason is held. Extends resolution runs best-effort: a broken extends
// chain on an unrelated entity is surfaced as the returned note and does not
// fail the load, while the target's own merged shape still resolves.
func loadResolvedCatalog(wsRoot, cacheDir string, dirs, named []string) (*catalog.Catalog, []catalog.SkippedEntity, string, error) {
	cat, skipped, err := catalog.LoadPassOneTolerantWithNamedDirs(wsRoot, cacheDir, named, dirs...)
	if err != nil {
		return nil, nil, "", fmt.Errorf("catalog load: %w", err)
	}
	var note string
	// The chains resolve over the catalog directories alone, because validating one
	// entity reads no lockfile and no cache. So a parent only a pull would supply
	// stays missing here whatever has been pulled, and the note carries no advice
	// to pull — ResolveAllExtends, unlike ResolveAllExtendsWithPins, offers none.
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		note = fmt.Sprintf("  note: %d catalog extends resolution error(s); first: %s", len(extErrs), extErrs[0].Error())
	}
	return cat, skipped, note, nil
}

// validateComponentEntity runs the Component shape and pillar checks. The
// pillar check needs the catalog's SchemaLibraries map for cross-document $ref
// expansion, so the catalog is loaded to supply it.
//
// The shape check reads every label selector the document declares on a
// dependency socket or an attachment slot, and refuses each one the selector
// grammar does not read, under the condition a build refuses it under once an
// edge or a fill reaches it (declaredselector.ComponentRefusals). It needs no
// catalog, so it runs before the catalog is loaded, and it reads the document
// rather than the chain it extends.
//
// The entity is read through the shared per-entity resolve rather than
// unmarshalled here. That is what makes this surface agree with a build about
// what the file declares: the resolve is where an authored `$import:` on the
// schema surface is substituted, so a parse of its own would judge the entity the
// author asked about against a contract it had not read — the one entity where
// that matters most, since it is the one they named.
func validateComponentEntity(res *EntityResult, wsRoot, cacheDir string, dirs, named []string, path string, data []byte) error {
	comp, err := catalog.ParseComponentEntity(data, path)
	if err != nil {
		return fmt.Errorf("parse Component at %q: %w", path, err)
	}
	noteSchemaAdvisories(res, comp.Metadata.Name, &comp.Spec.Schema)
	if errs := declaredselector.ComponentRefusals(path, &comp); len(errs) > 0 {
		return fmt.Errorf("shape: %w", errors.Join(errs...))
	}
	cat, refused, note, err := loadResolvedCatalog(wsRoot, cacheDir, dirs, named)
	res.Refused = refused
	if note != "" {
		res.Notes = append(res.Notes, note)
	}
	if err != nil {
		return err
	}
	if err := validator.ValidateSchemaPillars("Component", comp.Metadata.Name, path, comp.Spec.Extends, &comp.Spec.Schema, cat.SchemaLibraries); err != nil {
		return fmt.Errorf("pillars: %w", withCachedLibraryRemedy(err, wsRoot, cacheDir, dirs, named))
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
// entity through the same shared per-entity resolve for the same reason. Ahead
// of it, the shape check refuses each label selector the document declares on a
// dependency socket or an attachment slot that the selector grammar does not
// read, as the Component path does (declaredselector.FeatureRefusals).
func validateFeatureEntity(res *EntityResult, wsRoot, cacheDir string, dirs, named []string, path string, data []byte) error {
	feat, err := catalog.ParseFeatureEntity(data, path)
	if err != nil {
		return fmt.Errorf("parse Feature at %q: %w", path, err)
	}
	noteSchemaAdvisories(res, feat.Metadata.Name, &feat.Spec.Schema)
	if errs := declaredselector.FeatureRefusals(path, &feat); len(errs) > 0 {
		return fmt.Errorf("shape: %w", errors.Join(errs...))
	}
	cat, refused, note, err := loadResolvedCatalog(wsRoot, cacheDir, dirs, named)
	res.Refused = refused
	if note != "" {
		res.Notes = append(res.Notes, note)
	}
	if err != nil {
		return err
	}
	if err := validator.ValidateSchemaPillars("Feature", feat.Metadata.Name, path, feat.Spec.Extends, &feat.Spec.Schema, cat.SchemaLibraries); err != nil {
		return fmt.Errorf("pillars: %w", withCachedLibraryRemedy(err, wsRoot, cacheDir, dirs, named))
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

// noteSchemaAdvisories adds to the result's advisory lines the x-aggrega-*
// discouragement's findings over the checked entity's own schema content, each
// as the warning line a build displays for it. The check reads the entity as
// authored, before any extends chain is merged in, so what it names is what this
// document wrote.
func noteSchemaAdvisories(res *EntityResult, entity string, schema *yaml.Node) {
	for _, d := range catalog.SchemaAdvisoriesOf(entity, schema) {
		res.Notes = append(res.Notes, pipeline.WarningLine(d))
	}
}

// withCachedLibraryRemedy follows a pillar failure of one entity with the
// remediation line for the cached libraries this check could not use, or returns
// the failure unchanged where there is nothing to say (see cachedLibraryRemedy).
//
// The line goes into the message of the diagnostic the failure already leads
// with, re-issued at the same category and source, rather than into a frame
// around it. The terminal renders a diagnostic from its outermost envelope and
// drops every frame above that, so a line carried in a wrapping frame would never
// be printed; and the re-issued head holds the failure's own text, so the
// failure is not printed a second time beneath it.
func withCachedLibraryRemedy(err error, wsRoot, cacheDir string, dirs, named []string) error {
	remedy := cachedLibraryRemedy(err, wsRoot, cacheDir, dirs, named)
	if remedy == "" {
		return err
	}
	var ce *cerrors.CompilerError
	if errors.As(err, &ce) && ce != nil {
		return cerrors.Wrap(err, ce.Category(), ce.Source(), ce.Message()+"\n  "+remedy)
	}
	return fmt.Errorf("%w\n  %s", err, remedy)
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
// Four checks of a real build are out of reach here, and each is declined rather
// than failed.
//
// The first is whether the Component's `required: true` uses sockets are
// populated. The synthesized build holds a single instance, so it contains
// no second component an edge could target, and no authoring of the synthesized
// documents can change that.
//
// The second is whether the Component's `required: true` attachment slots are
// filled, and whether an attachment of each Feature type its presence mandates
// name is present. The synthesized build attaches no Feature, because which
// Feature fills a slot or carries a mandated type is chosen by whoever deploys
// the Component, not by the Recipe — so no attachment exists here to bind the
// slot or satisfy the mandate, for the same reason no edge exists to populate a
// socket. A mandate whose declaration cannot be evaluated is not declined: it is
// a fault of the Component's own, and is reported as one.
//
// The third is the render itself, when the Recipe's source is one that has to
// be fetched. A single-entity run reads no lockfile and no cache, so it holds
// neither a record of which bytes that source resolves to nor a store to resolve
// them from, and the infrastructure code the Recipe names exists nowhere it can
// reach. That is asked of sourceref.NeedsFetch, which is the one home for the
// question, so this surface and the resolution it would otherwise drive draw the
// line in the same place. A source the workspace already holds is NOT declined:
// it needs no fetch, so a render over it is attempted and a failure in it is a
// real finding.
//
// The fourth is a render that stops while the Recipe's templates read what only
// a connection or an attached Feature supplies — anything under .context.inject,
// .context.uses or .features. The build of one wires no connection and attaches
// no Feature, so those reads find nothing, and a render that stops beside them
// cannot be judged here. Whether it is declined is decided by what the Recipe
// READS, as buildOfOneReads states, never by what the stop says; the render runs
// first and is not changed in any way to make the decision.
//
// Every decline is named in Diagnostics, so the report states which surface it
// did not cover instead of leaving a silent difference between what validate
// checks and what a build checks.
//
// One finding is reported rather than declined, at a lower tier than a build
// reports it: an assertion that calls fail, in the Recipe or in the Component it
// implements, is a warning here, for the reason failedAssertion gives.
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
func validateRecipeEntity(ctx context.Context, res *EntityResult, wsRoot, cacheDir string, dirs, named []string, path string, data []byte, suppressUnsupportedFieldWarnings bool, display io.Writer) error {
	rec, err := catalog.ParseRecipeEntity(data, path)
	if err != nil {
		return fmt.Errorf("shape: %w", err)
	}
	res.Diagnostics = append(res.Diagnostics, "Shape:  valid")

	cat, refused, note, err := loadResolvedCatalog(wsRoot, cacheDir, dirs, named)
	res.Refused = refused
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
		// A malformed pin names no version, so the target is reported as that
		// malformation rather than as a Component the catalog does not hold.
		if pe, malformed := types.ParseVersionPin(rec.Spec.Implements).Malformed(); malformed {
			return fmt.Errorf("render: %w", pe.At("in the recipe's spec.implements"))
		}
		// The reference rides on the failure so a caller holding the load's record
		// of refused files can tell whether one of them declares this Component. It
		// adds no condition: the message and what the failure matches are unchanged.
		return closure.NewUnresolvedEntityError(rec.Spec.Implements, closure.KindComponent,
			fmt.Errorf("render: implements target %q not found in catalog", rec.Spec.Implements))
	}

	recipeURI := catalog.RecipeMappingURI(rec.Metadata.Name, rec.Metadata.Version)
	harness, err := buildEntityHarness(rec.Spec.Implements, recipeURI, &comp, lookup)
	if err != nil {
		return err
	}
	harness.libraries = templateLibrariesOf(cat)

	// Decline the required-socket, required-slot and presence-mandate checks for
	// this run and say so, rather than report the author's own valid Recipe as
	// broken. The narrowing is applied to the catalog view THIS harness hands the
	// pipeline and to nothing else, so each rule itself is untouched: a build asks
	// it about the Component exactly as authored.
	declined := declinedRequirementsOf(&comp)
	tierLookup := lookupWithRequirementsDeclined(lookup, rec.Spec.Implements, declined)
	if len(declined.sockets) > 0 {
		res.Diagnostics = append(res.Diagnostics,
			fmt.Sprintf("Sockets: required uses sockets not checked (%s)", strings.Join(declined.sockets, ", ")),
			"  Validating a Recipe on its own places its Component in a build of one,",
			"  so no other component exists for a required socket to be wired to. Whether",
			"  each of these sockets is populated is checked when the Component is deployed",
			"  in an Aggregate.")
	}
	if len(declined.slots) > 0 {
		res.Diagnostics = append(res.Diagnostics,
			fmt.Sprintf("Attachments: required attachment slots not checked (%s)", strings.Join(declined.slots, ", ")),
			"  Validating a Recipe on its own places its Component in a build that attaches",
			"  no Feature, so nothing exists to fill a required slot. Whether each of these",
			"  slots is filled is checked when a Release that deploys the Component is built.")
	}
	if len(declined.mandates) > 0 {
		res.Diagnostics = append(res.Diagnostics,
			fmt.Sprintf("Attachments: presence mandates not checked (%s)", presenceMandateList(declined.mandates)),
			"  Validating a Recipe on its own places its Component in a build that attaches",
			"  no Feature, so nothing exists to carry a type the Component requires. Whether",
			"  an attachment of each of these types is present is checked when a Release",
			"  that deploys the Component is built.")
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
		suppressUnsupportedFieldWarnings, sourceref.NeedsFetch(rec.Spec.Source),
		buildOfOneReadsOf(&rec, cat, lookup), display)
}

// buildOfOneSuppliedRoots are the context paths under which a Recipe's template
// reads what only a real build supplies: the delivery pipe a connection or an
// attached Feature fills, the connections themselves, and the attached Features.
// A build of one wires no connection and attaches no Feature, so every read under
// these finds nothing there.
var buildOfOneSuppliedRoots = []string{".context.inject", ".context.uses", ".features"}

// buildOfOneReads is what a Recipe's templates read that a build of one cannot
// supply, as the read census reports it over every template the Recipe's render
// evaluates, imported content included (renderer.RecipeTemplateReads).
//
// paths are those reads — each at or beneath one of buildOfOneSuppliedRoots, or
// naming a path that encloses one, since reading the enclosing value reads what is
// beneath it — de-duplicated and in lexical order, which is the order the census
// itself reports a template's reads in. readable is false when the census could
// not be taken: a template that does not parse, or an import that does not
// resolve. Then nothing is known about what the Recipe reads, and nothing is
// declined on its account.
type buildOfOneReads struct {
	paths    []string
	readable bool
}

// buildOfOneReadsOf takes the census over the Recipe's templates, resolving its
// imports against the catalog's libraries and reading its artifacts with the
// delimiter pair its provider declares, and keeps the reads a build of one cannot
// supply.
func buildOfOneReadsOf(rec *types.Recipe, cat *catalog.Catalog, lookup resolver.CatalogLookup) buildOfOneReads {
	left, right := providerArtifactDelimiters(lookup, rec.Spec.Capability)
	reads, err := renderer.RecipeTemplateReads(rec, templateLibrariesOf(cat), left, right)
	if err != nil {
		return buildOfOneReads{}
	}
	seen := map[string]bool{}
	var paths []string
	for _, read := range reads {
		if seen[read] || !readsWhatABuildOfOneLacks(read) {
			continue
		}
		seen[read] = true
		paths = append(paths, read)
	}
	sort.Strings(paths)
	return buildOfOneReads{paths: paths, readable: true}
}

// templateLibrariesOf is the set of libraries a Recipe's `$import:` entries
// resolve against in this scope: the ones the catalog directories the check is
// handed hold. No lockfile is read here, so a library reaches the check only by
// sitting in one of those directories.
//
// The census of what a Recipe reads and the isolated render both take this one
// set. A render handed none would stop at the first import as a library the
// catalog does not hold while the census beside it resolved the same import, so
// the two would disagree about one Recipe in one run.
func templateLibrariesOf(cat *catalog.Catalog) renderer.RecipeTemplateLibraries {
	return renderer.RecipeTemplateLibraries{
		Mappings:  cat.MappingLibraries,
		Workloads: cat.WorkloadLibraries,
		Artifacts: cat.ArtifactLibraries,
	}
}

// readsWhatABuildOfOneLacks reports whether one census path is at, beneath, or
// encloses one of buildOfOneSuppliedRoots.
func readsWhatABuildOfOneLacks(read string) bool {
	for _, root := range buildOfOneSuppliedRoots {
		if read == root || strings.HasPrefix(read, root+".") || strings.HasPrefix(root, read+".") {
			return true
		}
	}
	return false
}

// providerArtifactDelimiters is the delimiter pair the Recipe's provider applies
// to artifacts, read from the provider entity the catalog lookup resolves the
// capability to — the claim a provider's own report is held to when it loads —
// or the usual pair where the lookup resolves none or the entity declares none.
func providerArtifactDelimiters(lookup resolver.CatalogLookup, capability string) (string, string) {
	if lookup.ProviderPlugin != nil {
		if p, found, err := lookup.ProviderPlugin(capability); err == nil && found {
			if d := p.Spec.Provider.Contract.ArtifactDelimiters; d != nil {
				return d.Left, d.Right
			}
		}
	}
	return "{{", "}}"
}

// recipeOwnRenderFaults are the conditions a render stop carries when the fault
// is the Recipe's own whatever a real build would supply: a reference the
// template is refused, in any of the forms the render refuses one. Each is
// matched through its exported accessor.
//
// A failed assertion is not here, and is not declined either: the render tier
// reports it as a warning before this set is consulted (failedAssertion). A
// template that does not parse is not here because no condition names it; it is
// ruled out by buildOfOneReads being unreadable.
var recipeOwnRenderFaults = []func() error{
	renderer.ErrTemplateUnknownRoot,
	renderer.ErrProvidesSelfContainment,
	renderer.ErrLocalsForwardReference,
	renderer.ErrLocalsUnknownReference,
	renderer.ErrArtifactPillarNotDeclared,
}

// declinesRenderStop reports whether a render that stopped with err is reported
// as not attempted rather than failed.
//
// It is decided by what the Recipe reads, never by what the stop says: a Recipe
// whose templates read anything a build of one cannot supply has a render this
// check cannot judge, so its stop is declined — unless the stop is one of
// recipeOwnRenderFaults, which no supplied value changes, or the reads could not
// be taken at all. The consequence is stated rather than closed: a fault of the
// Recipe's own that names no such condition — a misspelled key, say — in a Recipe
// that also reads such a value is declined with it, and the report carries the
// stop's own sentence so the reader can see which it was.
//
// A run its caller withdrew is not a render stop at all, so it is never declined:
// the withdrawal is returned as the caller's own, whatever the Recipe reads. A
// render that stopped on a failed assertion is never asked about here: the render
// tier reports it as a warning first, whatever the Recipe reads.
func (r buildOfOneReads) declinesRenderStop(err error) bool {
	if !r.readable || len(r.paths) == 0 {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	for _, own := range recipeOwnRenderFaults {
		if errors.Is(err, own()) {
			return false
		}
	}
	return true
}

// declinedRequirements names what a build of one cannot satisfy on the Component
// it places: the uses sockets and the attachment slots the Component declares
// `required: true`, and the presence mandates it declares that can be evaluated,
// each in the entity's author order.
type declinedRequirements struct {
	sockets  []string
	slots    []string
	mandates []types.RequiredAttachmentType
}

// declinedRequirementsOf reads the Component's required sockets, required slots
// and presence mandates, the three requirements no authoring of the synthesized
// documents can meet.
func declinedRequirementsOf(comp *types.Component) declinedRequirements {
	return declinedRequirements{
		sockets:  declaredRequiredSockets(comp),
		slots:    declaredRequiredSlots(comp),
		mandates: declaredPresenceMandates(comp),
	}
}

// declaredPresenceMandates returns every presence mandate the Component declares
// whose declaration can be evaluated, in the entity's author order.
//
// The synthesized build attaches no Feature at any scope, so no attachment of a
// mandated type can be present, and the declared set and the out-of-reach set
// coincide as they do for slots. The mandates are read through
// types.RequiredAttachmentTypes, the one reading of the annotation family the
// rule that judges them reads too, so the two cannot come to disagree about which
// annotations are mandates.
//
// A malformed mandate is left out on purpose. It is not a requirement a build of
// one cannot meet but a declaration no build can evaluate, which a build refuses
// whatever is attached, so the rule reporting it must still see it here.
//
// It reads the declarations and decides nothing: whether a mandate is met stays
// the analyzer's rule, and a build asks it about the Component as authored.
func declaredPresenceMandates(comp *types.Component) []types.RequiredAttachmentType {
	var out []types.RequiredAttachmentType
	for _, mandate := range types.RequiredAttachmentTypes(comp.Metadata.Annotations) {
		if mandate.Malformed == "" {
			out = append(out, mandate)
		}
	}
	return out
}

// presenceMandateList renders declined mandates for the report as each one's
// label and the Feature type it names, in the order the Component declares them —
// the two halves of the annotation as its author wrote it.
func presenceMandateList(mandates []types.RequiredAttachmentType) string {
	parts := make([]string, 0, len(mandates))
	for _, mandate := range mandates {
		parts = append(parts, mandate.Label+": "+mandate.Type)
	}
	return strings.Join(parts, ", ")
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

// declaredRequiredSlots returns the names of every attachment slot the
// Component declares `required: true`, in the entity's author order.
//
// The synthesized build attaches no Feature — not on the instance and not at
// any scope above it — so every required slot is one this harness cannot fill,
// and, as with sockets, the declared set and the out-of-reach set coincide.
//
// Like its socket counterpart it reads a declared flag and decides nothing:
// whether an unfilled required slot is an error stays the analyzer's rule, and a
// build asks it about the Component as authored.
func declaredRequiredSlots(comp *types.Component) []string {
	var out []string
	for name, slot := range comp.Spec.FeaturesSchema.All() {
		if slot.Required {
			out = append(out, name)
		}
	}
	return out
}

// lookupWithRequirementsDeclined returns a copy of lookup whose Component
// accessor reports typeURI's named uses sockets and named attachment slots as
// optional, and declares none of its named presence mandates. Every other type,
// and every other accessor, passes through untouched; with nothing to decline it
// returns lookup itself.
//
// The copy is what the single-entity render tiers read, so the narrowing reaches
// only this in-memory validation run — the catalog itself is never modified, and
// no other command shares the view. The socket and slot entries survive intact
// apart from the flag, so the checks that read a socket's per-edge schema or a
// slot's label constraint and delivery mechanisms still see exactly what the
// author declared, and the entries are rebuilt in author order so the run stays
// byte-identical across invocations.
//
// A presence mandate has no flag to clear — it is an annotation, and declaring it
// is what makes it a requirement — so a declined one is left out of the
// Component's annotations in the copy, and every other annotation is kept in
// author order, a malformed mandate among them. The same annotations are what the
// render context carries as the Component's catalog annotations, so a template in
// this run reading a declined mandate's key would find it absent; nothing a
// provider reads is among them.
func lookupWithRequirementsDeclined(lookup resolver.CatalogLookup, typeURI string, declined declinedRequirements) resolver.CatalogLookup {
	if lookup.Component == nil || (len(declined.sockets) == 0 && len(declined.slots) == 0 && len(declined.mandates) == 0) {
		return lookup
	}
	optionalSockets := namedSet(declined.sockets)
	optionalSlots := namedSet(declined.slots)
	undeclaredMandates := make(map[string]bool, len(declined.mandates))
	for _, mandate := range declined.mandates {
		undeclaredMandates[mandate.Key] = true
	}

	inner := lookup.Component
	narrowed := lookup
	narrowed.Component = func(t string) (types.Component, bool) {
		comp, found := inner(t)
		if !found || t != typeURI {
			return comp, found
		}
		// Fresh maps, not struct copies: OrderedMap holds its entries behind a
		// shared backing map, so writing through a copy would reach the catalog's
		// own component.
		if len(optionalSockets) > 0 {
			relaxed := types.OrderedMap[types.UsesSchemaEntry]{}
			for name, entry := range comp.Spec.UsesSchema.All() {
				if optionalSockets[name] {
					entry.Required = false
				}
				relaxed.Set(name, entry)
			}
			comp.Spec.UsesSchema = relaxed
		}
		if len(optionalSlots) > 0 {
			relaxed := types.OrderedMap[types.AttachmentSlot]{}
			for name, slot := range comp.Spec.FeaturesSchema.All() {
				if optionalSlots[name] {
					slot.Required = false
				}
				relaxed.Set(name, slot)
			}
			comp.Spec.FeaturesSchema = relaxed
		}
		if len(undeclaredMandates) > 0 {
			kept := types.OrderedMap[string]{}
			for key, value := range comp.Metadata.Annotations.All() {
				if !undeclaredMandates[key] {
					kept.Set(key, value)
				}
			}
			comp.Metadata.Annotations = kept
		}
		return comp, true
	}
	return narrowed
}

// namedSet returns names as a membership set.
func namedSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
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
	// releasePath is the label the synthesized Release is loaded under. No file
	// holds that Release, so the label is the only name it has, and the run is
	// handed the same one it was loaded under rather than none.
	releasePath string
	// libraries are what the Recipe's `$import:` entries resolve against when the
	// harness is rendered (templateLibrariesOf). The zero value holds none, so
	// every import then stops the render as one the catalog does not hold.
	libraries renderer.RecipeTemplateLibraries
}

// syntheticInstanceName is the single component-instance name the synthesized
// build uses. It is a fixed, target-agnostic label so the harness — and
// thus the rendered output an author inspects — is deterministic.
const syntheticInstanceName = "instance"

// buildEntityHarness synthesizes the minimal Aggregate + Manifest + Release the
// render tiers drive. The Aggregate carries one instance of the implements
// Component with each required value filled by a typed placeholder, and the
// Release fills each required infra field of that instance the same way, because
// the Release is where a real build's infra is written. Schema defaults for every
// other value and infra field materialize through the real resolver rather than
// being hand-written here. The Release routes the instance into a synthesized
// default boundary; it declares no values: block and no compute the Aggregate
// did not declare.
func buildEntityHarness(implements, recipeURI string, comp *types.Component, lookup resolver.CatalogLookup) (entityHarness, error) {
	placeholders, err := requiredPlaceholders(comp, lookup.SchemaLibraries)
	if err != nil {
		return entityHarness{}, fmt.Errorf("render: synthesize values: %w", err)
	}

	instance := map[string]any{"type": implements}
	if len(placeholders.values) > 0 {
		instance["values"] = placeholders.values
	}
	aggregate := map[string]any{
		"apiVersion": apiversion.APIVersion,
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
		"apiVersion": apiversion.APIVersion,
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

	releaseInstance := map[string]any{}
	if len(placeholders.infra) > 0 {
		releaseInstance["infra"] = placeholders.infra
	}
	releaseBytes, err := yaml.Marshal(map[string]any{
		"apiVersion": apiversion.APIVersion,
		"kind":       "Release",
		"metadata":   map[string]any{"name": "validate-probe", "environment": "validate", "version": "1.0.0"},
		"spec": map[string]any{
			"aggregate":  aggregatePath,
			"manifest":   "validate://manifest.yaml",
			"components": map[string]any{syntheticInstanceName: releaseInstance},
		},
	})
	if err != nil {
		return entityHarness{}, fmt.Errorf("render: marshal synthesized release: %w", err)
	}
	const releasePath = "validate://release.yaml"
	release, err := resolver.LoadReleaseFromBytes(releaseBytes, releasePath)
	if err != nil {
		return entityHarness{}, fmt.Errorf("render: synthesized release: %w", err)
	}

	return entityHarness{
		aggregateBytes: aggBytes,
		manifest:       manifest,
		release:        release,
		aggregatePath:  aggregatePath,
		releasePath:    releasePath,
	}, nil
}

// runRenderTiers drives the synthesized harness through the real pipeline: Tier
// 2 runs resolution, guards, and IR construction with render skipped; Tier 3
// runs the real render and records the emitted artifacts. Pipeline diagnostics
// (refusals, render warnings) surface on the process diagnostic stream
// through the pipeline itself; the accumulated result carries only this scope's
// deterministic report.
//
// sourceNeedsFetch declines Tier 3 and reports it as not attempted. Tier 2 still
// runs and still fails the run on a resolution error, so declining the render
// narrows what is covered without softening anything that is.
//
// reads is what the Recipe's templates read that a build of one cannot supply.
// It is consulted only once Tier 3 has run and stopped: a stop it declines is
// reported as not attempted, naming those reads and carrying the stop's own
// sentence, and every other stop fails the run as before. Nothing about the
// render itself depends on it.
//
// A run that stops on a failed assertion, at either tier, is reported as a
// warning rather than failing the check, for the reason failedAssertion gives: the
// Component's assertions stop Tier 2, since they run in the steps every run takes
// before rendering, and the Recipe's stop Tier 3. That is asked before reads is,
// so an assertion beside a read a build of one cannot supply is reported as the
// assertion it is.
//
// display is where the two runs display what they raise as they raise it, the
// writer the caller handed the check (WithDisplay); nil shows nothing.
func runRenderTiers(ctx context.Context, res *EntityResult, h entityHarness, lookup resolver.CatalogLookup, recipeLookup renderer.RecipeLookup, subAggregateLoader topology.SubAggregateLoader, suppressUnsupportedFieldWarnings, sourceNeedsFetch bool, reads buildOfOneReads, display io.Writer) error {
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
		ReleasePath:       h.releasePath,
		WorkspaceConfig:   &wsCfg,
		// No workspace means no recorded closure, so nothing was ever going to
		// pin what this harness resolves. Written rather than omitted: the
		// pin-free source is what this probe MEANS, and an omission would be a
		// different fact wearing the same face.
		PluginPins:        pluginpath.NoPins(),
		MappingLibraries:  h.libraries.Mappings,
		WorkloadLibraries: h.libraries.Workloads,
		ArtifactLibraries: h.libraries.Artifacts,
		Display:           display,
	}

	// Tier 2: resolution, guards, IR construction — no render. A missing
	// required input, a guard halt, or a resolution error surfaces here.
	if _, err := pipeline.RunPipeline(ctx, inputs, lookup, recipeLookup, subAggregateLoader, pipeline.Options{SkipRender: true}); err != nil {
		if failedAssertion(err) {
			// A render runs the same steps first, so it would stop on the same
			// assertion: there is nothing past it for Tier 3 to check.
			warnFailedAssertion(res, "Resolution", fmt.Errorf("resolution: %w", err))
			res.Diagnostics = append(res.Diagnostics, "Render: not attempted (the run stopped on that assertion)")
			return nil
		}
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
		if failedAssertion(err) {
			warnFailedAssertion(res, "Render", &renderStopError{err: err})
			return nil
		}
		if reads.declinesRenderStop(err) {
			res.Diagnostics = append(res.Diagnostics,
				"Render: not attempted (this Recipe reads values a build of one cannot supply)",
				"  Validating a Recipe on its own places its Component in a build that wires",
				"  no connection and attaches no Feature, so nothing supplies what its",
				"  templates read under .context.inject, .context.uses or .features. Its",
				"  render stopped, and whether on one of those values or on a fault of its",
				"  own is not judged here. To render it, build a Release that deploys a",
				"  component this Recipe implements.",
				"  Reads: "+strings.Join(reads.paths, ", "))
			res.Diagnostics = append(res.Diagnostics, indentedLines("  The render stopped: ", cerrors.HeadedCauseChain(err))...)
			res.Valid = true
			return nil
		}
		// The isolated render did not complete, and nothing it reads is withheld
		// by the build of one — or the stop is a fault no supplied value changes.
		// The failure is the finding, and its sentence travels in the error.
		res.Diagnostics = append(res.Diagnostics,
			"Render: not completed in isolation",
			"  The render diagnostic below names what stopped it.")
		return &renderStopError{err: err}
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

// indentedLines renders text as report lines: the first opened by lead, each
// further non-blank one indented beneath it. Diagnostics holds one line per entry,
// so a sentence carrying a line break of its own — a provider's message may — is
// split rather than written into one entry.
func indentedLines(lead, text string) []string {
	parts := strings.Split(text, "\n")
	out := []string{lead + parts[0]}
	for _, part := range parts[1:] {
		if strings.TrimSpace(part) == "" {
			continue
		}
		out = append(out, "    "+part)
	}
	return out
}

// renderStopError reports an isolated render that did not complete.
//
// Its text carries every cause beneath the render's own frame on one line, and
// that is the reason it exists rather than a plain wrap. The render's outermost
// frame names only the node it was rendering; the sentence that names what
// stopped it — a template's own failure, a refused reference, a value the
// template could not read — sits several causes further in, and each frame of the
// chain renders as itself alone. A caller that prints this error as one line
// would otherwise report the node and drop the cause. The chain is still
// reachable through Unwrap, so a caller that renders it frame by frame renders it
// exactly as it would the render's own error.
type renderStopError struct {
	err error
}

// Error renders the stopped render as one line, carrying every cause beneath it.
func (e *renderStopError) Error() string {
	return "render: " + cerrors.CauseChain(e.err)
}

// Unwrap returns the render's own error, so errors.Is and errors.As reach every
// frame of it.
func (e *renderStopError) Unwrap() error {
	return e.err
}

// assertionAwaitsRelease is the sentence a failed assertion's warning gains,
// saying what the check leaves to a build. It is written once, beside the one
// decision that reports an assertion as a warning.
const assertionAwaitsRelease = "A Release must satisfy this assertion before a build of it can pass."

// failedAssertion reports whether a run of the check stopped on an assertion that
// called fail — one the Recipe declares, or one the Component it implements
// declares.
//
// It is the one decision about how this check reports a failed assertion, and the
// answer is a warning rather than the check's verdict. The check writes its own
// Release (buildEntityHarness), so nothing a real Release sets reaches the build of
// one, and an assertion over such a value — an image, a prerequisite's presence —
// fails here whether or not the entity is sound. The check cannot tell an
// assertion waiting on a value a Release sets from one that is broken, so it
// reports every failed assertion as a warning and leaves the verdict to a build,
// which always has a Release and halts on the assertion as it always has.
//
// Only a stop fail raises counts. An assertion that stops on a key it reads but
// does not find carries the same condition, and is not counted: that is a fault in
// the assertion's own text, and is decided like any other stop.
func failedAssertion(err error) bool {
	return renderer.AssertionCalledFail(err)
}

// warnFailedAssertion records a run that stopped on a failed assertion: the
// report names the tier that stopped and says why the stop is not the verdict,
// the stop travels in Warnings, and the result is valid.
func warnFailedAssertion(res *EntityResult, tier string, stop error) {
	res.Diagnostics = append(res.Diagnostics,
		tier+": stopped on a failed assertion, reported as a warning",
		"  Validating a Recipe on its own places its Component in a build whose",
		"  Release this check writes itself, so nothing a real Release sets reaches",
		"  it, and an assertion waiting on such a value cannot be told from one that",
		"  is broken. The warning names the assertion and what it requires.")
	res.Warnings = append(res.Warnings, &assertionWarning{stop: stop})
	res.Valid = true
}

// assertionWarning is a failed assertion this check reports as a warning.
//
// Its text is the stop with every cause beneath it on one line, its head carrying
// the short code of the condition the assertion raised as a build's halt carries
// it, closed as a sentence, followed by assertionAwaitsRelease, so every surface
// carrying it says the same thing and its reader can look the condition up. The
// causes are written out because the frame a stop opens with
// may name only the phase that stopped — the Component's assertions — while the
// assertion and the sentence its author wrote sit further in. Unwrap returns the
// stop, so every frame beneath it — the key the assertion is written under, the
// condition it raised — is read through it as through a blocking error.
type assertionWarning struct {
	stop error
}

// Error renders the stop and its causes on one line, then the sentence saying a
// Release must satisfy the assertion.
func (w *assertionWarning) Error() string {
	text := strings.TrimRight(cerrors.HeadedCauseChain(w.stop), " \t\n")
	if !strings.HasSuffix(text, ".") && !strings.HasSuffix(text, "!") && !strings.HasSuffix(text, "?") {
		text += "."
	}
	return text + " " + assertionAwaitsRelease
}

// Unwrap returns the stop the warning reports, so errors.Is and errors.As reach
// every frame of it.
func (w *assertionWarning) Unwrap() error {
	return w.stop
}

// pillarPlaceholders holds the placeholders a build of one fills for the
// Component it places: those for its values, which the synthesized Aggregate
// carries, and those for its infra, which the synthesized Release carries.
type pillarPlaceholders struct {
	values map[string]any
	infra  map[string]any
}

// requiredPlaceholders compiles the Component's schema once and returns, for the
// values pillar and the infra pillar alike, a typed placeholder for each required
// field that carries no default.
//
// The two pillars are filled by one reading for a reason: a required field with
// no default is a value no build can omit, whichever pillar declares it, and a
// build of one has nobody to supply it but this harness. A second reading per
// pillar would be free to fill one pillar's fields differently from the other's.
func requiredPlaceholders(comp *types.Component, libraries map[string]types.SchemaLibrary) (pillarPlaceholders, error) {
	if comp.Spec.Schema.Kind == 0 {
		return pillarPlaceholders{}, nil
	}
	opts := []compactform.CompileOption{}
	if libraries != nil {
		opts = append(opts, compactform.WithSchemaLibraries(libraries))
	}
	compiled, err := compactform.Compile(&comp.Spec.Schema, opts...)
	if err != nil {
		return pillarPlaceholders{}, err
	}
	return pillarPlaceholders{
		values: requiredFieldPlaceholders(compiled, "values"),
		infra:  requiredFieldPlaceholders(compiled, "infra"),
	}, nil
}

// requiredFieldPlaceholders returns a placeholder for each required field of one
// pillar of a compiled schema that carries no default, or nil when the pillar
// declares none. Fields with a default are omitted so the real resolver
// materializes the default; each placeholder is a value of the field's declared
// type that its own declared constraints accept, as placeholderForField states.
func requiredFieldPlaceholders(compiled map[string]any, pillar string) map[string]any {
	pillarNode, ok := mapAt(compiled, "properties", pillar)
	if !ok {
		return nil
	}
	required := toStringSlice(pillarNode["required"])
	props, _ := pillarNode["properties"].(map[string]any)
	if len(required) == 0 || props == nil {
		return nil
	}
	out := map[string]any{}
	for _, name := range required {
		if name == "" {
			continue
		}
		field, _ := props[name].(map[string]any)
		if field == nil {
			out[name] = placeholderString
			continue
		}
		if _, hasDefault := field["default"]; hasDefault {
			continue
		}
		out[name] = placeholderForField(field)
	}
	return out
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
