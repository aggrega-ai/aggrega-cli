// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"fmt"
	"sort"
	"text/template/parse"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// identityContextRoot is the context root a template reads a node's own
// identities through, and renderedNameContextKey is the key beneath it that names
// the value a target renders a node's output under.
//
// They are constants because every surface that watches for this key compares
// against the same two spellings: the rules declared over authored text, and the
// scopes that refuse the key rather than resolving it away. A spelling written
// out at each would agree on the day it was written, and a rename would then
// leave a rule watching a key no context defines — which reports exactly what a
// rule that ran and approved reports. The guard beside the context types holds
// both to those types' own json tags.
//
// How many such surfaces there are is deliberately not written here. A count
// beside the structure it counts is a second statement of what the structure
// already says, and the copy is what goes stale: a surface added later leaves the
// number wrong with nothing able to notice, and correcting it only resets the
// clock for the surface after that.
const (
	identityContextRoot    = "context"
	renderedNameContextKey = "rendered_name"
)

// renderedNameRootRef reports the first root access a parsed template makes to the
// name its target renders a node's output under, and whether it makes one.
//
// The reference is recovered from the collected root accesses rather than by
// matching the authored keystrokes, so every spelling that resolves to the same
// path is covered alike — the direct one, a read inside a with body over the
// identity pillar, and a read through a variable bound to it. Matching text would
// cover the first and miss the others, and the value would then reach an emitted
// artifact through exactly the spellings a defensive author reaches for.
//
// It yields the whole access rather than only its path, because a surface that
// refuses the key through the scope machinery needs the access itself in order to
// name the set it was consulted against. The path-only reading below is the same
// walk, and both are here so the two-segment comparison has one home.
func renderedNameRootRef(tree parse.Node) (gotmpl.RootRef, bool) {
	for _, ref := range gotmpl.CollectRootRefs(tree) {
		if len(ref.Segs) >= 2 && ref.Segs[0] == identityContextRoot && ref.Segs[1] == renderedNameContextKey {
			return ref, true
		}
	}
	return gotmpl.RootRef{}, false
}

// renderedNameReference reports the path of the first reference a parsed template
// makes to the name its target renders a node's output under, and whether it
// makes one.
func renderedNameReference(tree parse.Node) (string, bool) {
	ref, reads := renderedNameRootRef(tree)
	if !reads {
		return "", false
	}
	return ref.Path, true
}

// attachmentHostScope returns the reference set one surface of a per-component
// attachment is checked against, given the name its HOST's provider reported for
// the host.
//
// On every such surface .context.rendered_name is the host's answer, the way
// .context.instance_id there is the host's identity: an attachment names its own
// resources after the component hosting it, and the name the host's provider gives
// the host's resources is a spelling of that component its target accepts, where the
// identity as written may not be. So
// where the host's provider reported a name the surface's own scope stands and the
// value is on the context; where it reported none — a node rendering a placed
// artifact set, a node on a provider that derives no such name — the key is refused
// rather than resolved away, because an empty value there would name a resource
// nothing emits.
//
// It is the ONE home of that decision for the surfaces evaluated after the host's
// answer is known: the attachment's locals, its source and its provides. The rule
// declared over the attachment's render fields reads the same derivation for the
// set it reports, so what an author is refused and what they are told this surface
// defines cannot disagree.
func attachmentHostScope(scope templateScope, hostRenderedName string) templateScope {
	if hostRenderedName != "" {
		return scope
	}
	return scope.refusingKey(identityContextRoot, renderedNameContextKey)
}

// RenderedNameReporter is the optional capability a provider engine implements to
// report the name its target gives the resources it renders for a node — from the
// node's author-supplied source, the name those supplied templates themselves
// render against, and from a workload it synthesizes, the name it gives the
// resources it builds where it derives one.
//
// It is the OPPOSITE direction from [NameBounder] and the two must not be
// conflated. NameBounder carries a question the host asks about a name the host
// composed, and the answer carries a constraint; this carries no question at all
// — the guest composed the name, and the host spends the string without reading
// it. Neither stands in for the other: a constraint cannot be published in a
// template, and a name cannot bound anything.
//
// An engine that does not implement it reports nothing, and a recipe author has
// no such name to read for any node that engine renders. That is the honest
// position rather than a gap: a target whose resource names a Recipe writes
// verbatim, or whose synthesized resources carry the node's identity as written,
// has no derivation to report, and one whose name is a function of where its
// files were placed does not know it while a node is being rendered.
type RenderedNameReporter interface {
	RenderedName(ctx context.Context, in RenderedNameInput) (RenderedNameOutput, error)
}

