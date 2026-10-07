// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
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
// The surface declares two narrowings, and each is a deliberately kept refusal
// because the type carries the field for a surface that CAN supply it:
//
//   - attachment_name. No attachment is being resolved during a base Recipe's
//     locals pass, so the name would render empty. The type carries the field
//     because a FeatureRecipe's own locals populate it.
//   - context.rendered_name. Whether a node HAS a name of that kind depends on
//     which inputs its Recipe declares, whether it supplies source among them —
//     and on this surface a `source:` template is the very thing being evaluated
//     to decide what that source is. So the name is not offered here, and a
//     reference to it would render empty. A provides template reads it, which is
//     the surface it is published for.
//
// The Recipe source surface shares this scope rather than declaring a second one
// with the same content. Both are the base Recipe's node-local surface, evaluated
// against the same type with the same reasons for the same refusals, so one
// declaration is what keeps them from drifting into disagreement — and the second
// refusal above is a property of the source surface in particular, which sharing
// carries to both rather than leaving one of them to state it.
var recipeLocalsScope = templateScope{
	data:    LocalsContext{},
	exclude: []string{"attachment_name"},
	subGate: map[string][]string{identityContextRoot: {renderedNameContextKey}},
}

// featureRecipeLocalsScope is the reference set a FeatureRecipe's own locals entry
// is checked against. An attachment IS being resolved during that pass, so
// .attachment_name names it and is admitted here.
//
// One narrowing: context.rendered_name. The value on this surface is the HOST
// node's answer — the name the host's own bound provider reports for the resources
// it renders for the host — and an attachment is not that node: it binds its own
// recipe and therefore its own provider. So the value is not an answer about the
// attachment's own output at all. It is absent whenever the HOST's provider
// reports none for the host, whatever the attachment itself declares, and it comes
// from a different target entirely whenever the attachment's declared capability
// differs from its host's.
//
// It is deliberately NOT narrowed on the grounds that an attachment's supplied
// source renders under the attachment's own identity, which is what a reader
// reasonably assumes and is not what happens: for a chart-delivering attachment the
// name those templates render against stays the host's, precisely so the resources
// the chart's author declared are not renamed. The two coincide on that target and
// part on the two counts above, so the narrowing rests on where the answer comes
// from rather than on which identity it spells.
var featureRecipeLocalsScope = templateScope{
	data:    LocalsContext{},
	subGate: map[string][]string{identityContextRoot: {renderedNameContextKey}},
}

// localsContextRoot is the context root a template reads a resolved local through.
// Every surface that publishes the pillar publishes it under this name, so the
// reference check compares against one spelling; a guard test holds the constant to
// the json tags of the context types that carry the pillar, so a renamed pillar
// cannot leave the check watching a root no context defines.
//
// The spelling itself is declared beside the classification every such check
// routes through, because the surface that resolves a block for a `source:`
// coordinate compares against it too and cannot reach this package. The guard
// below therefore holds that declaration to these types' json tags.
const localsContextRoot = gotmpl.LocalsContextRoot

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

// evaluateLocalsInOrder is the ONE home of the locals evaluation contract for
// every surface that resolves a block FOR RENDERED OUTPUT. Each of those routes
// through it, so they agree by construction rather than by several loops being
// reviewed against each other.
//
// 🔴 That scope is the whole of the claim, and stating it is what keeps the claim
// true. One other evaluator of an authored locals block exists — the one that
// resolves a block so a Recipe's `source:` coordinate can read it, in the closure
// package this one imports. It does NOT route through here, because the
// dependency direction leaves no home reachable from both. On the reference-order
// contract below the two now answer identically, and structurally: both classify
// the reference through the same function, over the same author-order prefix, and
// report the same two conditions. Where they still differ is the template
// FUNCTION SET each registers: that one registers the narrower set, so THIS pass
// admits an expression it refuses — the reverse of the direction the ordering
// contract used to run in. Its own contract is documented where it is
// implemented, and assertions drive the two against each other rather than
// leaving the relationship to these two comments.
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
//
// withheld names what of each entry's context an author declared sensitive — the
// declared values and infra leaves, and the entries computed from them — so an
// entry's failure reports without such a value. It is stated for the whole block:
// an entry's context carries only the entries above it, and a withheld entry not
// yet resolved is absent from that context and so substitutes nothing.
func evaluateLocalsInOrder(
	locals types.OrderedMap[string],
	scope templateScope,
	site func(key string) localsRefSite,
	name func(key string) string,
	withPrefix func(resolved types.OrderedMap[string]) any,
	withheld redact.Keys,
) (types.OrderedMap[string], error) {
	resolved := types.OrderedMap[string]{}
	if locals.Len() == 0 {
		return resolved, nil
	}
	declared := locals.Keys()
	// The author-order prefix is carried explicitly rather than read back off
	// resolved, and that is what makes the contract legible rather than emergent.
	// Any halt below aborts the whole block, so at the entry for key k this holds
	// exactly the keys above k in author order — which is the same value a reader
	// with the declared order and nothing else can compute. Stating it as its own
	// value is what lets that reader be held to this one.
	resolvedKeys := make([]string, 0, len(declared))
	for key, tmplText := range locals.All() {
		s := site(key)
		if err := checkLocalsReferenceOrder(tmplText, s, resolvedKeys, declared); err != nil {
			return types.OrderedMap[string]{}, err
		}
		val, err := evalValueTemplate(name(key), tmplText, scope.with(withPrefix(resolved), withheld), "{{", "}}")
		if err != nil {
			return types.OrderedMap[string]{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: s.File}, s.Where)
		}
		resolved.Set(key, strings.TrimSpace(val))
		resolvedKeys = append(resolvedKeys, key)
	}
	return resolved, nil
}

