// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"
	"text/template"
	"text/template/parse"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/internal/sourceref"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcekind"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// DescendRecipeSources walks every Recipe and FeatureRecipe URI in
// the provided Closure, loads the parsed body via recipeLoader,
// evaluates spec.source's templated fields against each using
// component's own infra (plus the Recipe's own locals), classifies
// the source kind, dispatches to sourceResolver, and aggregates the
// pulled artifacts into a deduped slice ready to feed into the
// lockfile writer.
//
// perEntityInfra maps a source-descent entity URI (a Recipe or
// FeatureRecipe) to the per-instance infra of each instance that uses
// it — component instances for a Recipe, feature/secret-attachment
// instances for a FeatureRecipe. The source is resolved once per
// instance, so the same entity used by two instances with different
// infra resolves twice (deduped by identity, distinct when the infra
// genuinely differs). An entity with no using instance — mapped in the
// Manifest but instantiated/attached by nothing in this Release — is
// skipped: its source is never rendered at build, so there is no
// artifact to pin (descending it would either pin a source nothing
// consumes or, for an infra-templated source, halt for want of infra
// that no instance supplies). workspaceRoot and cacheDir are propagated
// into each SourceSpec the resolver consumes (see internal/cacheslot for
// the slot computation that depends on both).
//
// Dedupe is by {Kind, URL, Chart, PinnedVersion} identity; each bucket
// emits one SourceArtifact with all bucket's ReferencedBy records
// appended (sorted by URI, then Component, then Field for determinism).
//
// Returns ErrSourceTemplateAtLockTime wrapped with the template's
// source location on .provides references, ambiguous kind
// classification, or missing .infra/.locals keys; and
// ErrLocalsForwardReference / ErrLocalsUnknownReference on a locals entry
// whose reference into the pillar the block's own author order refuses.
func DescendRecipeSources(
	perEntityInfra map[string][]ComponentSourceInfra,
	closure Closure,
	recipeLoader RecipeLoader,
	sourceResolver SourceResolver,
	workspaceRoot, cacheDir string,
) ([]SourceArtifact, error) {
	return descendSources(closure, recipeLoader, sourceResolver, workspaceRoot, cacheDir,
		func(uri string, _ sourceRoute) []ComponentSourceInfra { return perEntityInfra[uri] })
}

// DescendAuthoredSources resolves, for every Recipe and FeatureRecipe in the
// closure, each source whose author wrote its identity out in full — and no
// other — once per entity, with no Release in reach.
//
// It is the descent an author's own pin step runs. Such a source names one
// artifact whatever instance uses it, so resolving it needs nothing a Release
// supplies: its identity fields carry no template, and the fields that are
// evaluated at all are evaluated against empty values and infra. That is what
// makes it resolvable before any Release exists. A source whose identity is still
// template text is skipped rather than resolved against nothing, because which
// artifact it names is decided by each Release that uses the entity, and so is
// pinned in that Release's lockfile rather than beside the entity.
//
// It runs the same body [DescendRecipeSources] runs, and differs only in which
// instances a source is resolved for: none per Release, one with nothing
// supplied. The routing reading, the spec construction, the resolution and the
// dedupe are therefore one implementation, so a pin the author records and a pin
// a pull records for the same authored source cannot come to be computed two
// ways. Each back-pointer carries no component, because no instance asked for
// the artifact.
func DescendAuthoredSources(
	closure Closure,
	recipeLoader RecipeLoader,
	sourceResolver SourceResolver,
	workspaceRoot, cacheDir string,
) ([]SourceArtifact, error) {
	return descendSources(closure, recipeLoader, sourceResolver, workspaceRoot, cacheDir,
		func(_ string, route sourceRoute) []ComponentSourceInfra {
			if route != routeAuthoredIdentity {
				return nil
			}
			return []ComponentSourceInfra{{}}
		})
}

