// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// linkTriple names one cross-boundary link the way its placeholders name it: the
// consuming node, the consumed node and the socket the edge is bound under.
type linkTriple struct {
	source, target, socket string
}

func linkTripleOf(edge types.IREdge) linkTriple {
	return linkTriple{source: edge.Source, target: edge.Target, socket: edge.Socket}
}

// linkRead is what one read placeholder stands for: the link and the provides key
// it was stamped for, the connection alias the consuming node knows the link by,
// the real value, and the delivery placeholder the same key takes on the inject
// pipe, which is what the read placeholder is rewritten to once a template has
// been shown to copy it.
type linkRead struct {
	link      linkTriple
	alias     string
	key       string
	value     string
	delivered string
}

// crossBoundaryLinks is one render's view of the uses edges the cross-boundary
// link pass wires, and its record of what the render's templates read off them.
//
// It is classified once, before any template of the render is evaluated, so every
// stage that needs to know whether an edge crosses into another output reads one
// answer: the delivery-pipe stamp, the stamp of the connection views a template
// reads (.context.uses and the boundary edge views) and the link pass itself.
//
// The record half is filled as templates evaluate: each read placeholder a
// template copies into its output is recorded against the link it belongs to, and
// the link pass hands the recorded keys to the providers as the edge's
// read_provides_keys.
type crossBoundaryLinks struct {
	fires map[linkTriple]bool
	// stampErr holds, per consuming node, the failure deriving one of its links'
	// dispatch identities met. It is returned at the delivery-pipe stamp, which is
	// where the same derivation has always halted.
	stampErr     map[string]error
	aliases      map[linkTriple]string
	placeholders map[string]linkRead
	read         map[linkTriple]map[string]bool
}

func newCrossBoundaryLinks() *crossBoundaryLinks {
	return &crossBoundaryLinks{
		fires:        map[linkTriple]bool{},
		stampErr:     map[string]error{},
		aliases:      map[linkTriple]string{},
		placeholders: map[string]linkRead{},
		read:         map[linkTriple]map[string]bool{},
	}
}

// HoldsLinkReadPlaceholders reports whether any read placeholder has been stamped.
func (l *crossBoundaryLinks) HoldsLinkReadPlaceholders() bool {
	return l != nil && len(l.placeholders) > 0
}

// firing reports whether the edge is one the link pass wires.
func (l *crossBoundaryLinks) firing(edge types.IREdge) bool {
	return l != nil && l.fires[linkTripleOf(edge)]
}

// classifyCrossBoundaryLinks decides, for every uses edge of the graph, whether
// the cross-boundary link pass wires it, by the one classification the link pass
// applies ([classifyCrossBoundaryLink]).
//
// An edge is considered only when its consuming node's provider advertises
// cross-boundary linking; a shared provider reports one linking claim for both
// ends, so the consumer's claim stands in for the target's, as it always has at
// the delivery-pipe stamp. Each end's dispatch identity comes from the same pure
// path evaluation the per-node loop runs ([onDemandDispatchKey]), so this view of
// which edges cross a boundary matches the link pass's exactly.
//
// A provider whose plugin cannot be selected links nothing here: the per-node loop
// reports that failure where it always has. A dispatch identity that cannot be
// derived is held against the consuming node and returned at its delivery-pipe
// stamp, which is where the same derivation halted before this view existed.
func classifyCrossBoundaryLinks(
	graph *types.IRGraph,
	nodeRecipes map[string]*types.Recipe,
	engineFactory EngineFactory,
	input RendererInput,
	boundaryDirEvals map[string]string,
	perBoundaryAnnotations map[string]types.OrderedMap[string],
) *crossBoundaryLinks {
	links := newCrossBoundaryLinks()
	if graph == nil || engineFactory == nil {
		return links
	}
	var parameters map[string]any
	if input.ResolveResult != nil {
		parameters = input.ResolveResult.Parameters
	}
	claims := map[string]bool{}
	claimOf := func(providerRef string) bool {
		if claim, ok := claims[providerRef]; ok {
			return claim
		}
		engine, err := engineFactory(providerRef)
		claim := err == nil && linksCrossBoundary(engine)
		claims[providerRef] = claim
		return claim
	}
	type derived struct {
		key dispatchKey
		ok  bool
		err error
	}
	keys := map[string]derived{}
	keyOf := func(nodeID string) derived {
		if d, ok := keys[nodeID]; ok {
			return d
		}
		var d derived
		node, found := graph.Spec.Nodes.Get(nodeID)
		recipe := nodeRecipes[nodeID]
		if found && recipe != nil {
			d.key, d.err = nodeDispatchKey(nodeID, node, recipe, input, parameters, boundaryDirEvals, perBoundaryAnnotations)
			d.ok = d.err == nil
		}
		keys[nodeID] = d
		return d
	}
	// A linking node's own identity is derived whether or not it consumes over any
	// edge, as the delivery-pipe stamp always derived it.
	for _, nodeID := range graph.Spec.Nodes.Keys() {
		recipe := nodeRecipes[nodeID]
		if recipe == nil || !claimOf(recipe.Spec.Capability) {
			continue
		}
		if d := keyOf(nodeID); d.err != nil {
			links.stampErr[nodeID] = d.err
		}
	}
	for _, edge := range graph.Spec.Edges {
		srcRecipe := nodeRecipes[edge.Source]
		if srcRecipe == nil || !claimOf(srcRecipe.Spec.Capability) {
			continue
		}
		src, tgt := keyOf(edge.Source), keyOf(edge.Target)
		for _, d := range []derived{src, tgt} {
			if d.err != nil {
				if _, held := links.stampErr[edge.Source]; !held {
					links.stampErr[edge.Source] = d.err
				}
			}
		}
		if !src.ok || !tgt.ok {
			continue
		}
		if classifyCrossBoundaryLink(src.key, tgt.key, true, true) != crossBoundaryLinkFires {
			continue
		}
		links.fires[linkTripleOf(edge)] = true
		links.aliases[linkTripleOf(edge)] = connectionAlias(edge)
	}
	return links
}