// NodeRenderedNames is the name each node's own provider reports for the output
// it renders for that node, by node identity. A node
// whose provider reports none is absent rather than present and empty, so a
// reader can tell "no name" from "a name that happens to be empty".
type NodeRenderedNames map[string]string

// askRenderedName puts one node to a provider engine and reads back the name that
// target will render the node's output under.
//
// An engine that does not implement the capability reports no name without being
// asked, so adding the exchange costs a provider that serves no such name
// nothing — not a crossing, not a load.
func askRenderedName(ctx context.Context, engine any, in RenderedNameInput) (string, error) {
	reporter, ok := engine.(RenderedNameReporter)
	if !ok {
		return "", nil
	}
	out, err := reporter.RenderedName(ctx, in)
	if err != nil {
		return "", err
	}
	return out.Name, nil
}

// CollectRenderedNames asks each node's own provider what name it will render
// that node's output under, and returns the answers by node.
//
// # Why it is one pass ahead of the node walk rather than part of it
//
// A producer's `provides` templates read the value, and those are evaluated for
// the whole graph in one pass before any node renders. So the answers have to be
// complete before that pass, which puts this ahead of it. The recipe binding is
// what selects a node's provider, so the earliest point this can run at all is
// the point that binding completes — and it is asked there rather than later so
// that one computation site serves every reader.
//
// # What is asked, and of whom
//
// Only the two identities a target derives its own names from, plus which input
// kinds the node's bound Recipe declares: source the author wrote, and a workload
// for the provider to synthesize. The full render context does not exist at this
// point and is not needed: what a target names its own output is a function of
// the node's identity, not of its resolved values.
//
// Both flags read the text the AUTHOR wrote rather than a resolved value, because
// nothing has been resolved yet. For the source the two part for a Recipe whose
// `source:` template evaluates to nothing — authored-sourced, rendered
// sourceless — and the authored reading is the one available here. The workload
// flag is read through the same helper the render reads when it decides to
// synthesize, so a provider is told a node is synthesized exactly when the render
// synthesizes it.
//
// A node with no bound Recipe is not asked: there is no provider to ask, and no
// name for an author to read.
//
// # Determinism
//
// Nodes are walked in sorted identity order, so a guest that is asked about
// several nodes is asked in the same order on every run of identical input. The
// answers are keyed by node, so the map's content is order-independent; the walk
// order matters only to the guest.
func CollectRenderedNames(
	ctx context.Context,
	graph *types.IRGraph,
	recipes map[string]*types.Recipe,
	engineFactory EngineFactory,
) (NodeRenderedNames, error) {
	if graph == nil || engineFactory == nil {
		return nil, nil
	}
	names := NodeRenderedNames{}
	nodeIDs := graph.Spec.Nodes.Keys()
	sort.Strings(nodeIDs)
	for _, nodeID := range nodeIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		recipe, bound := recipes[nodeID]
		if !bound || recipe == nil {
			continue
		}
		engine, err := engineFactory(recipe.Spec.Capability)
		// A binding the factory cannot resolve leaves the node with no reported
		// name, which is the same answer a provider that reports none gives. The
		// resolution failure itself is reported by the render path that needs the
		// engine to render with, in full; raising it here would report it twice and
		// would report it first from a pass that only wanted a name.
		if err != nil || engine == nil {
			continue
		}
		name, err := askRenderedName(ctx, engine, RenderedNameInput{
			NodeID:      nodeID,
			Sourced:     recipeSourceDeclared(recipe),
			Synthesized: recipeWorkloadDeclared(recipe),
			// DerivedNameBasis is deliberately left unset. It is carried on a node
			// whose own identity a target's name alphabet refuses, and the one node
			// kind that carries it has its basis settled inside the node walk —
			// after this pass and after the provides evaluation this pass exists to
			// serve. A component node leaves it empty and the guest falls back to
			// the node's own identity, which is exactly what the render does.
		})
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{Instance: nodeID},
				fmt.Sprintf("node %q rendered name", nodeID))
		}
		if name == "" {
			continue
		}
		names[nodeID] = name
	}
	return names, nil
}