// descendSources is the one body both descents run. instancesFor names, per
// entity and per the route its source takes, the instances the source is
// resolved for; an entity it names none for is skipped.
func descendSources(
	closure Closure,
	recipeLoader RecipeLoader,
	sourceResolver SourceResolver,
	workspaceRoot, cacheDir string,
	instancesFor func(uri string, route sourceRoute) []ComponentSourceInfra,
) ([]SourceArtifact, error) {
	dedupe := map[string]*SourceArtifact{}
	var order []string

	for _, ent := range closure.Entities {
		// Prefer the kind recorded at collection time (e.g. a Manifest mapping
		// value's position) over re-deriving it lexically from the URI. The
		// hint recognises short-form Recipe references that carry no "/recipe/"
		// path segment; entities collected without a hint fall back to lexical
		// classification.
		entityKind, ok := ent.KindHint, ent.KindHint != ""
		if !ok {
			entityKind, ok = classifyEntityKind(ent.URI)
		}
		if !ok {
			continue
		}

		var (
			source         any
			locals         types.OrderedMap[string]
			loadErr        error
			entityFieldTag = "spec.source"
		)
		switch entityKind {
		case "recipe":
			r, err := recipeLoader.LoadRecipe(ent.URI)
			if err != nil {
				loadErr = err
				break
			}
			source = r.Spec.Source
			locals = r.Spec.Locals
		case "feature_recipe":
			fr, err := recipeLoader.LoadFeatureRecipe(ent.URI)
			if err != nil {
				loadErr = err
				break
			}
			source = fr.Spec.Source
			locals = fr.Spec.Locals
		default:
			continue
		}
		if loadErr != nil {
			return nil, fmt.Errorf("resolve: load %s %q: %w", entityKind, ent.URI, loadErr)
		}
		if source == nil {
			continue
		}
		// What this run owes the source is decided once, here, before anything is
		// acquired — and the decision is carried into the loop below rather than
		// consumed and discarded. A reference the workspace already holds is never
		// pinned, so the descent has nothing to acquire for it and the walk ends
		// here; the reading behind that is the one every pass asking the question
		// shares, which is what keeps the set of references a lockfile pins and the
		// set a render fetches from drifting apart. The two remaining routes differ
		// in whether the artifact this source names is the same one for every
		// instance that uses it, which is what a failure below has to say and what
		// each reference carries onward, so the pass that records pins beside the
		// entity acts on this decision rather than taking it again.
		route := routeForSource(source)
		if route == routeWorkspaceHeld {
			continue
		}

		// Resolve the source once per using-instance, against that instance's
		// own infra — not a flattened Release-wide bag. An entity used by N
		// instances resolves N times; identical resolutions dedupe to one
		// artifact, genuinely-different ones produce distinct artifacts.
		// An entity with no recorded using instance is mapped in the Manifest
		// but instantiated/attached by nothing in this Release; its source is
		// never built, so skip it rather than pin an unconsumed artifact (or
		// halt on an infra-templated source no instance supplies infra for).
		comps := instancesFor(ent.URI, route)
		if len(comps) == 0 {
			continue
		}
		for _, comp := range comps {
			compInfra := comp.Infra
			if compInfra == nil {
				compInfra = map[string]any{}
			}
			compValues := comp.Values
			if compValues == nil {
				compValues = map[string]any{}
			}
			spec, err := buildSourceSpec(source, compValues, compInfra, locals, ent.URI)
			if err != nil {
				return nil, err
			}
			if spec == nil {
				continue
			}
			spec.WorkspaceRoot = workspaceRoot
			spec.CacheDir = cacheDir

			resolved, err := sourceResolver.Resolve(*spec)
			if err != nil {
				return nil, fmt.Errorf("resolve: resolve source for %s %q%s: %w",
					entityKind, ent.URI, route.attribution(comp.Component), err)
			}

			ref := SourceArtifactReference{
				Entity:           entityKind,
				URI:              ent.URI,
				Field:            entityFieldTag,
				Component:        comp.Component,
				IdentityAuthored: route == routeAuthoredIdentity,
			}
			// Every authored reference is recorded, whatever it resolved to. The
			// record is what a later run joins on — to find the artifact from the
			// authored document, and to notice that the reference now resolves
			// elsewhere — and both of those are needed exactly as much when the
			// reference happens to agree with the authored version today. Recording
			// only a disagreement made the set of sources that carry a reference and
			// the set that can be built from one disjoint.
			if spec.Ref != "" {
				ref.RequestedRef = spec.Ref
			}

			key := identityKey(resolved.Kind, resolved.URL, resolved.Chart, resolved.ModuleSubdir, resolved.PinnedVersion)
			sa, exists := dedupe[key]
			if !exists {
				sa = &SourceArtifact{
					Kind:           resolved.Kind,
					URL:            resolved.URL,
					Chart:          resolved.Chart,
					ModuleSubdir:   resolved.ModuleSubdir,
					PinnedVersion:  resolved.PinnedVersion,
					Digest:         resolved.Digest,
					ManifestDigest: resolved.ManifestDigest,
					Mediatype:      resolved.Mediatype,
					ResolvedURL:    resolved.ResolvedURL,
				}
				dedupe[key] = sa
				order = append(order, key)
			}
			sa.ReferencedBy = append(sa.ReferencedBy, ref)
		}
	}

	out := make([]SourceArtifact, 0, len(order))
	for _, k := range order {
		sa := dedupe[k]
		sort.Slice(sa.ReferencedBy, func(i, j int) bool {
			a, b := sa.ReferencedBy[i], sa.ReferencedBy[j]
			if a.URI != b.URI {
				return a.URI < b.URI
			}
			if a.Component != b.Component {
				return a.Component < b.Component
			}
			return a.Field < b.Field
		})
		out = append(out, *sa)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.URL != b.URL {
			return a.URL < b.URL
		}
		if a.Chart != b.Chart {
			return a.Chart < b.Chart
		}
		if a.ModuleSubdir != b.ModuleSubdir {
			return a.ModuleSubdir < b.ModuleSubdir
		}
		return a.PinnedVersion < b.PinnedVersion
	})
	return out, nil
}

// sourceRoute names what a run owes one entity's source: the decision the descent
// takes before it acquires anything, and the one the locality reading alone used
// to make and then discard.
//
// The three answers are not degrees of one another. A reference the workspace
// already holds is acquired by nobody and pinned by nothing. A fetched source
// whose author wrote its identity out in full names one artifact, so a single
// resolution describes every instance that uses it. A fetched source whose
// identity is still template text names an artifact only once an instance's own
// infra has filled that text in, so both the resolution and any failure of it
// belong to that instance rather than to the entity — which is the difference a
// reader of a failure has to be told, because it decides where they look.
type sourceRoute int

const (
	// routeWorkspaceHeld is a source naming something the workspace already holds.
	// Nothing is fetched for it and nothing pins it.
	routeWorkspaceHeld sourceRoute = iota
	// routeAuthoredIdentity is a fetched source whose author wrote out every field
	// that fixes which artifact it names.
	routeAuthoredIdentity
	// routeResolvedIdentity is a fetched source with at least one such field still
	// template text, so which artifact it names is settled per using instance.
	routeResolvedIdentity
)

