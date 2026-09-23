// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/internal/sourceref"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// EvaluateRecipeSource renders Go templates inside a Recipe's source field with
// the same self-contained context as Recipe.Spec.Locals — .context.instance_id /
// .values / .infra / .locals, no .context.inject / .context.uses and no .provides.
//
// Which parts of a source carry a template is decided by
// mapRecipeSourceTemplates rather than here, because the sensitivity reading asks
// the same question of the same value and the two answers must be one answer.
//
// Self-containment holds by sharing LocalsContext with EvaluateRecipeLocals: the
// context this surface renders against defines no cross-node pillar, so a
// .context.inject or .context.uses reference halts before the template executes.
// Sharing the type AND the scope also means the two surfaces admit and refuse
// exactly the same references — including .provides, which the type does not
// define, and .attachment_name, which the scope refuses because a Recipe's source
// resolves for a node rather than for an attachment.
//
// locals is the node's RESOLVED locals. A source template is evaluated after the
// whole block has resolved, so it reads every local rather than a prefix; a
// reference to a key the block does not declare halts with
// ErrLocalsUnknownReference instead of resolving to an empty string, which is the
// same refusal a locals entry gets for the same mistake. Passing the resolved map
// rather than resolving it here keeps one evaluation behind every reader of a
// node's locals.
func EvaluateRecipeSource(
	source any,
	nodeID string,
	values types.OrderedMap[any],
	infra types.OrderedMap[any],
	locals types.OrderedMap[string],
) (any, error) {
	ctx := LocalsContext{
		Context: ProvidesContextBlock{InstanceID: encodeNodeIdentity(nodeID)},
		Values:  values,
		Infra:   infra,
		Locals:  locals,
	}
	surface := sourceTemplateSurface{
		scope:  recipeLocalsScope.with(ctx),
		locals: locals.Keys(),
		kind:   "Recipe",
	}

	return mapRecipeSourceTemplates(source, func(field, tmplText string) (string, error) {
		return renderSourceString(tmplText, surface, sourceFieldLabel(field))
	})
}

// EvaluateFeatureRecipeSource renders Go templates inside a FeatureRecipe's
// source field, on the same terms a Recipe's source is evaluated: the template is
// evaluated, and what it produced is what the render then resolves.
//
// The reference set is the one the attachment's OWN locals were resolved against
// — featureRecipeLocalsScope — rather than the base Recipe's, and the difference
// is not cosmetic. An attachment IS being resolved here, so .attachment_name names
// it and is admitted, exactly as it is admitted on the locals entries of the same
// entity; .context.rendered_name stays narrowed, for the reason the source surface
// narrows it everywhere — on this surface a source template is the very thing being
// evaluated to decide what the source is, so whether a name of that kind exists is
// not a settled fact.
//
// instanceID and instanceName are what EvaluateFeatureRecipeLocals is given at the
// same site, passed through unchanged: .context.instance_id is the host component
// the attachment resolves for and .attachment_name the instance filling the slot,
// and one entity's own templates have to publish one denotation of each. Deriving
// either here instead would give the same author two answers for one reference.
//
// locals is the resolved locals set this attachment's other templates read, which
// at component scope is the FeatureRecipe's own block merged over the host
// Recipe's. A reference to a key that set does not carry halts with the same
// unknown-reference refusal a locals entry gets for the same mistake, rather than
// rendering the key away.
func EvaluateFeatureRecipeSource(
	source any,
	instanceName, instanceID string,
	values types.OrderedMap[any],
	infra types.OrderedMap[any],
	locals types.OrderedMap[string],
) (any, error) {
	ctx := LocalsContext{
		Context:        ProvidesContextBlock{InstanceID: instanceID},
		Values:         values,
		Infra:          infra,
		Locals:         locals,
		AttachmentName: instanceName,
	}
	surface := sourceTemplateSurface{
		scope:  featureRecipeLocalsScope.with(ctx),
		locals: locals.Keys(),
		kind:   "FeatureRecipe",
	}

	return mapRecipeSourceTemplates(source, func(field, tmplText string) (string, error) {
		return renderSourceString(tmplText, surface, sourceFieldLabel(field))
	})
}