// stampCrossBoundaryLinkSentinels overwrites the current node's cross-boundary
// injected values with deterministic link placeholders before the node's
// artifacts render, so the placeholder is what the render pipes into the
// consuming output. The post-assembly link pass then locates each placeholder
// in the assembled output and replaces it with the provider-native
// cross-reference — a two-stage handshake because the consuming render runs per
// node while the reference can only be shaped once every dispatch group has
// assembled and the peer's published identity is known.
//
// Which edges it stamps is the render's one classification ([crossBoundaryLinks]),
// so the stamp's view of which edges cross a boundary matches the link pass's view
// exactly.
//
// Only a value already delivered on the edge (present in the inject pipe after
// the feature-order overlays) is overwritten: the stamp replaces a delivered
// scalar, it never introduces an injection the edge did not carry.
func stampCrossBoundaryLinkSentinels(tmplCtx *types.TemplateContext, nodeID string, graph *types.IRGraph, links *crossBoundaryLinks) error {
	if links == nil || graph == nil {
		return nil
	}
	if err := links.stampErr[nodeID]; err != nil {
		return err
	}
	stamped := false
	for _, edge := range graph.Spec.Edges {
		if edge.Source != nodeID || !links.firing(edge) {
			continue
		}
		if stampEdgeSentinels(&tmplCtx.Context.Inject, edge) {
			stamped = true
		}
	}
	if stamped {
		tmplCtx.Context.Inject.All = buildInjectAll(tmplCtx.Context.Inject)
	}
	return nil
}

// stampCrossBoundaryLinkReads replaces every provides value of each of the
// current node's connections the link pass wires with a read placeholder for its
// key, in the connection views the node's templates read (.context.uses), and
// hands the render's record to the context so every template evaluated against it
// — or against a copy of it — is held to copying those values.
//
// It runs before any template of the node reads the connection views, the
// attached FeatureRecipes' inject mappings included, so no template sees a
// linked connection's real value where the provider later rewrites it. Every
// other field of a connection view is left as it stands, and so is every
// connection the link pass does not wire.
func stampCrossBoundaryLinkReads(tmplCtx *types.TemplateContext, nodeID string, graph *types.IRGraph, links *crossBoundaryLinks) {
	if links == nil || tmplCtx == nil || graph == nil {
		return
	}
	stamped := false
	for _, edge := range graph.Spec.Edges {
		if edge.Source != nodeID || !links.firing(edge) {
			continue
		}
		alias := connectionAlias(edge)
		entry, ok := tmplCtx.Context.Uses.Get(alias)
		if !ok {
			continue
		}
		entry.Provides = links.stampProvides(linkTripleOf(edge), alias, entry.Provides)
		tmplCtx.Context.Uses.Set(alias, entry)
		stamped = true
	}
	if stamped {
		tmplCtx.LinkReads = links
	}
}