// routeForSource reads a source's route off the source value itself, through the
// two readings that live in the one leaf both this descent and the render reach.
//
// Locality is asked first and is the only answer that ends the walk. Whether the
// identity is still text is deliberately NOT a second skip: this descent resolves
// a source once per using instance against that instance's own infra, which is
// precisely how a templated identity is made concrete and pinned, so reading that
// fact as a reason to skip would leave unpinned exactly the sources a lockfile
// exists to pin.
//
// What that fact does decide is where else a pin may be recorded. The lockfile
// pins every identity the descent resolves; the lock beside an entity records a
// pin only for a source whose identity its author wrote out in full, because only
// such a source names the same artifact for every Release. Each reference carries
// the route's answer as [SourceArtifactReference.IdentityAuthored], so the one
// reading here is the one that decision rests on.
func routeForSource(source any) sourceRoute {
	switch {
	case sourceref.IsLocal(source):
		return routeWorkspaceHeld
	case sourceref.IdentityTemplated(source):
		return routeResolvedIdentity
	default:
		return routeAuthoredIdentity
	}
}

// attribution names the instance an acquisition failure belongs to, and says
// nothing where the failure belongs to the entity instead.
//
// A source whose identity its author wrote out in full resolves to the same
// artifact for every instance, so naming one of them would report as one
// instance's failure what is every instance's. A source whose identity is filled
// in per instance resolves to whatever that instance's infra says, so the failure
// is that instance's alone and a reader cannot begin looking until they know
// which one it was.
func (r sourceRoute) attribution(instance string) string {
	if r != routeResolvedIdentity || instance == "" {
		return ""
	}
	return fmt.Sprintf(" used by %q, whose own infra fills in which artifact that source names", instance)
}

// EvaluateSourceSpec is the build-time-orchestrator entry point for
// source-template evaluation. It takes the resolving component's own
// per-component, cascade-resolved infra, extracts spec.source +
// spec.locals from the Recipe, and delegates to the same buildSourceSpec
// the lock-time descent uses — so a component's build-time source
// identity matches the identity its lock-time descent pinned, against
// the same per-component infra.
//
// The infra map is the component's BoundaryInfra projection (the same
// per-component bag DescendRecipeSources resolves against at lock time),
// NOT a Release-wide flattened bag. A Recipe backing two components with
// different infra resolves to two identities — one per component — which
// is exactly what the lockfile records.
//
// Returns a SourceSpec carrying the identity triple
// {Kind, URL, Version} plus optional side-knobs (Chart, Ref,
// ModuleSubdir) and OriginalSource. The orchestrator uses
// {Kind, URL, Version} to look up the lockfile's `source_artifacts:`
// entry; mismatch halts with ErrFetchIntegrity.
//
// Returns (nil, nil) when recipe.Spec.Source is nil (sourceless
// Recipe — orchestrator skips resolution). Returns
// ErrSourceTemplateAtLockTime on .provides references, ambiguous
// kind classification, or missing keys, and
// ErrLocalsForwardReference / ErrLocalsUnknownReference on a refused
// locals reference.
func EvaluateSourceSpec(values, infra map[string]any, recipe *types.Recipe) (*SourceSpec, error) {
	if recipe == nil || recipe.Spec.Source == nil {
		return nil, nil
	}
	recipeURI := recipe.Metadata.Name
	if recipe.Metadata.Version != "" {
		recipeURI = recipeURI + ":" + recipe.Metadata.Version
	}
	return EvaluateSourceSpecFields(values, infra, recipe.Spec.Source, recipe.Spec.Locals, recipeURI)
}

// EvaluateSourceSpecFields is the entity-agnostic build-time source-spec evaluator.
// It takes the source fields directly — the source value, the resolving instance's
// per-instance infra, the entity's locals, and its URI — rather than a *types.Recipe,
// so a FeatureRecipe render site resolves its source through the same buildSourceSpec
// the component path uses (one evaluation body, two entities). EvaluateSourceSpec is
// the thin *types.Recipe wrapper over it. Returns (nil, nil) for a nil source.
func EvaluateSourceSpecFields(values, infra map[string]any, source any, locals types.OrderedMap[string], uri string) (*SourceSpec, error) {
	if source == nil {
		return nil, nil
	}
	if values == nil {
		values = map[string]any{}
	}
	if infra == nil {
		infra = map[string]any{}
	}
	return buildSourceSpec(source, values, infra, locals, uri)
}

// buildSourceSpec normalises a Recipe spec.source field (which is
// typed `any` — string URI or structured map) into a SourceSpec
// ready to feed into the SourceResolver. Templated fields
// (`version`, `ref`, `chart`, `module_subdir`) are evaluated against
// {.values, .infra, .locals}, over the locals entries those fields read. Returns
// ErrSourceTemplateAtLockTime on .provides references, ambiguous
// kind classification, or missing keys, and
// ErrLocalsForwardReference / ErrLocalsUnknownReference on a refused
// locals reference.
func buildSourceSpec(source any, values, infra map[string]any, locals types.OrderedMap[string], recipeURI string) (*SourceSpec, error) {
	switch s := source.(type) {
	case string:
		kind, err := classifyKindFromScheme(s, recipeURI)
		if err != nil {
			return nil, err
		}
		return &SourceSpec{Kind: kind, URL: s, OriginalSource: source}, nil
	case map[string]any:
		return buildSpecFromMap(s, values, infra, locals, recipeURI, source)
	default:
		return nil, fmt.Errorf(
			"resolve: %s spec.source has unsupported shape %T: %w "+
				"(refactor to a scalar URI or to the structured form {kind, url, version, ...})",
			recipeURI, source, ErrSourceTemplateAtLockTime())
	}
}

