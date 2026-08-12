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
// classification, or missing .infra/.locals keys.
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
		if isLocalSource(source) {
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
// kind classification, or missing keys.
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
// kind classification, or missing keys.
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
// ctx.Locals. Locals are evaluated in OrderedMap iteration order,
// mirroring Phase B's preamble semantics. Later .locals entries may
// reference earlier .locals via `.locals.<earlier_key>`; the
// closure's same-pass-as-source semantics align with build-time
// expectations.
func resolveLocals(locals types.OrderedMap[string], ctx templateContext, recipeURI string) error {
	for k, v := range locals.All() {
		resolved, err := evalSourceTemplate(v, ctx, recipeURI+".spec.locals."+k)
		if err != nil {
			// Precise tolerance. A malformed template (parse) or a `.provides`
			// reverse reference is invalid at any phase — keep it loud. An
			// execute/missingkey failure means this local references data not
			// available at the per-Release closure (e.g. a render-time-only
			// `.context.instance_id` consumed by valuesMapping); it is not a
			// lock-time concern, so skip it and continue. The source template's
			// own evaluation remains the gate — if `source:` references this
			// now-absent local, that eval fails with ErrSourceTemplateAtLockTime
			// naming the source field.
			if errors.Is(err, ErrSourceTemplateParse()) || errors.Is(err, ErrSourceTemplateProvidesRef()) {
				return err
			}
			continue
		}
		ctx.Locals[k] = resolved
	}
	return nil
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

// isLocalSource returns true when source is a scalar URI that
// names a Tier-1 (filesystem-relative) or Tier-2 (workspace-
// catalog-relative) reference. Local sources are never lockfile-
// pinned per amendment §3.12; the closure walker skips them.
//
// In the current implementation heuristic is: scalar string sources
// that don't carry a recognised remote-scheme prefix
// (oci://, https://, git+ssh://, etc.) are treated as local.
// Structured-form sources always carry an explicit `url:` field
// whose scheme determines remote-vs-local; structured-form with a
// local-shaped url (file://, ./, ../, leading /) is also classified
// local here.
func isLocalSource(source any) bool {
	switch s := source.(type) {
	case string:
		return isLocalURL(s)
	case map[string]any:
		urlVal, _ := s["url"].(string)
		return isLocalURL(urlVal)
	default:
		return false
	}
}

func isLocalURL(s string) bool {
	if s == "" {
		return false
	}
	switch {
	case strings.HasPrefix(s, "oci://"),
		strings.HasPrefix(s, "https://"),
		strings.HasPrefix(s, "http://"),
		strings.HasPrefix(s, "git+ssh://"),
		strings.HasPrefix(s, "git://"),
		strings.HasPrefix(s, "ssh://"),
		strings.HasSuffix(s, ".git"):
		return false
	default:
		return true
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
