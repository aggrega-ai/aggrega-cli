// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/dryrunreport"
	"github.com/aggrega-ai/aggrega-cli/internal/entitypin"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// entityPins is the pin step as a dependency resolution runs it: over every
// Recipe and FeatureRecipe this Release builds that has a writable home, it
// either records the pins in the lock beside each entity or holds the lock on
// disk to them, and it returns the content digest of each such entity's document.
//
// # One step, and this is a caller of it
//
// The computation is the author's own pin step — [entitypin.Compute] — and not a
// second one kept in step with it, so the lock a resolution writes for an entity
// is the lock the author's own run writes for it, byte for byte. What this adds is
// only which entities the step is run over and what is done with its plan.
//
// # Which entities
//
// The ones this Release builds: an entity the closure reaches with at least one
// instance using it. An entity mapped in the Manifest that nothing in this
// Release instantiates is never rendered here, so its pins are no fact about this
// Release and its lock is left exactly as it was. Of those, only an entity with a
// writable home is considered — a document in one of the workspace's catalog
// directories. An entity resolved through the artifact cache has no such home;
// see [entitypin.WritableHome] for why that is the catalog's meaning of
// "local" rather than the one a source's own reference text carries.
//
// # Pin, or hold the lock to the pins
//
// pin says which. When it is set, every lock is brought up to date — or, on a
// reporting run, the bytes it would hold are returned instead. When it is not,
// nothing is written, and an entity whose lock does not record a pin its inputs
// need halts the run, naming the entity, each missing input and the class it
// belongs to, and the step that records them. The two are one plan read two ways,
// so the halt is raised exactly where a pinning run would have written.
//
// Only a pinning run claims the entities' lock files, so only a pinning run holds
// each entity to a directory of its own, before anything is resolved. A pull that
// does not pin writes nothing into any directory, and a Release whose entities
// share one stays pullable and buildable.
//
// # The digests
//
// The entity document is never opened for writing, so its digest is the same
// value whether or not a lock is written beside it. That is what the returned map
// reports, keyed by entity URI, for the pull to overlay onto the matching
// lockfile entity; the overlay is keyed strictly by URI so a lockfile entity of
// another kind that happens to share the recipe's bare name is never touched.
func entityPins(lazyView *catalog.LazyLookup, entities []closure.ResolvedRef, used map[string][]closure.ComponentSourceInfra,
	resolver closure.SourceResolver, workspaceRoot, cacheDir string, pin, reportOnly bool) (map[string]string, []dryrunreport.File, error) {
	var uris []string
	for _, ent := range entities {
		if len(used[ent.URI]) == 0 {
			continue
		}
		uris = append(uris, ent.URI)
	}
	plan, err := entitypin.Compute(entitypin.PlanInputs{
		View:          lazyView,
		URIs:          uris,
		Resolver:      resolver,
		WorkspaceRoot: workspaceRoot,
		CacheDir:      cacheDir,
		ForWriting:    pin,
	})
	if err != nil {
		return nil, nil, err
	}

	var reported []dryrunreport.File
	switch {
	case !pin:
		if err := plan.Unpinned(pullRemedy); err != nil {
			return nil, nil, err
		}
	case reportOnly:
		_, files, rerr := plan.Report()
		if rerr != nil {
			return nil, nil, rerr
		}
		reported = files
	default:
		if _, werr := plan.Write(); werr != nil {
			return nil, nil, werr
		}
	}

	digests := map[string]string{}
	for uri, d := range plan.EntityDigests() {
		if !uriShadowedByComponentOrFeature(lazyView, uri) {
			digests[uri] = d
		}
	}
	return digests, reported, nil
}

// pullRemedy is what the unpinned-entity halt tells a pull's reader the pull can
// do instead of halting. It names the pull's own setting and no per-run request:
// a dependency resolution is reached from a pull and from a build that fills its
// own lockfile, and a request spelled for one of them would name something the
// other cannot send, while the author's step and the setting are true for both.
var pullRemedy = recipepin.Remedy{Operation: "pull", Setting: workspaceconfig.PinSourcesAtPullKey}

// uriShadowedByComponentOrFeature reports whether a recipe URI also resolves to a
// Component or Feature. The closure resolves an entity URI component-first, then
// feature, then recipe — so when a bare recipe name is shared with a Component or
// Feature of the same identity, the lockfile entity for that URI tracks the
// shadowing Component/Feature bytes, not the recipe's. Such an entity's recorded
// digest must NOT be overlaid with the recipe file's digest — doing so would
// fabricate drift the build would then flag. Only a URI that resolves solely to
// the recipe carries the recipe's bytes in the lockfile, and only that entity
// takes the overlay.
//
// It is the caller's rule rather than the pin step's, because it is a fact about
// what the LOCKFILE records rather than about what the step does to a file.
func uriShadowedByComponentOrFeature(lazyView *catalog.LazyLookup, uri string) bool {
	if _, ok := lazyView.Component(uri); ok {
		return true
	}
	if _, ok := lazyView.Feature(uri); ok {
		return true
	}
	return false
}

// onceEachSourceResolver resolves each distinct source spec once per run and
// answers every later request for the same spec with the first answer.
//
// A resolution acquires a source for the Release's own lockfile and then runs the
// pin step, which resolves the authored-identity sources of the entities the
// Release builds a second time, with no Release in reach. Those are the same
// specs — an authored identity carries no template, so it resolves to the same
// request whatever instance asked — and fetching each twice in one run would be
// the same bytes downloaded twice. So the two share this, and a spec is fetched
// once.
//
// It is keyed on every field a resolution reads other than the authored value
// the answer echoes, and it keeps only answers: a failure is returned as it is
// and ends the run, so there is nothing to remember about it.
type onceEachSourceResolver struct {
	next     closure.SourceResolver
	resolved map[string]closure.ResolvedSource
}

func resolveEachSourceOnce(next closure.SourceResolver) *onceEachSourceResolver {
	return &onceEachSourceResolver{next: next, resolved: map[string]closure.ResolvedSource{}}
}

func (o *onceEachSourceResolver) Resolve(spec closure.SourceSpec) (*closure.ResolvedSource, error) {
	key := strings.Join([]string{spec.Kind, spec.URL, spec.Version, spec.Chart, spec.ModuleSubdir, spec.Ref,
		spec.WorkspaceRoot, spec.CacheDir}, "\x00")
	if prior, ok := o.resolved[key]; ok {
		answer := prior
		answer.OriginalSource = spec.OriginalSource
		return &answer, nil
	}
	res, err := o.next.Resolve(spec)
	if err != nil {
		return nil, err
	}
	o.resolved[key] = *res
	return res, nil
}