// sourceSpecFields declares, per authored structured-source key, where that key's
// value lands in a SourceSpec and whether it is evaluated as a template on the
// way. kind and url are taken as the author wrote them; the four that select a
// version or a part of a repository are evaluated against {.values, .infra,
// .locals} first.
//
// It is a declared table rather than a switch because the key set has to stay
// enumerable. The same six authored keys are the set a source's identity is read
// out of, in the source-reference leaf this package imports, and the two sites share no
// symbol — a switch gives a guard nothing to compare, so a seventh key taught to
// one side could sit there unread by the other with every test green. This table
// is what that guard reads, on this side.
//
// Walking the declared order rather than the authored map also fixes WHICH of
// several failing fields reports first, which map iteration left to chance.
var sourceSpecFields = [...]struct {
	key       string
	evaluated bool
	assign    func(*SourceSpec, string)
}{
	{"kind", false, func(spec *SourceSpec, v string) { spec.Kind = v }},
	{"url", false, func(spec *SourceSpec, v string) { spec.URL = v }},
	{"version", true, func(spec *SourceSpec, v string) { spec.Version = v }},
	{"chart", true, func(spec *SourceSpec, v string) { spec.Chart = v }},
	{"ref", true, func(spec *SourceSpec, v string) { spec.Ref = v }},
	{"module_subdir", true, func(spec *SourceSpec, v string) { spec.ModuleSubdir = v }},
}

func buildSpecFromMap(s map[string]any, values, infra map[string]any, locals types.OrderedMap[string], recipeURI string, original any) (*SourceSpec, error) {
	spec := &SourceSpec{OriginalSource: original}
	ctx := templateContext{
		Values: values,
		Infra:  infra,
		Locals: map[string]any{},
	}
	authored := mapStringValues(s)
	reads := sourceLocalsReads(authored, locals, recipeURI)
	unevaluated, err := resolveLocals(locals, ctx, recipeURI, reads)
	if err != nil {
		return nil, err
	}

	for _, field := range sourceSpecFields {
		v, present := authored[field.key]
		if !present {
			continue
		}
		if !field.evaluated {
			field.assign(spec, v)
			continue
		}
		fieldPath := recipeURI + ".spec.source." + field.key
		resolved, err := evalSourceTemplate(v, ctx, fieldPath, "")
		if err != nil {
			return nil, withUnevaluatedLocals(err, reads.fieldKeys[fieldPath], unevaluated, recipeURI)
		}
		field.assign(spec, resolved)
	}

	if spec.Kind == "" {
		k, err := classifyKindFromScheme(spec.URL, recipeURI)
		if err != nil {
			return nil, err
		}
		spec.Kind = k
	}
	if !isKnownKind(spec.Kind) {
		return nil, fmt.Errorf(
			"resolve: %s spec.source.kind %q not recognised: %w",
			recipeURI, spec.Kind, ErrSourceTemplateAtLockTime())
	}
	return spec, nil
}

// mapStringValues returns a flat view of the structured source map
// where every value is a string. Non-string values are stringified
// via fmt.Sprint; complex shapes (nested maps, lists) are not
// expected at spec.source's leaf fields.
func mapStringValues(s map[string]any) map[string]string {
	out := make(map[string]string, len(s))
	for k, v := range s {
		if str, ok := v.(string); ok {
			out[k] = str
		} else if v != nil {
			out[k] = fmt.Sprint(v)
		}
	}
	return out
}

// templateContext is the {.values, .infra, .locals} substrate
// source-version templates evaluate against. .provides is
// deliberately absent — referencing .provides at lock time halts
// with ErrSourceTemplateAtLockTime.
type templateContext struct {
	Values map[string]any
	Infra  map[string]any
	Locals map[string]any
}

// evalSourceTemplate evaluates a single source-version template
// against the resolved {.values, .infra, .locals} context. Returns
// ErrSourceTemplateAtLockTime on missing keys, .provides references,
// or template syntax errors.
//
// fieldPath names the source-side location (e.g.,
// "<recipe-uri>.spec.source.version") and is included in error
// messages so operators can locate the failing template. readBy is empty
// for a source field; for a locals entry it is the phrase naming the source
// field the entry is evaluated for, so a failure in the entry says which
// coordinate needed it.
func evalSourceTemplate(input string, ctx templateContext, fieldPath, readBy string) (string, error) {
	if !strings.Contains(input, "{{") {
		return input, nil
	}
	where := fieldPath
	if readBy != "" {
		where += ", " + readBy
	}
	// Aggrega's helpers come from the one place they are defined, so `optional`
	// resolves a key here exactly as it does when the same recipe renders — same
	// presence check, same value, same type — and `index` halts on a map key the
	// map does not hold exactly as it does there. Take the set whole rather than
	// rebuilding it here: a local implementation registered under an authored
	// name Aggrega already defines is what makes two surfaces disagree about a
	// non-string value, and they disagree silently.
	tmpl, err := template.New(fieldPath).Option("missingkey=error").Funcs(gotmpl.Helpers()).Parse(input)
	if err != nil {
		msg := fmt.Sprintf("resolve: parse template at %s: %s (cause: %s)",
			where, errorSentence(ErrSourceTemplateParse()), err)
		// A text the rendering function set parses failed here for a function only a
		// render registers, and the cause names that function without saying why it
		// is missing; the advice says why, and what to do instead.
		if _, renderErr := gotmpl.ParseTree(input, "{{", "}}"); renderErr == nil {
			msg += "; " + renderOnlyFunctionAdvice
			if readBy != "" {
				msg += ", or move the value into a locals entry no source field reads"
			}
		}
		return "", sourceTemplateFault{msg: msg, condition: ErrSourceTemplateParse()}
	}
	if err := rejectProvidesReference(tmpl, where); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	data := map[string]any{
		"values": ctx.Values,
		"infra":  ctx.Infra,
		"locals": ctx.Locals,
	}
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", sourceTemplateExecution{where: where, cause: err}
	}
	return buf.String(), nil
}

// sourceTemplateExecution is a source template that parsed and could not be
// executed against the data this surface holds. It keeps the execution's own
// cause apart from the message, so a field failing for want of a locals entry can
// quote why that entry holds no value.
type sourceTemplateExecution struct {
	where string
	cause error
}

