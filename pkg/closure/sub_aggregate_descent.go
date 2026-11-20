// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package closure

import (
	"context"
	"fmt"
	"sort"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ReleaseAggregateKindHint marks the closure entity a Release's own spec.aggregate
// names. Every other collection site's hint is the kind's own word — a
// sub-aggregate type URI (Aggregate.Spec.Aggregates) carries KindAggregate — and
// this one is distinct from it on purpose: DescendSubAggregates selects the entities
// hinted KindAggregate to load and re-walk, so the Aggregate the Release deploys is never loaded and
// re-walked as though some Aggregate had imported it, and the walk resolves it
// exactly as it resolves a reference carrying no hint.
//
// What it adds is the kind. An Aggregate named by identity and fetched from a
// registry is a bare name, and a bare name carries no kind segment, so the one
// binding of a reference to its registry location and trust policy
// (catalog.SignatureSubjectOf) reads the kind from this hint. With it, the pull
// verifies that Aggregate's signature and the build holds its row to the recorded
// fact, as for every other reference a registry served; one named by path is read
// from beside the Release, which neither of them holds.
//
// It records an Aggregate (EntityKindOf maps it to KindAggregate), so the lockfile
// row it produces carries that kind. It is the weaker of the two Aggregate hints: a
// sub-aggregate reference to the same URI replaces it, so an Aggregate another one
// includes is descended as one. The hint itself is not serialised to the lockfile.
const ReleaseAggregateKindHint = "release_aggregate"

// maxSubAggregateDepth bounds DescendSubAggregates' transitive recursion. It
// shares the value of the topology flattener's inclusion-depth bound: the two
// are independent constants over the same authoring graph (one bounds closure
// pinning, the other bounds IR flattening), deliberately aligned so a graph
// that builds also pins and vice versa.
const maxSubAggregateDepth = 10

// ErrSubAggregateDepth halts DescendSubAggregates when transitive descent
// exceeds maxSubAggregateDepth — the backstop for an inclusion graph that
// evades the visited set (for example a version-renaming chain that presents a
// fresh identity at every level). An ordinary cycle (A imports B imports A)
// terminates earlier via the visited set and never reaches this bound.
func ErrSubAggregateDepth() error { return errSubAggregateDepth }

var errSubAggregateDepth = cerrors.Register("sub-aggregate-depth", cerrors.Meta{
	ShortCode: "E0113",
	Message:   "resolve: sub-aggregate inclusion depth exceeded",
	RulePlace: cerrors.RuleNoStageReaches("The closure walk's transitive sub-aggregate descent evaluates the rule, bounding how deep an " +
		"inclusion chain may run before the descent refuses to continue. The depth is a fact about the " +
		"descent in flight, and the walk pins a Release's reachable entities rather than compiling them, " +
		"so no declared stage holds it."),
})

// AggregateLoader loads the parsed body of a resolved (pinned) sub-aggregate so
// DescendSubAggregates can re-walk its own references. The digest is the pin
// just recorded for the URI; a cache-backed implementation reads the cache
// slot for that URI and version and verifies it against the digest before
// returning, so the descent walks the exact bytes the pin covers — never a
// re-serialization or an unverified read.
//
// The loader is injected rather than exposed as a Lookup method because only
// transitive aggregate descent needs resolved bytes; the Lookup surface stays
// a version/digest/tier resolver for every other axis.
type AggregateLoader interface {
	LoadAggregate(uri, version, digest string) (*types.Aggregate, error)
}

// ErrNoAggregateLoader halts a caller whose catalog lookup cannot load resolved
// sub-aggregate bodies, so the sub-aggregate axis cannot be computed over it.
func ErrNoAggregateLoader() error { return errNoAggregateLoader }

var errNoAggregateLoader = cerrors.Register("catalog-cannot-load-sub-aggregate-bodies", cerrors.Meta{
	ShortCode: "E0108",
	Message:   "resolve: catalog lookup cannot load resolved sub-aggregate bodies",
	RulePlace: cerrors.NotARule("A caller supplied a catalog lookup that cannot load resolved sub-aggregate bodies, so the " +
		"sub-aggregate axis cannot be computed over it at all. It reports how this program was wired " +
		"rather than a rule over anything an author wrote."),
})

// RequireAggregateLoader adapts a Lookup to the AggregateLoader the
// sub-aggregate axis needs, refusing with ErrNoAggregateLoader when the lookup
// cannot serve resolved bodies.
//
// Every caller that descends or reconstructs that axis goes through this one
// function, because the failure it guards is silent by construction:
// DescendSubAggregates reads a nil loader as "no loader was wired" and returns
// immediately, so a caller that discarded a failed assertion would hand it nil
// and receive a closure short an entire axis with nothing reported. Routing the
// check here leaves no site able to discard it.
func RequireAggregateLoader(lookup Lookup) (AggregateLoader, error) {
	loader, ok := lookup.(AggregateLoader)
	if !ok {
		return nil, ErrNoAggregateLoader()
	}
	return loader, nil
}

// DescendSubAggregates extends an already-computed Closure with its transitive
// sub-aggregate axis: every Aggregate reachable through a chain of
// Aggregate.Spec.Aggregates references is loaded, re-walked, and its own
// references resolved and pinned into the closure. ComputeClosure walks one
// in-memory Aggregate value, so it pins only the DIRECT sub-aggregates of the
// top-level Aggregate; this pass closes over the rest.
//
// It is a separate pass, run after ComputeClosure (and the library discovery
// passes) — never inside ComputeClosure, which takes no loader and so cannot
// obtain the parsed bodies this pass re-walks. Every path that pins or verifies a
// closure runs it, through the one descent of the pinned axes (DescendPinnedAxes):
// the pull that writes the pins, and the hermetic computation a build or a serve
// drift check performs against an existing lockfile. A hermetic caller is
// unaffected by that reach, because the loader's contract is a read of
// already-resolved bytes — authored catalog bytes, or the cache slot the pin
// names, verified against that pin — never a fetch. The shape mirrors
// DescendRecipeSources: a separate pass over the resolved closure driven by an
// injected loader.
//
// Termination has two disciplines, matching the topology flattener:
//
//   - A visited set keyed on resolved entity URI (the name:version identity)
//     makes a repeat reference a no-op. This dedups diamonds (A→B→D, A→C→D pins
//     D once) and terminates cycles (A→B→A halts) without error.
//   - A depth bound (maxSubAggregateDepth) backstops an inclusion graph that
//     evades the visited set; exceeding it returns ErrSubAggregateDepth.
//
// Newly-discovered entities are appended to c.Entities (deduped by kind and URI
// against what is already present); the closure is re-canonicalised before return, so
// the pinned set — and the lockfile written from it — is byte-identical across
// runs regardless of descent order. A nil loader is a no-op (the caller wired
// no registry tier).
//
// The descent goes one level at a time, and each level's sub-aggregates are
// loaded in URI order, never in the order a map is ranged in or the order their
// parents were loaded in. What is loaded first is visible: the first sub-aggregate
// that fails to load, or whose own references do not resolve, is the one the
// refusal names, and an entity two siblings both reference keeps the kind and the
// resolution the sibling loaded first recorded for it.
//
// The instances of every included module are appended to c.Instances once the
// descent is done, depth first in the author order of each Aggregate's includes:
// a module's own instances, then those of the modules it includes. Each module is
// loaded and walked once whatever number of Aggregates include it, and its
// instances are reported once per place the composition includes it, each
// component under the identity that place gives it (types.JoinQualifiedID of the
// names the includes are written under, then its own name), as the composition
// names the node it flattens it to. The first level of includes is the one the
// walk of the Release's own Aggregate recorded; a closure assembled without that
// walk reports no module's instances.
func DescendSubAggregates(ctx context.Context, c *Closure, loader AggregateLoader, lookup Lookup) error {
	if c == nil || loader == nil {
		return nil
	}

	index := entityIndexOf(c)
	var level []descentFrame
	for _, e := range c.Entities {
		if e.KindHint == KindAggregate {
			level = append(level, descentFrame{uri: e.URI, version: e.ResolvedVersion, digest: e.Digest, depth: 1})
		}
	}

	visited := map[string]struct{}{}
	walked := map[string]moduleWalk{}
	var deferred []deferredTypes
	for len(level) > 0 {
		// Stable, so a URI queued twice within one level keeps the frame of the
		// parent loaded first, which the previous level's order already fixes.
		sort.SliceStable(level, func(i, j int) bool { return level[i].uri < level[j].uri })
		var next []descentFrame
		for _, f := range level {
			if _, seen := visited[f.uri]; seen {
				continue
			}
			visited[f.uri] = struct{}{}
			if f.depth > maxSubAggregateDepth {
				return fmt.Errorf("%w: %q at depth %d", ErrSubAggregateDepth(), f.uri, f.depth)
			}

			agg, err := loader.LoadAggregate(f.uri, f.version, f.digest)
			if err != nil {
				return fmt.Errorf("resolve: load sub-aggregate %q: %w", f.uri, err)
			}

			sub := newWalkState()
			// The loader's contract is a parsed *types.Aggregate; the walk's is a
			// normalized one. Normalizing here is where the two meet — the walk cannot
			// be entered without the guarantee, and a loader that already normalized
			// (every production one loads through the parse core, which does) hands
			// back the identical value.
			sub.walkAggregate(types.Normalize(agg), fmt.Sprintf("aggregate(%s)", f.uri))
			instanceTypes, err := sub.resolveAllButInstanceTypes(ctx, lookup)
			if err != nil {
				return err
			}
			walked[f.uri] = moduleWalk{instances: sub.instances, modules: sub.modules, file: moduleFile(f)}
			deferred = append(deferred, deferredTypes{walk: sub, keys: instanceTypes})

			for key, p := range sub.entities {
				if p.resolution == nil {
					continue
				}
				uri := key.URI
				ref := p.toResolvedRef(uri)
				// A transitively-shared entity (a diamond's shared leaf, an entity
				// also used at the top level) accumulates every referencing site,
				// like ComputeClosure's single-walkState accumulation.
				// canonicaliseClosure sorts the merged set, so the result is
				// independent of descent order.
				mergeEntity(c, index, ref)
				if ref.KindHint == KindAggregate {
					if _, done := visited[uri]; !done {
						next = append(next, descentFrame{uri: uri, version: ref.ResolvedVersion, digest: ref.Digest, depth: f.depth + 1})
					}
				}
			}
		}
		level = next
	}

	c.Instances = append(c.Instances, moduleInstances(c.modules, "", walked, map[string]bool{})...)
	if err := resolveModuleInstanceTypes(ctx, c, index, deferred, lookup); err != nil {
		return err
	}
	canonicaliseClosure(c)
	return nil
}

// deferredTypes is one module walk and the Components and Features its instances
// are typed by, which the descent resolves once every module is walked.
type deferredTypes struct {
	walk *walkState
	keys []EntityKey
}

// resolveModuleInstanceTypes resolves the types the modules' instances write,
// which the descent leaves until every module is walked: the version an instance
// resolves at is settled from every constraint on every instance writing its
// reference, and a module's instance is mapped by the name the composition gives
// it, which is known only then (InstanceVersionSelector). The settlement is handed
// every instance of the Release, the closure's own and the modules', with the
// Manifest the walk was handed.
//
// The walks are resolved in the order the descent loaded them, each in its own
// order, so a type two modules write keeps the resolution of the one loaded first.
// A type the closure already holds — one the Release's own Aggregate writes, or
// one an earlier module wrote — is not resolved again: its sites are merged onto
// the entity the closure holds.
func resolveModuleInstanceTypes(ctx context.Context, c *Closure, index map[EntityKey]int, deferred []deferredTypes, lookup Lookup) error {
	var requests map[EntityKey]string
	settled := false
	for _, d := range deferred {
		var unresolved []EntityKey
		for _, key := range d.keys {
			if _, held := index[key]; held {
				p := d.walk.entities[key]
				mergeEntity(c, index, ResolvedRef{URI: key.URI, ReferencedFrom: p.sites(), KindHint: p.kindHint})
				continue
			}
			unresolved = append(unresolved, key)
		}
		if len(unresolved) == 0 {
			continue
		}
		if !settled {
			requests = selectInstanceVersions(ctx, lookup, c.manifest, c.Instances, c.Entities)
			settled = true
		}
		if err := d.walk.resolveKeys(ctx, lookup, unresolved, requests); err != nil {
			return err
		}
		for _, key := range unresolved {
			mergeEntity(c, index, d.walk.entities[key].toResolvedRef(key.URI))
		}
	}
	return nil
}

// moduleWalk is what the walk of one loaded module recorded: the instances it
// declares, named as inside the module, the modules it includes, and the file
// its instances are written in.
type moduleWalk struct {
	instances []InstanceReference
	modules   []moduleInclusion
	file      string
}

// moduleFile names the document a loaded module's instances are written in: the
// module's reference at the version it resolved to, or as written where it
// resolved to none.
func moduleFile(f descentFrame) string {
	if f.version == "" {
		return f.uri
	}
	return types.ParseVersionPin(f.uri).Base() + ":" + f.version
}

// moduleInstances returns the instances of each module in modules, included
// under prefix, and of the modules each of them includes, depth first and in
// author order. A component is named by the identity the composition gives it
// there; a feature keeps the name its attachment is written under. onPath holds
// the modules of the include chain being followed, so an include leading back
// into that chain is not followed again: the descent has already stopped such a
// chain without a refusal of its own, and the composition refuses it where it
// flattens the Aggregate.
func moduleInstances(modules []moduleInclusion, prefix string, walked map[string]moduleWalk, onPath map[string]bool) []InstanceReference {
	var out []InstanceReference
	for _, m := range modules {
		w, loaded := walked[m.uri]
		if !loaded || onPath[m.uri] {
			continue
		}
		modulePrefix := types.JoinQualifiedID(prefix, m.name)
		for _, in := range w.instances {
			if !in.Feature {
				in.Name = types.JoinQualifiedID(modulePrefix, in.Name)
			}
			in.File = w.file
			in.document = documentNamed
			out = append(out, in)
		}
		onPath[m.uri] = true
		out = append(out, moduleInstances(w.modules, modulePrefix, walked, onPath)...)
		delete(onPath, m.uri)
	}
	return out
}

// mergeReferencedFrom unions two referenced-from path lists, dropping
// duplicates. Order is left to canonicaliseClosure's per-entity sort, so the
// union is itself order-independent.
func mergeReferencedFrom(existing, add []string) []string {
	seen := make(map[string]struct{}, len(existing)+len(add))
	out := make([]string, 0, len(existing)+len(add))
	for _, paths := range [][]string{existing, add} {
		for _, p := range paths {
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	return out
}

// descentFrame is one queued sub-aggregate to load and re-walk: its resolved
// URI and version (which the loader reconstructs into the name:version cache
// key for a bare-name sub), the pin digest the loader verifies cached bytes
// against, and the transitive depth for the maxSubAggregateDepth backstop.
type descentFrame struct {
	uri     string
	version string
	digest  string
	depth   int
}
