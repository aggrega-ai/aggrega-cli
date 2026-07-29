// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// LocalsContext is the restricted context available during Recipe .locals
// evaluation: node-local data only — the node's own identity, its values and infra
// pillars, and the locals resolved so far.
//
// A local may read an EARLIER sibling and receives its resolved value. The pillar
// carries a PREFIX of the block, not the whole of it: it is published inside the
// evaluation loop, so at the entry for key k it holds exactly the entries above k
// in author order. A reference to a later sibling, or to a key the block does not
// declare, halts before the template executes rather than rendering empty — the
// pillar is an open keyed map, so an unknown key under it would otherwise resolve
// through the missing-key policy to an empty string. Author order is the contract;
// the block is never sorted or reordered to make a forward reference resolve.
//
// The type declares no provides pillar, and the absence is an ordering property
// rather than an omission. A node's locals resolve BEFORE its provides so that a
// provides template may read .locals; the node's own provides therefore do not
// exist yet when a local is evaluated, and a local that reaches for .provides
// halts before the template executes rather than rendering empty. A local that
// needs a value the node's provides also publish computes that value from .values
// and .infra directly — the same expression the provides entry evaluates.
//
// Self-containment is a property of this type rather than of a check over template
// text. Context is a ProvidesContextBlock, which defines instance_id and nothing
// else, so .context.inject and .context.uses name keys this surface's context does
// not define and the reference halts before the template executes. Cross-node data
// is therefore unreachable from a local by construction, which is what keeps locals
// resolvable without ordering the graph first.
//
// The Recipe source surface shares this type, and shares it with one difference in
// what the locals pillar carries rather than in what the type admits: a source
// template is evaluated after the whole block has resolved, so it reads every
// local. The reference check is the same one at both, so a source template naming a
// key the block does not declare halts exactly as a locals entry would.
type LocalsContext struct {
	Context ProvidesContextBlock  `json:"context"`
	Values  types.OrderedMap[any] `json:"values"`
	Infra   types.OrderedMap[any] `json:"infra"`
	// Locals carries the locals resolved at the point this context is built —
	// the entries above the current one for a locals entry, the whole block for
	// the source surface. It is populated inside the evaluation loop rather than
	// once beside it, which is what makes a sibling read see a resolved value.
	Locals types.OrderedMap[string] `json:"locals"`
	// AttachmentName is the attachment instance name, populated for a
	// FeatureRecipe's own locals so an attachment local may name the instance it
	// is resolving for. Empty for a base Recipe's locals and for the Recipe
	// source surface, whose scope declares this root a narrowing of this type (a
	// base local or a source template referencing the attachment name halts
	// rather than rendering empty).
	//
	// It is the route to the instance name from a local, and it is the only one:
	// .context.instance_id is the HOST COMPONENT the attachment is resolving
	// for, on this surface exactly as on every other, so the two names answer
	// two different questions and neither stands in for the other.
	//
	// The template key mirrors the field name, and every scope that exposes the
	// attachment name — this one, the provides scope and the render scope —
	// publishes it under that same key, so a template author reads one name at
	// all three.
	AttachmentName string `json:"attachment_name,omitempty"`
}

// recipeLocalsScope is the reference set a base Recipe's locals entry is checked
// against. The universe is LocalsContext — the type the entry is evaluated against
// — so the roots the check admits are derived from that type rather than
// enumerated here.
//
// The surface declares one narrowing, and it is a deliberately kept exclusion:
// attachment_name. No attachment is being resolved during a base Recipe's locals
// pass, so the name would render empty. The type carries the field because a
// FeatureRecipe's own locals populate it, and this surface refuses it because its
// own pass cannot supply it.
//
// The Recipe source surface shares this scope rather than declaring a second one
// with the same content. Both are the base Recipe's node-local surface, evaluated
// against the same type with the same reason for the same exclusion, so one
// declaration is what keeps them from drifting into disagreement.
var recipeLocalsScope = templateScope{
	data:    LocalsContext{},
	exclude: []string{"attachment_name"},
}

// featureRecipeLocalsScope is the reference set a FeatureRecipe's own locals entry
// is checked against: the same context type with no narrowing. An attachment IS
// being resolved during that pass, so .attachment_name names it.
var featureRecipeLocalsScope = templateScope{
	data: LocalsContext{},
}

// localsContextRoot is the context root a template reads a resolved local through.
// Every surface that publishes the pillar publishes it under this name, so the
// reference check compares against one spelling; a guard test holds the constant to
// the json tags of the context types that carry the pillar, so a renamed pillar
// cannot leave the check watching a root no context defines.
const localsContextRoot = "locals"

// localsRefSite names the template a reference check is running for: the phrase a
// diagnostic quotes to locate it, the source location the halt carries, and the
// locals key being evaluated. Key is empty for a surface that is not itself a
// locals entry, which is what lets the self-reference remedy fire only where a
// self-reference is possible.
type localsRefSite struct {
	Where string
	File  string
	Key   string
}