func (e sourceTemplateExecution) Error() string {
	return fmt.Sprintf("resolve: evaluate template at %s: %s (cause: %s)",
		e.where, errorSentence(ErrSourceTemplateAtLockTime()), e.cause)
}

// Unwrap returns the condition every source-template failure carries.
func (e sourceTemplateExecution) Unwrap() error { return ErrSourceTemplateAtLockTime() }

// renderOnlyFunctionAdvice is what a source template calling a function only a
// render registers is told. It names the helpers the surface does register, so
// the reader can see what is available rather than only what is not.
const renderOnlyFunctionAdvice = "a source field, and every locals entry it reads, is evaluated when the " +
	"source is resolved with Aggrega's own helpers only (optional, capitalizedWords and index), not the " +
	"Sprig functions a render adds; write the value without that function"

// sourceTemplateFault is a source-template failure carrying a condition that is
// one case of ErrSourceTemplateAtLockTime.
//
// It is ONE chain, and that is the point of it. The failure has two identities —
// the specific condition and the umbrella every source-template failure carries —
// and the two have to stay matchable with errors.Is. Wrapping both with two %w
// verbs makes an error whose Unwrap returns a slice, which every reader of a
// refusal takes for a join of independent failures: each identity is then printed
// as a finding of its own, by its bare sentence, and the frame carrying the field,
// the entry and the cause is never printed at all. Here the specific condition is
// the one link beneath the message, so the message is what is printed and the
// specific condition's short code heads it, and the umbrella is answered by Is.
type sourceTemplateFault struct {
	msg       string
	condition error
}

func (f sourceTemplateFault) Error() string { return f.msg }

// Unwrap returns the specific condition, the one link beneath the message.
func (f sourceTemplateFault) Unwrap() error { return f.condition }

// Is reports the umbrella condition every source-template failure carries.
func (f sourceTemplateFault) Is(target error) bool {
	return target == ErrSourceTemplateAtLockTime()
}

// errorSentence is the sentence a condition was registered with.
func errorSentence(condition error) string {
	if sentence, ok := cerrors.MessageOf(condition); ok {
		return sentence
	}
	return condition.Error()
}

// withUnevaluatedLocals adds to a source field's failure the reason each locals
// entry it reads holds no value here.
//
// A field reading an entry that could not be evaluated on this surface fails with
// the template's own missing-key text, which names the key and not why it is
// missing. The entry's own failure is the why, so it is quoted beside it. Only an
// execution failure is extended: a field that does not parse, or that reads
// `.provides`, failed for a reason of its own.
func withUnevaluatedLocals(err error, keys []string, unevaluated map[string]error, recipeURI string) error {
	if errors.Is(err, ErrSourceTemplateParse()) || errors.Is(err, ErrSourceTemplateProvidesRef()) {
		return err
	}
	var notes []string
	for _, k := range keys {
		failure, ok := unevaluated[k]
		if !ok {
			continue
		}
		cause := failure.Error()
		var exec sourceTemplateExecution
		if errors.As(failure, &exec) {
			cause = exec.cause.Error()
		}
		notes = append(notes, fmt.Sprintf("it reads %s.spec.locals.%s, which could not be evaluated here (cause: %s)",
			recipeURI, k, cause))
	}
	if len(notes) == 0 {
		return err
	}
	return fmt.Errorf("%w; %s", err, strings.Join(notes, "; "))
}

// rejectProvidesReference walks the parsed template's AST and
// returns ErrSourceTemplateAtLockTime if any pipeline-side action
// references `.provides`. The walk inspects `*parse.FieldNode` and
// `*parse.ChainNode` occurrences inside command pipelines — the
// only places where `.provides` would be a *data reference*, as
// opposed to a literal string that happens to contain the text
// `.provides`.
//
// Walked node types and the contract:
//   - *parse.ActionNode: the {{ ... }} action; its `Pipe` is walked.
//   - *parse.IfNode / *parse.RangeNode / *parse.WithNode: walk Pipe
//     plus the inner BranchNode bodies via List → walkNode recursion.
//   - *parse.TemplateNode: the {{ template "name" pipeline }}
//     sub-template invocation; its `Pipe` argument is walked so a
//     `.provides.X` reference passed as the dot context for a
//     named sub-template is caught.
//   - *parse.PipeNode: walks each Cmd.Args.
//   - *parse.CommandNode: walks each argument.
//   - *parse.FieldNode: a chained field reference like `.provides.X`
//     surfaces as `FieldNode.Ident = ["provides", "X"]` — if the
//     first identifier is "provides", reject.
//   - *parse.ChainNode: a chained access on a pipelined value (e.g.
//     `(foo).provides.X`) — same first-identifier check applies.
//
// String literals containing the text ".provides" never reach
// FieldNode or ChainNode walks; they live in *parse.StringNode as
// constant content, which the walker ignores.
//
// Residual escape hatch: `{{ index . "provides" "X" }}` names
// `.provides.X` but parses as an IdentifierNode + DotNode +
// StringNode chain that the walker does not specifically recognise
// as a `.provides` reference. It still halts, because the data this
// surface evaluates against holds no "provides" key and `index`
// halts on a map key the map does not hold — but it halts naming
// that key rather than with this check's condition. This requires
// deliberate authoring to reach; documented here for completeness.
func rejectProvidesReference(tmpl *template.Template, fieldPath string) error {
	root := tmpl.Tree
	if root == nil || root.Root == nil {
		return nil
	}
	return walkParseNode(root.Root, fieldPath)
}

