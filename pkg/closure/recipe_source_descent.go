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
		// A reference the workspace already holds is never pinned, so the descent
		// has nothing to acquire for it. The reading is the one every pass that
		// asks this question shares, which is what keeps the set of references a
		// lockfile pins and the set a render fetches from drifting apart.
		if sourceref.IsLocal(source) {
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
		comps := perEntityInfra[ent.URI]
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
				return nil, fmt.Errorf("resolve: resolve source for %s %q: %w", entityKind, ent.URI, err)
			}

			ref := SourceArtifactReference{
				Entity:    entityKind,
				URI:       ent.URI,
				Field:     entityFieldTag,
				Component: comp.Component,
			}
			if resolved.PinnedVersion != spec.Version && spec.Ref != "" {
				ref.RequestedRef = spec.Ref
			}

			key := identityKey(resolved.Kind, resolved.URL, resolved.Chart, resolved.PinnedVersion)
			sa, exists := dedupe[key]
			if !exists {
				sa = &SourceArtifact{
					Kind:           resolved.Kind,
					URL:            resolved.URL,
					Chart:          resolved.Chart,
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
		return a.PinnedVersion < b.PinnedVersion
	})
	return out, nil
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
// {.values, .infra, .locals} per amendment §3.7. Returns
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

func buildSpecFromMap(s map[string]any, values, infra map[string]any, locals types.OrderedMap[string], recipeURI string, original any) (*SourceSpec, error) {
	spec := &SourceSpec{OriginalSource: original}
	ctx := templateContext{
		Values: values,
		Infra:  infra,
		Locals: map[string]any{},
	}
	if err := resolveLocals(locals, ctx, recipeURI); err != nil {
		return nil, err
	}

	for k, v := range mapStringValues(s) {
		switch k {
		case "kind":
			spec.Kind = v
		case "url":
			spec.URL = v
		case "version":
			resolved, err := evalSourceTemplate(v, ctx, recipeURI+".spec.source.version")
			if err != nil {
				return nil, err
			}
			spec.Version = resolved
		case "chart":
			resolved, err := evalSourceTemplate(v, ctx, recipeURI+".spec.source.chart")
			if err != nil {
				return nil, err
			}
			spec.Chart = resolved
		case "ref":
			resolved, err := evalSourceTemplate(v, ctx, recipeURI+".spec.source.ref")
			if err != nil {
				return nil, err
			}
			spec.Ref = resolved
		case "module_subdir":
			resolved, err := evalSourceTemplate(v, ctx, recipeURI+".spec.source.module_subdir")
			if err != nil {
				return nil, err
			}
			spec.ModuleSubdir = resolved
		}
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
// expected at spec.source's leaf fields per the source-resolution
// amendment.
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
// messages so operators can locate the failing template.
func evalSourceTemplate(input string, ctx templateContext, fieldPath string) (string, error) {
	if !strings.Contains(input, "{{") {
		return input, nil
	}
	// Aggrega's helpers come from the one place they are defined, so `optional`
	// resolves a key here exactly as it does when the same recipe renders — same
	// presence check, same value, same type. Take the set whole rather than
	// rebuilding it here: a local implementation registered under an authored
	// name Aggrega already defines is what makes two surfaces disagree about a
	// non-string value, and they disagree silently.
	tmpl, err := template.New(fieldPath).Option("missingkey=error").Funcs(gotmpl.Helpers()).Parse(input)
	if err != nil {
		return "", fmt.Errorf("resolve: parse template at %s: %w (also %w) (cause: %s)",
			fieldPath, ErrSourceTemplateAtLockTime(), ErrSourceTemplateParse(), err)
	}
	if err := rejectProvidesReference(tmpl, fieldPath); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	data := map[string]any{
		"values": ctx.Values,
		"infra":  ctx.Infra,
		"locals": ctx.Locals,
	}
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("resolve: evaluate template at %s: %w (cause: %s)",
			fieldPath, ErrSourceTemplateAtLockTime(), err)
	}
	return buf.String(), nil
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
// Residual escape hatch: `{{ index . "provides" "X" }}` evaluates
// to `.provides.X` at runtime but parses as an IdentifierNode +
// DotNode + StringNode chain that the walker does not specifically
// recognise as a `.provides` reference. This requires deliberate
// authoring to bypass; documented here for completeness, not
// closed.
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
	return fmt.Errorf(
		"source-version template at %s references `.provides`, which is an invalid reverse "+
			"reference (locals feed provides, never the reverse) — refactor to read only "+
			"`.values` and `.infra` (and `.locals` over the same): %w (also %w)",
		fieldPath, ErrSourceTemplateAtLockTime(), ErrSourceTemplateProvidesRef())
}

// resolveLocals evaluates each .locals entry against the current
// {Values, Infra} substrate and writes the resolved value back into
// ctx.Locals, in author order, so a later entry may read an earlier one
// through `.locals.<earlier_key>`.
//
// It resolves a block for ONE purpose: so a `source:` coordinate can read
// it. Both entry points into this file's source evaluation reach it — the
// one a lockfile acquisition uses and the one a build uses — so this pass
// answers the same way at both, which is what makes a pinned coordinate
// re-derivable.
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
// means one thing wherever a coordinate is evaluated. An entry calling a
// function only the rendering surface registers therefore fails to PARSE here —
// and a parse failure is outside the tolerance, so it halts while the
// renderer's pass admits it. That difference is a property of the surface being
// resolved, not of the phase: acquiring a lockfile and building against one run
// this same body.
func resolveLocals(locals types.OrderedMap[string], ctx templateContext, recipeURI string) error {
	declared := locals.Keys()
	// The author-order prefix is carried explicitly rather than read back off
	// ctx.Locals, and the difference is the whole of the paragraph above: entries
	// drop out of ctx.Locals legitimately here, and a prefix taken from it would
	// shrink as they did. Keys() and All() walk the one ordered mapping in the one
	// order, so at the entry for the key at index i this holds exactly the keys
	// above it.
	above := 0
	for k, v := range locals.All() {
		fieldPath := recipeURI + ".spec.locals." + k
		if err := refuseLocalsReference(v, fieldPath, k, declared[:above], declared); err != nil {
			return err
		}
		above++
		resolved, err := evalSourceTemplate(v, ctx, fieldPath)
		if err != nil {
			// What remains of the tolerance is bounded by error CLASS, and the two
			// classes kept loud are the ones no surface can resolve: a template that
			// does not parse, and a `.provides` reverse reference. Everything else
			// is an execute failure and is skipped — see this function's own
			// contract above for what that covers, and why a parse failure landing
			// outside it makes this surface the stricter one on that axis.
			if errors.Is(err, ErrSourceTemplateParse()) || errors.Is(err, ErrSourceTemplateProvidesRef()) {
				return err
			}
			continue
		}
		ctx.Locals[k] = resolved
	}
	return nil
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
func refuseLocalsReference(tmplText, fieldPath, entry string, readable, declared []string) error {
	ref := gotmpl.ClassifyRootKeyReference(
		tmplText, "{{", "}}", gotmpl.LocalsContextRoot, readable, declared,
	)
	switch ref.Fault {
	case gotmpl.RootKeyUnresolved:
		return cerrors.Wrap(gotmpl.ErrLocalsForwardReference(), cerrors.CatalogError,
			cerrors.SourceLocation{File: fieldPath},
			cerrors.ComposeMessage(gotmpl.ErrLocalsForwardReference(), []string{
				fmt.Sprintf("%s references %q", fieldPath, ref.Path),
				gotmpl.LocalsForwardPosition(ref.Key, entry),
				"entries resolve in author order and an entry reads only a sibling defined above it",
				fmt.Sprintf("resolved at this point: %s", gotmpl.LocalsReadablePhrase(readable)),
			}, gotmpl.LocalsForwardRemedy(ref.Key, entry)))
	case gotmpl.RootKeyUndeclared:
		return cerrors.Wrap(gotmpl.ErrLocalsUnknownReference(), cerrors.CatalogError,
			cerrors.SourceLocation{File: fieldPath},
			cerrors.ComposeMessage(gotmpl.ErrLocalsUnknownReference(), []string{
				fmt.Sprintf("%s references %q", fieldPath, ref.Path),
				fmt.Sprintf("the locals block declares no %q", ref.Key),
				gotmpl.LocalsDeclaredPhrase(declared),
			}, gotmpl.LocalsUnknownRemedy))
	default:
		return nil
	}
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
		return "helm-oci-chart", nil
	case strings.HasPrefix(url, "https://") && strings.HasSuffix(strings.ToLower(url), ".tgz"):
		return "helm-https-chart", nil
	case strings.HasPrefix(url, "https://") && strings.HasSuffix(strings.ToLower(url), ".yaml"):
		return "compose-https-fragment", nil
	case strings.HasPrefix(url, "https://") && strings.HasSuffix(strings.ToLower(url), ".yml"):
		return "compose-https-fragment", nil
	case strings.HasPrefix(url, "git+ssh://"),
		strings.HasPrefix(url, "git://"),
		strings.HasPrefix(url, "ssh://"),
		strings.HasSuffix(url, ".git"):
		return "terraform-module-git", nil
	default:
		return "", fmt.Errorf(
			"resolve: %s spec.source URL %q is ambiguous — "+
				"refactor to the structured form {kind, url, version, ...} so the kind is explicit: %w",
			recipeURI, url, ErrSourceTemplateAtLockTime())
	}
}

// isKnownKind returns true when kind names one of the five
// canonical source-kind values (four shipped + one reserved).
func isKnownKind(kind string) bool {
	switch kind {
	case "helm-oci-chart", "helm-https-chart", "compose-https-fragment",
		"terraform-module-git", "terraform-module-oci":
		return true
	default:
		return false
	}
}

// identityKey returns the dedupe key for a SourceArtifact: the
// quad {Kind, URL, Chart, PinnedVersion}. URL is the authored identity
// URL and Chart names the chart within a repo root, so two charts served
// from one repo root at the same version key distinctly. Two artifacts
// sharing the quad merge into a single lockfile entry with multiple
// ReferencedBy records.
func identityKey(kind, url, chart, version string) string {
	return kind + "\x00" + url + "\x00" + chart + "\x00" + version
}