// evaluateLocalsInOrder is the ONE home of the locals evaluation contract. Every
// surface that resolves a locals block routes through it, so the surfaces agree by
// construction rather than by four loops being reviewed against each other.
//
// The contract, implemented once:
//
//   - Entries resolve in AUTHOR order. The block is never sorted, reordered or
//     topologically arranged to make a reference resolve, because emitted output
//     would then be a function of an inference rather than of what was written.
//   - Each entry is evaluated against a context carrying the entries resolved
//     BEFORE it, published inside the loop. An entry may therefore read an earlier
//     sibling and receives its resolved value, never authored template text.
//   - A reference to a later sibling, or to a key the block does not declare,
//     halts before the template executes. Neither renders empty.
//   - The resolved value is the evaluated template with surrounding whitespace
//     trimmed.
//
// Callers supply only what genuinely differs between surfaces: the scope the
// surface admits, how it names a template to the evaluator, how it locates a halt,
// and how it builds the context that carries the prefix. withPrefix is called once
// per entry with the resolved-so-far map, and a caller must build its context from
// that argument rather than capturing an accumulator — an OrderedMap copy shares
// its backing store but carries its own key order, so a context built once outside
// the loop would deliver a stale key set.
//
// scope carries the surface's narrowing and the loop substitutes the per-entry
// context into it, so a root a surface refuses halts here rather than being left to
// each caller to guard. Threading the scope is what keeps the narrowing declared
// once per surface instead of once per surface per pillar it refuses.
func evaluateLocalsInOrder(
	locals types.OrderedMap[string],
	scope templateScope,
	site func(key string) localsRefSite,
	name func(key string) string,
	withPrefix func(resolved types.OrderedMap[string]) any,
) (types.OrderedMap[string], error) {
	resolved := types.OrderedMap[string]{}
	if locals.Len() == 0 {
		return resolved, nil
	}
	declared := locals.Keys()
	for key, tmplText := range locals.All() {
		s := site(key)
		if err := checkLocalsReferenceOrder(tmplText, s, resolved, declared); err != nil {
			return types.OrderedMap[string]{}, err
		}
		val, err := evalValueTemplate(name(key), tmplText, scope.with(withPrefix(resolved)), "{{", "}}")
		if err != nil {
			return types.OrderedMap[string]{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: s.File}, s.Where)
		}
		resolved.Set(key, strings.TrimSpace(val))
	}
	return resolved, nil
}

// checkLocalsReferenceOrder refuses a .locals reference the template cannot
// legitimately read at the point it is evaluated.
//
// resolved is the locals already evaluated when this template runs; declared is
// every key the block names. The two sets are what separate the two halts, and the
// separation is the point: a reference to a declared-but-unresolved key is an
// ORDERING fault the author fixes by moving the definition above its reader, while
// a reference to a key the block never declares is a NAMING fault the author fixes
// by correcting the key. One condition covering both would tell an author who
// mistyped a key to reorder their block.
//
// The comparison walks the parsed tree's root-anchored references through the same
// walk every other reference check in this package shares, so it reads NAMES and
// never template text: the pillar's name appearing inside a string literal or a
// comment is not a reference, and a key read inside a rebound range body names a
// field of the element rather than a local. Nothing here inspects what a resolved
// value means.
//
// A malformed template yields no tree and no refusal — a parse failure is the
// evaluator's diagnostic to report, and naming an ordering fault for one would
// describe the author's mistake wrongly. A bare .locals reference is admitted,
// matching the whole-root admission every scope applies: what a template does with
// the pillar once it holds it is the evaluator's business.
func checkLocalsReferenceOrder(tmplText string, site localsRefSite, resolved types.OrderedMap[string], declared []string) error {
	if tmplText == "" {
		return nil
	}
	tree, err := gotmpl.ParseTree(tmplText, "{{", "}}")
	if err != nil {
		return nil
	}
	for _, ref := range collectTemplateRootRefs(tree) {
		if ref.Segs[0] != localsContextRoot || len(ref.Segs) < 2 {
			continue
		}
		referenced := ref.Segs[1]
		if _, ok := resolved.Get(referenced); ok {
			continue
		}
		if slices.Contains(declared, referenced) {
			return localsForwardRefErr(site, ref.Path, referenced, resolved.Keys())
		}
		return localsUnknownRefErr(site, ref.Path, referenced, declared)
	}
	return nil
}