// checkLocalsReferenceOrder refuses a .locals reference the template cannot
// legitimately read at the point it is evaluated.
//
// resolved is the locals already evaluated when this template runs, in author
// order; declared is every key the block names. The two sets are what separate the
// two halts, and the separation is the point: a reference to a
// declared-but-unresolved key is an ORDERING fault the author fixes by moving the
// definition above its reader, while a reference to a key the block never declares
// is a NAMING fault the author fixes by correcting the key. One condition covering
// both would tell an author who mistyped a key to reorder their block.
//
// # The decision is not made here
//
// Which reference offends, and which of the two faults it carries, is
// [gotmpl.ClassifyRootKeyReference] — the one home of that decision for every
// surface that admits or refuses such a reference. What is composed here is the
// DIAGNOSTIC: the referencing site, the remedy that applies to this occurrence,
// and the wrapping this package's callers expect. That split is what lets the
// lock-time closure walk reach the same decision without reaching into this
// package, which it cannot: this package already imports that walk's own.
//
// The classification reads NAMES through the under-collecting parsed-tree walk, so
// the pillar's name appearing inside a string literal or a comment is not a
// reference, and a key read inside a rebound range body names a field of the
// element rather than a local. Nothing here inspects what a resolved value means.
//
// A malformed template yields no refusal — a parse failure is the evaluator's
// diagnostic to report, and naming an ordering fault for one would describe the
// author's mistake wrongly. A bare .locals reference is admitted, matching the
// whole-root admission every scope applies: what a template does with the pillar
// once it holds it is the evaluator's business.
func checkLocalsReferenceOrder(tmplText string, site localsRefSite, resolved, declared []string) error {
	ref := gotmpl.ClassifyRootKeyReference(
		tmplText, "{{", "}}", localsContextRoot, resolved, declared,
	)
	switch ref.Fault {
	case gotmpl.RootKeyUnresolved:
		return localsForwardRefErr(site, ref.Path, ref.Key, resolved)
	case gotmpl.RootKeyUndeclared:
		return localsUnknownRefErr(site, ref.Path, ref.Key, declared)
	default:
		return nil
	}
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
// There is no forward class on this surface, and that is why the resolved block's
// key set is passed as BOTH arguments: a provides template runs after the whole block has
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
		if err := checkLocalsReferenceOrder(tmplText, site(key), declared, declared); err != nil {
			return err
		}
	}
	return nil
}

// The remedy sentences and the phrases naming the readable and declared key sets
// are NOT declared here. They are declared beside the classification that decides
// the fault, because the surface that resolves a locals block for a `source:`
// coordinate states the same sentences and cannot reach this package — and two
// copies of "define X above Y" would be free to tell one author two things about
// one mistake. What is composed here is the DIAGNOSTIC around them: the
// referencing site, the source location, and the wrapping this package's callers
// expect.

func localsForwardRefErr(site localsRefSite, path, referenced string, resolvedKeys []string) error {
	available := gotmpl.LocalsReadablePhrase(resolvedKeys)
	remedy := gotmpl.LocalsForwardRemedy(referenced, site.Key)
	return cerrors.Wrap(ErrLocalsForwardReference(), cerrors.CatalogError, cerrors.SourceLocation{File: site.File},
		cerrors.ComposeMessage(ErrLocalsForwardReference(), []string{
			fmt.Sprintf("%s references %q", site.Where, path),
			gotmpl.LocalsForwardPosition(referenced, site.Key),
			"entries resolve in author order and an entry reads only a sibling defined above it",
			fmt.Sprintf("resolved at this point: %s", available),
		}, remedy))
}

func localsUnknownRefErr(site localsRefSite, path, referenced string, declared []string) error {
	declares := gotmpl.LocalsDeclaredPhrase(declared)
	return cerrors.Wrap(ErrLocalsUnknownReference(), cerrors.CatalogError, cerrors.SourceLocation{File: site.File},
		cerrors.ComposeMessage(ErrLocalsUnknownReference(), []string{
			fmt.Sprintf("%s references %q", site.Where, path),
			fmt.Sprintf("the locals block declares no %q", referenced),
			declares,
		}, gotmpl.LocalsUnknownRemedy))
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
//
// withheld names what the node's Component declares sensitive among values and
// infra, and the locals computed from it, so a failing entry reports without such
// a value; a caller rendering data no declaration governs passes an empty set.
func EvaluateRecipeLocals(
	recipe *types.Recipe,
	nodeID string,
	values types.OrderedMap[any],
	infra types.OrderedMap[any],
	withheld redact.Keys,
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
				Context: ProvidesContextBlock{InstanceID: encodeNodeIdentity(nodeID)},
				Values:  values,
				Infra:   infra,
				Locals:  resolved,
			}
		},
		withheld,
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