func walkParseNode(node parse.Node, fieldPath string) error {
	switch n := node.(type) {
	case *parse.ListNode:
		if n == nil {
			return nil
		}
		for _, child := range n.Nodes {
			if err := walkParseNode(child, fieldPath); err != nil {
				return err
			}
		}
	case *parse.ActionNode:
		return walkParseNode(n.Pipe, fieldPath)
	case *parse.IfNode:
		return walkBranchNode(&n.BranchNode, fieldPath)
	case *parse.RangeNode:
		return walkBranchNode(&n.BranchNode, fieldPath)
	case *parse.WithNode:
		return walkBranchNode(&n.BranchNode, fieldPath)
	case *parse.TemplateNode:
		return walkParseNode(n.Pipe, fieldPath)
	case *parse.PipeNode:
		if n == nil {
			return nil
		}
		for _, cmd := range n.Cmds {
			if err := walkParseNode(cmd, fieldPath); err != nil {
				return err
			}
		}
	case *parse.CommandNode:
		for _, arg := range n.Args {
			if err := walkParseNode(arg, fieldPath); err != nil {
				return err
			}
		}
	case *parse.FieldNode:
		if len(n.Ident) > 0 && n.Ident[0] == "provides" {
			return providesError(fieldPath)
		}
	case *parse.ChainNode:
		if len(n.Field) > 0 && n.Field[0] == "provides" {
			return providesError(fieldPath)
		}
		return walkParseNode(n.Node, fieldPath)
	}
	return nil
}

func walkBranchNode(b *parse.BranchNode, fieldPath string) error {
	if err := walkParseNode(b.Pipe, fieldPath); err != nil {
		return err
	}
	if b.List != nil {
		if err := walkParseNode(b.List, fieldPath); err != nil {
			return err
		}
	}
	if b.ElseList != nil {
		return walkParseNode(b.ElseList, fieldPath)
	}
	return nil
}

func providesError(fieldPath string) error {
	return sourceTemplateFault{
		msg: fmt.Sprintf(
			"source-version template at %s references `.provides`, which is an invalid reverse "+
				"reference (locals feed provides, never the reverse) — refactor to read only "+
				"`.values` and `.infra` (and `.locals` over the same): %s",
			fieldPath, errorSentence(ErrSourceTemplateProvidesRef())),
		condition: ErrSourceTemplateProvidesRef(),
	}
}

// resolveLocals evaluates the .locals entries the source fields read against the
// current {Values, Infra} substrate and writes each resolved value back into
// ctx.Locals, in author order, so a later entry may read an earlier one through
// `.locals.<earlier_key>`. It returns, for each such entry that could not be
// executed here, why — see withUnevaluatedLocals for what that is for.
//
// It resolves a block for ONE purpose: so a `source:` coordinate can read
// it. Both entry points into this file's source evaluation reach it — the
// one a lockfile acquisition uses and the one a build uses — so this pass
// answers the same way at both, which is what makes a pinned coordinate
// re-derivable.
//
// # It evaluates what a coordinate reads, and nothing else
//
// Which entries that is comes from reads, which sourceLocalsReads derives from
// the authored text of the source fields and of the entries they reach. An entry
// no source field reads is not looked at: not evaluated, not classified, not
// parsed. Its value cannot reach a coordinate, so evaluating it here could only
// ever halt the resolution over something the coordinate does not depend on — and
// the rendered-output pass, which every such entry exists for, refuses its faults
// at build. Where a field reads the block in a way no static reading resolves to
// named entries, every entry is evaluated, so a value the field may reach is never
// left out.
//
// # It is a second evaluator of an authored locals block, and the split is deliberate
//
// A block that feeds rendered output is resolved by the renderer's own
// pass, which is the one home of that contract for every rendering
// surface. This pass cannot route through it: that package imports this
// one, so the dependency direction leaves no home reachable from both. What
// is shared is therefore everything except the loop — the reference
// classification, the two conditions a refused reference carries, the
// remedy each of them states, and the phrases naming the readable and
// declared key sets all live in internal/gotmpl, which both packages can
// import. What remains duplicated is the walk, held to the renderer's by
// assertions that drive the two against each other.
//
// # The tolerance is bounded by what the block itself decides, and by nothing else
//
// An entry whose template cannot EXECUTE is skipped and the walk continues.
// That covers an entry reading data no source coordinate can see — a
// render-time-only `.context.instance_id` consumed by a values mapping is the
// case it exists for — and the tolerance is real, because such an entry is
// legitimately absent here.
//
// What the tolerance does NOT cover is a reference into the locals pillar that
// the block's own author order refuses: a sibling declared below the entry
// reading it, or a key the block never declares. Both are decidable from the
// declared key order and the authored text alone, with no value and no
// layer-specific datum, so the answer is the same wherever the question is
// asked — which is why it is asked here rather than deferred. The reference is
// classified BEFORE the entry is evaluated, so the refusal does not depend on
// whether the entry would have executed, and it carries the same condition the
// rendered-output pass reports for the same mistake.
//
// The readable set the classification is given is the declared order's PREFIX
// above the entry, not the keys that happened to resolve. That is what keeps
// the tolerance transitive: an entry reading a legitimately-skipped earlier
// sibling is admitted here and then fails to execute, so it is skipped in turn,
// where a readable set narrowed to what resolved would have called a
// correctly-ordered block an ordering fault.
//
// # The remaining difference from the rendered-output pass runs the other way
//
// This surface registers Aggrega's template helpers alone, without the broad
// general-purpose set a rendering surface layers them over, so an authored name
// means one thing wherever a coordinate is evaluated and no function reading the
// clock, the environment, a random source or the network takes part in deciding
// what is pinned. An entry a coordinate reads that calls a function only the
// rendering surface registers therefore fails to PARSE here — and a parse failure
// is outside the tolerance, so it halts, naming the entry and the source field it
// was evaluated for, while the renderer's pass admits it. That difference is a
// property of the surface being resolved, not of the phase: acquiring a lockfile
// and building against one run this same body.
func resolveLocals(locals types.OrderedMap[string], ctx templateContext, recipeURI string, reads localsReads) (map[string]error, error) {
	declared := locals.Keys()
	unevaluated := map[string]error{}
	// The author-order prefix is carried explicitly rather than read back off
	// ctx.Locals, and the difference is the whole of the paragraph above: entries
	// drop out of ctx.Locals legitimately here, and a prefix taken from it would
	// shrink as they did. Keys() and All() walk the one ordered mapping in the one
	// order, so at the entry for the key at index i this holds exactly the keys
	// above it — whether or not the entries above it were evaluated.
	above := 0
	for k, v := range locals.All() {
		readable := declared[:above]
		above++
		readBy, evaluated := reads.readBy(k)
		if !evaluated {
			continue
		}
		fieldPath := recipeURI + ".spec.locals." + k
		if err := refuseLocalsReference(v, fieldPath, k, readable, declared, readBy); err != nil {
			return nil, err
		}
		resolved, err := evalSourceTemplate(v, ctx, fieldPath, readBy)
		if err != nil {
			// What remains of the tolerance is bounded by error CLASS, and the two
			// classes kept loud are the ones no surface can resolve: a template that
			// does not parse, and a `.provides` reverse reference. Everything else
			// is an execute failure and is skipped — see this function's own
			// contract above for what that covers, and why a parse failure landing
			// outside it makes this surface the stricter one on that axis.
			if errors.Is(err, ErrSourceTemplateParse()) || errors.Is(err, ErrSourceTemplateProvidesRef()) {
				return nil, err
			}
			unevaluated[k] = err
			continue
		}
		ctx.Locals[k] = resolved
	}
	return unevaluated, nil
}