// checkProvidesLocalsReferences refuses a provides template that reads a local the
// entity's resolved locals block does not carry.
//
// It exists because admitting .locals on the provides surface admits an OPEN keyed
// map: a provides template naming a key the block never declared passes the root
// check and resolves through the missing-key policy to an empty string, so the halt
// is what keeps a mistyped key from reaching an emitted artifact. Every surface that
// evaluates provides against resolved locals routes through here, so the refusal is
// one contract rather than one per provides path.
//
// There is no forward class on this surface, and that is why the resolved block is
// passed as BOTH arguments: a provides template runs after the whole block has
// resolved, so every declared local is a resolved local and the only fault a
// reference can carry is a NAMING one. Passing the same set twice is what collapses
// checkLocalsReferenceOrder's two halts to the naming halt alone — telling an author
// to reorder a block would describe their mistake wrongly when no ordering resolves
// the key.
//
// The reference identification is checkLocalsReferenceOrder's, so the pillar is read
// by NAME through the shared parsed-tree walk. site names the referencing provides
// entry so the halt locates both sides of the mistake: the provides key that reads,
// and the local key that does not exist.
func checkProvidesLocalsReferences(
	provides types.OrderedMap[string],
	resolved types.OrderedMap[string],
	site func(key string) localsRefSite,
) error {
	if provides.Len() == 0 {
		return nil
	}
	declared := resolved.Keys()
	for key, tmplText := range provides.All() {
		if err := checkLocalsReferenceOrder(tmplText, site(key), resolved, declared); err != nil {
			return err
		}
	}
	return nil
}

func localsForwardRefErr(site localsRefSite, path, referenced string, resolvedKeys []string) error {
	available := "no locals are resolved yet"
	if len(resolvedKeys) > 0 {
		available = templateRootsPhrase("."+localsContextRoot, resolvedKeys)
	}
	remedy := fmt.Sprintf("define %q above %q in the locals block", referenced, site.Key)
	if referenced == site.Key {
		remedy = "a locals entry cannot read itself; compute the value from .values and .infra directly"
	}
	return cerrors.Wrap(ErrLocalsForwardReference, cerrors.CatalogError, cerrors.SourceLocation{File: site.File},
		fmt.Sprintf("%s references %q, but the locals block defines %q later — entries resolve in author order and an entry reads only a sibling defined above it. Resolved at this point: %s. To fix: %s",
			site.Where, path, referenced, available, remedy))
}

func localsUnknownRefErr(site localsRefSite, path, referenced string, declared []string) error {
	// The empty case is reachable: a provides or source template may read .locals on
	// an entity that declares no locals block at all, and naming an empty list there
	// would leave the diagnostic's second half blank.
	declares := "no locals are declared"
	if len(declared) > 0 {
		declares = "it declares " + templateRootsPhrase("."+localsContextRoot, declared)
	}
	return cerrors.Wrap(ErrLocalsUnknownReference, cerrors.CatalogError, cerrors.SourceLocation{File: site.File},
		fmt.Sprintf("%s references %q, but the locals block declares no %q — %s. To fix: correct the key name",
			site.Where, path, referenced, declares))
}

// EvaluateRecipeLocals evaluates each entry in recipe.Spec.Locals as a Go template
// against the restricted LocalsContext. Returns a deterministic resolved OrderedMap.
// Runs as the Pass-1 preamble — a node's locals resolve before that node's provides,
// so the resolved values are what a provides template reads through .locals.
//
// Iteration is in author order (recipe.Spec.Locals is an OrderedMap), and an entry
// may read an earlier sibling: the evaluation routes through evaluateLocalsInOrder,
// which publishes the resolved-so-far prefix into the context for each entry and
// halts on a forward or unknown sibling reference.
//
// nodeID is what .context.instance_id resolves to. The surface refuses
// .attachment_name — no attachment is being resolved here — so a base local
// reaching for it halts rather than rendering empty.
func EvaluateRecipeLocals(
	recipe *types.Recipe,
	nodeID string,
	values types.OrderedMap[any],
	infra types.OrderedMap[any],
) (types.OrderedMap[string], error) {
	if recipe == nil {
		return types.OrderedMap[string]{}, nil
	}
	return evaluateLocalsInOrder(
		recipe.Spec.Locals,
		recipeLocalsScope,
		func(key string) localsRefSite {
			return localsRefSite{
				Where: fmt.Sprintf("Recipe %q locals[%q]", recipe.Metadata.Name, key),
				File:  recipe.Metadata.Name,
				Key:   key,
			}
		},
		func(key string) string { return fmt.Sprintf("locals[%q]", key) },
		func(resolved types.OrderedMap[string]) any {
			return LocalsContext{
				Context: ProvidesContextBlock{InstanceID: nodeID},
				Values:  values,
				Infra:   infra,
				Locals:  resolved,
			}
		},
	)
}

// mergeFeatureLocals returns the merge of a base Recipe's resolved locals and a
// FeatureRecipe's resolved own locals. FR entries override base entries on key
// collision. Author order preserved — base first, then FR-only entries.
//
// BOTH arguments carry resolved values. This is a merge and nothing else — it
// evaluates no template, so authored template text handed to either side would
// travel verbatim to whatever reads the result. Every caller resolves the
// FeatureRecipe's block through EvaluateFeatureRecipeLocals before merging, and
// merging AFTER that resolution is what keeps a base local unreachable from an
// attachment local on every surface alike.
func mergeFeatureLocals(base, frOwn types.OrderedMap[string]) types.OrderedMap[string] {
	out := types.OrderedMap[string]{}
	for k, v := range base.All() {
		out.Set(k, v)
	}
	for k, v := range frOwn.All() {
		out.Set(k, v)
	}
	return out
}