// EvaluateBoundaryScopeFeatureRecipeSource renders Go templates inside a
// boundary-scope FeatureRecipe's source field.
//
// It is a second binding of the same surface rather than a second evaluator: the
// shape classification, the templated reading, the reference check and the
// wrapping are all the ones above. What differs is the context TYPE and therefore
// the reference set — a boundary-scope FeatureRecipe renders once for a boundary
// rather than once per component, so it reads .boundary.* pillars the node-local
// type does not carry and cannot read the per-component ones. That is the same
// context-shape difference the boundary-scope locals evaluation already carries,
// and this surface is checked against the same scope it is, so an author writing
// `source:` and `locals:` in one boundary-scope FeatureRecipe is admitted and
// refused the same references on both.
func EvaluateBoundaryScopeFeatureRecipeSource(source any, frCtx *types.TemplateContext) (any, error) {
	var locals []string
	if frCtx != nil {
		locals = frCtx.Locals.Keys()
	}
	surface := sourceTemplateSurface{
		scope:  boundaryScopeFRScope.with(frCtx),
		locals: locals,
		kind:   "FeatureRecipe",
	}

	return mapRecipeSourceTemplates(source, func(field, tmplText string) (string, error) {
		return renderSourceString(tmplText, surface, sourceFieldLabel(field))
	})
}

// anchorSourceToDocument resolves what a source template PRODUCED against the
// document that declared it, and is the one home of that step for every entity
// whose source is template-evaluated.
//
// A source written as a template names nothing until it has been evaluated, so the
// directory it is relative to belongs to the value it has just produced. The load
// anchors every other source and deliberately leaves a templated one alone, and
// both run the same resolution — so what a relative source is resolved against is
// one answer given at two moments rather than two answers.
//
// The produced value decides its own treatment: an absolute path, a transport
// reference and an empty result each pass through, which is precisely what
// anchoring the template's text could not do, because all three are settled by text
// a join has already prefixed by the time they exist.
//
// Whether the anchoring is owed is a property of what the AUTHOR wrote, which is
// why authored and produced are both read here: they are the same value either side
// of one step that rewrites it, and asking the produced value whether it carries a
// template would answer about the result of the evaluation rather than about
// whether there was one to run.
func anchorSourceToDocument(authored, produced any, documentPath string) (any, error) {
	authoredText, authoredScalar := authored.(string)
	producedText, producedScalar := produced.(string)
	if !authoredScalar || !producedScalar || !sourceref.IsTemplated(authoredText) {
		return produced, nil
	}
	return sourceref.AnchorToDocument(documentPath, producedText)
}

// sourceTemplateSurface names the entity surface one source template is evaluated
// on: the reference set it is checked against already carrying the data it renders
// against, the resolved locals a .locals reference is checked over, and the entity
// kind a diagnostic quotes.
//
// It exists so the entity kinds whose source is template-evaluated share one
// evaluation rather than one apiece. The shape classification is already shared —
// mapRecipeSourceTemplates is its one home, for the reason stated there — and this
// is the other half: a second rendering body per kind would agree on the day it was
// written and would be free to diverge on the templated reading, the reference
// check or the diagnostic wrapping, none of which is kind-specific.
//
// scope arrives already bound to its value through templateScope.with, so a caller
// cannot pair a surface's declared narrowing with data of another shape.
type sourceTemplateSurface struct {
	scope  templateScope
	locals []string
	kind   string
}

// templateName is the name a parse or execution failure on this surface reports.
// It is derived from the kind rather than carried beside it, so a surface cannot
// name one entity kind in its diagnostic and another in its template name.
func (s sourceTemplateSurface) templateName(label string) string {
	return fmt.Sprintf("%s-source[%s]", strings.ToLower(s.kind), label)
}

// where is the phrase a diagnostic on this surface quotes to locate the template.
func (s sourceTemplateSurface) where(label string) string {
	return fmt.Sprintf("%s source %q", s.kind, label)
}