// stampBoundaryLinkReads does for a boundary-scope FeatureRecipe's context what
// [stampCrossBoundaryLinkReads] does for a node's: every edge view of the
// boundary the link pass wires carries a read placeholder in place of each
// provides value, and the context carries the render's record.
func (l *crossBoundaryLinks) stampBoundaryLinkReads(frCtx *types.TemplateContext) {
	if l == nil || frCtx == nil || frCtx.Boundary == nil {
		return
	}
	bc := frCtx.Boundary
	stamped := false
	for _, views := range []*[]types.EnrichedEdge{
		&bc.InternalUsesEdges, &bc.OutgoingCrossBoundaryUsesEdges, &bc.IncomingCrossBoundaryUsesEdges, &bc.CrossBoundaryUsesEdges,
	} {
		for i := range *views {
			view := &(*views)[i]
			link := linkTriple{source: view.Source, target: view.Target, socket: view.Socket}
			if !l.fires[link] {
				continue
			}
			view.Provides = l.stampProvides(link, l.aliases[link], view.Provides)
			stamped = true
		}
	}
	if stamped {
		frCtx.LinkReads = l
	}
}

// stampProvides returns a new provides map, in the same author order, holding a
// read placeholder in place of each value, and records what each stands for. The
// map it is handed is left as it stands, because the same map is the producer's
// own provides everywhere else in the render.
func (l *crossBoundaryLinks) stampProvides(link linkTriple, alias string, provides types.OrderedMap[string]) types.OrderedMap[string] {
	var out types.OrderedMap[string]
	for key, value := range provides.All() {
		placeholder := rendercore.CrossBoundaryLinkReadPlaceholder(link.source, link.target, link.socket, key)
		delivered := rendercore.CrossBoundaryLinkSentinel(link.source, link.target, link.socket, key)
		l.placeholders[placeholder] = linkRead{link: link, alias: alias, key: key, value: value, delivered: delivered}
		out.Set(key, placeholder)
	}
	return out
}

// readKeys returns the provides keys the render's templates read off the edge,
// in the producer's author order of its provides.
func (l *crossBoundaryLinks) readKeys(edge types.IREdge, allProvides NodeProvides) []string {
	if l == nil {
		return nil
	}
	read := l.read[linkTripleOf(edge)]
	if len(read) == 0 {
		return nil
	}
	provides := allProvides[edge.Target]
	keys := make([]string, 0, len(read))
	for _, key := range provides.Keys() {
		if read[key] {
			keys = append(keys, key)
		}
	}
	return keys
}

// linkReadsOf returns the render's record a template's data carries, or nil.
func linkReadsOf(data any) *crossBoundaryLinks {
	var reads types.CrossBoundaryLinkReads
	switch ctx := data.(type) {
	case *types.TemplateContext:
		if ctx == nil {
			return nil
		}
		reads = ctx.LinkReads
	case types.TemplateContext:
		reads = ctx.LinkReads
	default:
		return nil
	}
	links, _ := reads.(*crossBoundaryLinks)
	if !links.HoldsLinkReadPlaceholders() {
		return nil
	}
	return links
}

// runCopyOnly evaluates a template whose data carries read placeholders, holding
// it to copying each one: it is evaluated once over the placeholders and once
// over the real values, every other input identical, and the first output with
// each placeholder written back to its real value must equal the second byte for
// byte. A template that writes a value whole, or inside a larger string, or
// through a function that leaves it as it is, passes; one that compares it,
// branches on it, measures it or changes it does not, because those evaluate
// against the placeholder rather than the value, and the build halts naming the
// read.
//
// When it passes, each read placeholder in the output is rewritten to the delivery
// placeholder for the same key — the form a provider locates — and recorded as
// read off its link. A failure both evaluations meet is reported as the real
// evaluation's, which is the failure the template has with or without the link.
//
// It reports checked false when the data carries none of the record's
// placeholders, and the caller evaluates the template as it otherwise would.
func (l *crossBoundaryLinks) runCopyOnly(p preparedTemplate) (string, bool, error) {
	data := p.eval.data()
	present := l.placeholdersInTree(data)
	if len(present) == 0 {
		return "", false, nil
	}
	out, outErr := p.executeOver(data)
	realOut, realErr := p.executeOver(l.writtenBack(p.eval, present, ""))
	if outErr != nil && realErr != nil {
		return "", true, p.executionFailure(realErr)
	}
	if outErr == nil && realErr == nil && l.writeBack(out, present) == realOut {
		read := l.placeholdersInText(out, present)
		l.record(read)
		return l.deliveredForm(out, read), true, nil
	}
	involved := l.involved(present, realErr == nil, func(except string) (string, error) {
		return p.executeOver(l.writtenBack(p.eval, present, except))
	}, realOut)
	return "", true, l.notCopiedError(fmt.Sprintf("template %q", p.eval.name), p.eval.name, involved)
}