// localsReads is which locals entries a source's fields read, found from their
// authored text before anything is evaluated.
type localsReads struct {
	// reader names, per entry a field reads directly or through another entry,
	// the first field in sourceSpecFields order that reaches it.
	reader map[string]string
	// fieldKeys names, per source field path, the entries that field reads
	// directly.
	fieldKeys map[string][]string
	// whole is the first field whose text, or the text of an entry it reaches,
	// reads the block in a way no static reading resolves to named entries. While
	// it is set, every entry is evaluated.
	whole string
}

// readBy reports whether an entry is evaluated for a source coordinate, and the
// phrase naming the field it is evaluated for.
func (r localsReads) readBy(entry string) (string, bool) {
	if field, ok := r.reader[entry]; ok {
		return "read by " + field, true
	}
	if r.whole != "" {
		return "evaluated for " + r.whole + ", which reads the locals block as a whole rather than " +
			"by entry, so every entry is evaluated", true
	}
	return "", false
}

// sourceLocalsReads finds which locals entries the templated source fields read:
// each entry a field names, and each entry such an entry names in turn.
//
// The reading is the one every reference check of a template is built on
// (gotmpl.RootKeysRead), so the entries found here are the entries the same text
// is checked against everywhere else. Where that reading cannot name every entry
// a text may reach, the answer is every entry: leaving one out would hand a field
// a block missing a value it reads.
//
// A text that does not parse contributes nothing here. A field that does not
// parse fails on its own evaluation, and an entry that does not parse is
// evaluated, because something read it, and fails there — in both cases with the
// diagnostic that names it.
func sourceLocalsReads(authored map[string]string, locals types.OrderedMap[string], recipeURI string) localsReads {
	reads := localsReads{reader: map[string]string{}, fieldKeys: map[string][]string{}}
	for _, field := range sourceSpecFields {
		text, present := authored[field.key]
		if !field.evaluated || !present || !strings.Contains(text, "{{") {
			continue
		}
		fieldPath := recipeURI + ".spec.source." + field.key
		keys, complete, err := gotmpl.RootKeysRead(text, "{{", "}}", gotmpl.LocalsContextRoot)
		if err != nil {
			continue
		}
		if !complete {
			if reads.whole == "" {
				reads.whole = fieldPath
			}
			continue
		}
		reads.fieldKeys[fieldPath] = keys
		pending := append([]string(nil), keys...)
		for len(pending) > 0 {
			k := pending[0]
			pending = pending[1:]
			if _, seen := reads.reader[k]; seen {
				continue
			}
			entryText, declared := locals.Get(k)
			if !declared {
				continue
			}
			reads.reader[k] = fieldPath
			next, complete, err := gotmpl.RootKeysRead(entryText, "{{", "}}", gotmpl.LocalsContextRoot)
			if err != nil {
				continue
			}
			if !complete {
				if reads.whole == "" {
					reads.whole = fieldPath
				}
				continue
			}
			pending = append(pending, next...)
		}
	}
	return reads
}