// mapRecipeSourceTemplates is the ONE place a Recipe source's shape is
// classified: which of its parts carry a template, what each of those parts is
// named, and what the rebuilt value looks like once each has been replaced.
// It walks the source, calls fn for every template-bearing part, and returns the
// source rebuilt from fn's results with every other part carried verbatim.
//
// It exists because two independent readers need that classification and must not
// each carry their own copy of it. The evaluator replaces each template with its
// rendered text; the sensitivity reading replaces nothing and only records which
// parts read a declared-sensitive datum. Two switches over one shape union would
// agree until someone taught the evaluator a third shape, at which point the
// sensitivity reading would silently stop covering it and a computed secret would
// print in the clear. Routing both through this walk makes that divergence
// impossible rather than merely unlikely: a new shape is taught here, once, and
// both readers gain it together.
//
// The shapes, and why each is what it is:
//
//   - nil: a sourceless Recipe. Nothing to walk.
//   - string: a single URI or version reference, evaluated whole. It has no field
//     name, so the field passed to fn is redact.WholeSourceKey — the same
//     spelling the display uses for it, taken from the display's own declaration
//     so the walk and the display cannot disagree about it.
//   - map[string]any: a structured source. Only the string-valued fields are
//     evaluated as templates; every other field travels verbatim, which is why
//     fn is not called for it.
//   - anything else: a shape that reached here already typed. No template is
//     evaluated against it, so it is returned untouched.
//
// That last arm is deliberately forward-compatible — an unfamiliar shape
// survives the walk rather than being mangled by it — and the cost of that
// is precision. A structured source that arrived through an order-preserving
// decode is an ordered container rather than a map[string]any, so it lands in
// the passthrough instead of the map arm and its string fields go unevaluated:
// no error, no diagnostic, just a template that was never rendered. The source
// field is declared as a bare any and decodes through the standard path today,
// which is what keeps the map arm the one that fires. Declaring it as an
// order-preserving container instead replaces the passthrough with an explicit
// arm for that shape, in the same change.
//
// Other places in the compiler also switch on this union, and they are
// deliberately NOT folded in here, because they ask a different question of the
// same value: whether a source is declared at all, whether its reference is
// local, whether the fields that fix WHICH artifact it names are still text, and
// which kind and URI it names at lock time.
//
// Those questions share a walk of their own, in internal/sourceref, and the
// split between the two walks is the carve-out above rather than a lapse from it.
// What is shared there is one projection — the reference a source names, read out
// of either shape — and each site composes its own question on top of it: the
// catalog load asks where a relative reference is anchored, this package's
// resolution asks what path a local one names, and the lock-time descent asks
// whether the author already knows which artifact it is. None of them asks which
// PARTS carry a template or what each part is called, which is the only question
// this walk answers and the reason the two cannot be merged: that leaf is
// stdlib-only, which is what lets the catalog load and the dependency descent
// reach it at all, and neither can import this package. A reader tempted to unify
// them would be folding a per-field question into a per-source one and would get
// a classifier answering neither cleanly.
//
// The two walks do cover the same two-shape union, and each says so where it is
// declared, so a shape taught to one is a shape the other is missing rather than
// a difference nobody notices.
//
// fn's error is returned unwrapped and stops the walk. Map iteration order is
// Go's own and therefore unspecified; the walk's result is an unordered map, so
// no displayed or rendered byte depends on it. The only order-sensitive outcome
// is WHICH of several failing fields reports first, which is the behaviour this
// walk inherited and deliberately does not change.
func mapRecipeSourceTemplates(source any, fn func(field, tmplText string) (string, error)) (any, error) {
	switch s := source.(type) {
	case nil:
		return nil, nil
	case string:
		return fn(redact.WholeSourceKey, s)
	case map[string]any:
		out := make(map[string]any, len(s))
		for k, v := range s {
			str, ok := v.(string)
			if !ok {
				out[k] = v
				continue
			}
			replaced, err := fn(k, str)
			if err != nil {
				return nil, err
			}
			out[k] = replaced
		}
		return out, nil
	default:
		return source, nil
	}
}

// sourceFieldLabel names one template-bearing part of a source for a diagnostic.
// The unnamed whole-source template is labelled by the block it is; a structured
// field is labelled by its own name beneath it.
func sourceFieldLabel(field string) string {
	if field == redact.WholeSourceKey {
		return "source"
	}
	return "source." + field
}

// renderSourceString evaluates a single string field as a template on the surface
// the entity being rendered declares. Empty strings and strings without template
// actions short-circuit (no parsing cost).
//
// The locals reference check runs against the whole resolved block, so a source
// template naming a local the block does not declare halts rather than rendering
// the key away. There is no forward class on any of these surfaces: every local is
// resolved before a source template runs, which is why the resolved set is passed
// as both arguments.
//
// Each surface brings its own scope rather than this body choosing one, and the
// scopes are the ones the SAME entity's locals entries are checked against — a base
// Recipe's, which refuses .attachment_name because a Recipe's source resolves for a
// node rather than for an attachment; a FeatureRecipe's, which admits it because an
// attachment is exactly what is being resolved; and the boundary-scope one, whose
// context type carries the boundary pillars instead of the per-component ones.
func renderSourceString(tmplText string, surface sourceTemplateSurface, label string) (string, error) {
	if tmplText == "" {
		return "", nil
	}
	// The same reading that decides, at load, whether this field's anchoring is
	// owed to the value rather than to the text. Sharing it is what keeps the two
	// from disagreeing about which references are still text: a field this skips
	// is exactly a field the load already resolved.
	if !sourceref.IsTemplated(tmplText) {
		return tmplText, nil
	}
	site := localsRefSite{Where: surface.where(label), File: label}
	if err := checkLocalsReferenceOrder(tmplText, site, surface.locals, surface.locals); err != nil {
		return "", err
	}
	out, err := evalValueTemplate(surface.templateName(label), tmplText, surface.scope, "{{", "}}")
	if err != nil {
		return "", cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: label}, surface.where(label))
	}
	return strings.TrimSpace(out), nil
}