// involved names the placeholders a failed copy check turns on, key by key: each
// placeholder is left in place alone, every other written back to its value, and
// it is involved where that evaluation does not, once written back, reproduce the
// real one. Where no single placeholder accounts for the difference, every
// placeholder the data carried is named.
func (l *crossBoundaryLinks) involved(present []string, realOK bool, runLeaving func(placeholder string) (string, error), realOut string) []string {
	var involved []string
	for _, placeholder := range present {
		out, err := runLeaving(placeholder)
		differs := (err == nil) != realOK || (err == nil && l.writeBack(out, []string{placeholder}) != realOut)
		if differs {
			involved = append(involved, placeholder)
		}
	}
	if len(involved) == 0 {
		return present
	}
	return involved
}

// notCopiedError is the copy-only halt, naming the template, and each provides key
// involved with the connection it was read off: the consuming component, the
// connection's alias and socket, and the producing component.
func (l *crossBoundaryLinks) notCopiedError(surface, location string, involved []string) error {
	reads := make([]linkRead, 0, len(involved))
	for _, placeholder := range involved {
		reads = append(reads, l.placeholders[placeholder])
	}
	sort.SliceStable(reads, func(i, j int) bool {
		a, b := reads[i], reads[j]
		if a.link.source != b.link.source {
			return a.link.source < b.link.source
		}
		if a.alias != b.alias {
			return a.alias < b.alias
		}
		return a.key < b.key
	})
	values := []string{surface}
	for _, r := range reads {
		values = append(values, fmt.Sprintf("provides key %q that component %q reads off its connection %q on socket %q to component %q",
			r.key, r.link.source, r.alias, r.link.socket, r.link.target))
	}
	return cerrors.Wrap(ErrCrossBoundaryLinkValueNotCopied(), cerrors.CatalogError, cerrors.SourceLocation{File: location},
		cerrors.ComposeMessage(ErrCrossBoundaryLinkValueNotCopied(), values,
			"Copy the value as it stands, whole or inside a larger string, because the provider writes in its place what the value becomes in the other output, and branch on a value of the component's own instead"))
}

// placeholdersInTree returns the record's read placeholders a template's data
// carries anywhere, sorted.
func (l *crossBoundaryLinks) placeholdersInTree(tree any) []string {
	found := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case string:
			l.collectPlaceholders(t, found)
		case map[string]any:
			for _, x := range t {
				walk(x)
			}
		case []any:
			for _, x := range t {
				walk(x)
			}
		}
	}
	walk(tree)
	return sortedSet(found)
}

// placeholdersInText returns which of the given placeholders the text carries,
// sorted.
func (l *crossBoundaryLinks) placeholdersInText(text string, among []string) []string {
	var found []string
	for _, placeholder := range among {
		if strings.Contains(text, placeholder) {
			found = append(found, placeholder)
		}
	}
	return found
}

// collectPlaceholders adds each of the record's read placeholders the text
// carries to found. Text carrying no placeholder prefix at all is passed over at
// once, which is nearly every string a template's data holds.
func (l *crossBoundaryLinks) collectPlaceholders(text string, found map[string]bool) {
	if !strings.Contains(text, rendercore.CrossBoundaryLinkSentinelPrefix) {
		return
	}
	for placeholder := range l.placeholders {
		if strings.Contains(text, placeholder) {
			found[placeholder] = true
		}
	}
}

// writeBack returns the text with each of the given placeholders replaced by the
// value it stands for.
func (l *crossBoundaryLinks) writeBack(text string, placeholders []string) string {
	for _, placeholder := range placeholders {
		text = strings.ReplaceAll(text, placeholder, l.placeholders[placeholder].value)
	}
	return text
}