// refuseLocalsReference halts on a locals reference the entry cannot legitimately
// read at the point it is evaluated: a sibling the block declares below it, or a
// key the block does not declare at all.
//
// # The decision is not made here, and neither are the sentences
//
// Which reference offends, which of the two faults it carries, what the author is
// told to do about it, and how the readable and declared key sets are spelled all
// live in internal/gotmpl — the one home each of them has for every surface that
// admits or refuses such a reference. This function composes the DIAGNOSTIC around
// them: the field path the halt is positioned at, and the wrapping this package's
// callers expect. That split is deliberate and is the same split the
// rendered-output pass records at its own composition: the two surfaces cannot
// reach each other, so what they share has to be reachable from both, and what is
// left over is the locator each one carries.
//
// readable is the declared order's prefix above this entry; declared is every key
// the block names. The two sets are what separate the two faults, and the
// separation is the point: a declared-but-not-yet-readable key is an ORDERING
// fault the author fixes by moving the definition above its reader, while a key
// the block never declares is a NAMING fault the author fixes by correcting the
// key. One condition covering both would tell an author who mistyped a key to
// reorder their block.
//
// A malformed template yields no refusal here. A parse failure is the evaluator's
// diagnostic to report, and naming a reference fault for one would describe the
// author's mistake wrongly.
//
// readBy names the source field the entry is evaluated for. It is part of the
// occurrence rather than of the fault: the fault is the block's own, and the field
// is why this surface looked at the entry at all.
func refuseLocalsReference(tmplText, fieldPath, entry string, readable, declared []string, readBy string) error {
	ref := gotmpl.ClassifyRootKeyReference(
		tmplText, "{{", "}}", gotmpl.LocalsContextRoot, readable, declared,
	)
	switch ref.Fault {
	case gotmpl.RootKeyUnresolved:
		return cerrors.Wrap(gotmpl.ErrLocalsForwardReference(), cerrors.CatalogError,
			cerrors.SourceLocation{File: fieldPath},
			cerrors.ComposeMessage(gotmpl.ErrLocalsForwardReference(), withReader([]string{
				fmt.Sprintf("%s references %q", fieldPath, ref.Path),
				gotmpl.LocalsForwardPosition(ref.Key, entry),
				"entries resolve in author order and an entry reads only a sibling defined above it",
				fmt.Sprintf("resolved at this point: %s", gotmpl.LocalsReadablePhrase(readable)),
			}, readBy), gotmpl.LocalsForwardRemedy(ref.Key, entry)))
	case gotmpl.RootKeyUndeclared:
		return cerrors.Wrap(gotmpl.ErrLocalsUnknownReference(), cerrors.CatalogError,
			cerrors.SourceLocation{File: fieldPath},
			cerrors.ComposeMessage(gotmpl.ErrLocalsUnknownReference(), withReader([]string{
				fmt.Sprintf("%s references %q", fieldPath, ref.Path),
				fmt.Sprintf("the locals block declares no %q", ref.Key),
				gotmpl.LocalsDeclaredPhrase(declared),
			}, readBy), gotmpl.LocalsUnknownRemedy))
	default:
		return nil
	}
}

// withReader adds the phrase naming the source field an entry was evaluated for
// to an occurrence's values, after the values describing the fault itself.
func withReader(values []string, readBy string) []string {
	if readBy == "" {
		return values
	}
	return append(values, readBy)
}

// classifyEntityKind reads a catalog URI and returns "recipe" or
// "feature_recipe" if the URI's path segment names a Recipe or
// FeatureRecipe. Returns ("", false) for other catalog entities
// (Components, Features, Aggregates); DescendRecipeSources
// silently skips those.
func classifyEntityKind(uri string) (string, bool) {
	switch {
	case strings.Contains(uri, "/recipe/"):
		return "recipe", true
	case strings.Contains(uri, "/feature-recipe/"), strings.Contains(uri, "/featurerecipe/"):
		return "feature_recipe", true
	default:
		return "", false
	}
}

// classifyKindFromScheme infers the source kind from a scalar URL
// (the legacy authoring shape). Returns ErrSourceTemplateAtLockTime
// for ambiguous shapes (e.g., an https:// URL that could be either
// helm-https or compose-https without further hints).
func classifyKindFromScheme(url, recipeURI string) (string, error) {
	switch {
	case strings.HasPrefix(url, "oci://"):
		return sourcekind.HelmOCIChart, nil
	case strings.HasPrefix(url, "https://") && strings.HasSuffix(strings.ToLower(url), ".tgz"):
		return sourcekind.HelmHTTPSChart, nil
	case strings.HasPrefix(url, "https://") && strings.HasSuffix(strings.ToLower(url), ".yaml"):
		return sourcekind.ComposeHTTPSFragment, nil
	case strings.HasPrefix(url, "https://") && strings.HasSuffix(strings.ToLower(url), ".yml"):
		return sourcekind.ComposeHTTPSFragment, nil
	case strings.HasPrefix(url, "git+ssh://"),
		strings.HasPrefix(url, "git://"),
		strings.HasPrefix(url, "ssh://"),
		strings.HasSuffix(url, ".git"):
		return sourcekind.GitRepository, nil
	default:
		return "", fmt.Errorf(
			"resolve: %s spec.source URL %q is ambiguous — "+
				"refactor to the structured form {kind, url, version, ...} so the kind is explicit: %w",
			recipeURI, url, ErrSourceTemplateAtLockTime())
	}
}

// isKnownKind reports whether kind is one this program serves, read from the one
// declaration of that set ([sourcekind.Served]).
//
// An authored kind no adapter serves is refused as its Recipe or FeatureRecipe is
// read, so a document admitted through a loader never reaches the refusal this
// answers for. It is the lock-time backstop for a source that reached this
// reading without that load — one a caller assembled in memory — and it reports
// under the lock-time umbrella condition rather than the authored-kind rule's,
// which is a check and is raised by the check alone.
func isKnownKind(kind string) bool {
	return sourcekind.Served(kind)
}

// identityKey returns the dedupe key for a SourceArtifact:
// {Kind, URL, Chart, ModuleSubdir, PinnedVersion}. URL is the authored
// identity URL; the two selector terms name whatever the URL alone does not.
// Chart names the chart within a repo root, so two charts served from one repo
// root at the same version key distinctly; moduleSubdir names the directory
// within a git-sourced repository, so two directories of one repository at one
// commit key distinctly. Two artifacts sharing every term merge into a single
// lockfile entry with multiple ReferencedBy records.
//
// The terms are joined on a separator no term can contain, so no combination of
// values can spell another combination's key.
func identityKey(kind, url, chart, moduleSubdir, version string) string {
	return kind + "\x00" + url + "\x00" + chart + "\x00" + moduleSubdir + "\x00" + version
}