// deliveredForm returns the text with each of the given read placeholders
// replaced by the delivery placeholder for the same key.
func (l *crossBoundaryLinks) deliveredForm(text string, placeholders []string) string {
	for _, placeholder := range placeholders {
		text = strings.ReplaceAll(text, placeholder, l.placeholders[placeholder].delivered)
	}
	return text
}

// record notes each placeholder as read off its link.
func (l *crossBoundaryLinks) record(placeholders []string) {
	for _, placeholder := range placeholders {
		r := l.placeholders[placeholder]
		if l.read[r.link] == nil {
			l.read[r.link] = map[string]bool{}
		}
		l.read[r.link][r.key] = true
	}
}

// writtenBack converts an evaluation's data afresh, as every execution of it
// does, and writes every given placeholder but one — except, which may be empty —
// back to its real value.
func (l *crossBoundaryLinks) writtenBack(e templateEval, placeholders []string, except string) any {
	keep := make([]string, 0, len(placeholders))
	for _, placeholder := range placeholders {
		if placeholder != except {
			keep = append(keep, placeholder)
		}
	}
	return rewriteTreeStrings(e.data(), func(s string) string { return l.writeBack(s, keep) })
}

// rewriteTreeStrings rewrites every string in a converted template data tree in
// place. The tree is the conversion's own fresh copy, so nothing another
// evaluation reads is touched.
func rewriteTreeStrings(v any, rewrite func(string) string) any {
	switch t := v.(type) {
	case string:
		return rewrite(t)
	case map[string]any:
		for k, x := range t {
			t[k] = rewriteTreeStrings(x, rewrite)
		}
		return t
	case []any:
		for i, x := range t {
			t[i] = rewriteTreeStrings(x, rewrite)
		}
		return t
	default:
		return v
	}
}

func sortedSet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// onDemandDispatchKey derives a node's dispatch key by evaluating the pure path
// template the per-node loop evaluates, so a target's key can be computed before
// the target's own loop iteration reaches it. It shares [computeFinalPath] with
// the loop, so the key it returns is byte-identical to the one the loop records.
func onDemandDispatchKey(
	nodeID, providerName, recipeName, boundaryPath string,
	bi *resolver.BoundaryInfra,
	parameters map[string]any,
	boundaryDirEvals map[string]string,
	perBoundaryAnnotations map[string]types.OrderedMap[string],
) (dispatchKey, error) {
	eval, err := computeFinalPath(nodeID, boundaryPath, providerName, recipeName, bi, parameters, boundaryDirEvals, perBoundaryAnnotations)
	if err != nil {
		return dispatchKey{}, err
	}
	return dispatchKey{Path: eval.path, Provider: providerName}, nil
}

// boundaryInfraFor returns the resolver-side per-component BoundaryInfra for a
// node, or nil when the resolve carries none (path evaluation then uses its
// documented defaults).
func boundaryInfraFor(input RendererInput, nodeID string) *resolver.BoundaryInfra {
	if input.ResolveResult == nil {
		return nil
	}
	if bi, ok := input.ResolveResult.BoundaryInfra[nodeID]; ok {
		return bi
	}
	return nil
}

// stampEdgeSentinels overwrites the delivered value of each of an edge's
// injected keys with the deterministic placeholder for that (edge, output-key)
// pair, under every mechanism the edge's inject mapping names — env, file, arg
// and any other — walked as the edge delivery walks them ([deliveryMappingMechanisms]).
// Only a key already present in the pipe is overwritten — the edge's delivered
// value is replaced, never a new key introduced — so a value the producer's
// provides did not carry stays absent. It reports whether it changed any value.
func stampEdgeSentinels(inject *types.InjectPipe, edge types.IREdge) bool {
	stamped := false
	for mech, keymap := range deliveryMappingMechanisms(edge.Inject) {
		dest, keep := injectPipeMechanism(inject, mech)
		changed := false
		for localKey, providesKey := range keymap.All() {
			if _, delivered := dest.Get(localKey); !delivered {
				continue
			}
			dest.Set(localKey, rendercore.CrossBoundaryLinkSentinel(edge.Source, edge.Target, edge.Socket, providesKey))
			changed = true
		}
		if changed {
			keep()
			stamped = true
		}
	}
	return stamped
}
