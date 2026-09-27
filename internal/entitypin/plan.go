// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package entitypin

import (
	"errors"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/dryrunreport"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// PlanInputs is what one run of the pin step reads.
//
// It is not named Inputs, the compiler pipeline's own inputs type: the pin step
// drives no pipeline and resolves no plugin, so it has no plugin pin source to
// name, and a type sharing that name would be read as a pipeline construction by
// every check that matches the pipeline's inputs by name.
//
// View is the catalog the entities are read through, and the one whose walk
// decides which of them has a writable home. URIs names the entities to
// consider; a URI that is not a Recipe or FeatureRecipe, or that has no writable
// home in View, is passed over. Resolver acquires each source the step pins, and
// WorkspaceRoot and CacheDir are where it stores what it fetched.
//
// ForWriting says the caller will write the plan, or report what writing it
// would do, rather than only hold the locks on disk to it. A plan for writing
// claims each entity's lock file, so every entity it covers is first held to
// having a directory of its own — see [RefuseSharedDirectory] — before anything
// is resolved; a plan computed only to be checked claims nothing and is not held
// to that, which is what keeps a pull that does not pin from refusing a
// directory it writes nothing into. Only a plan computed for writing can be
// written or reported.
type PlanInputs struct {
	View          *catalog.LazyLookup
	URIs          []string
	Resolver      closure.SourceResolver
	WorkspaceRoot string
	CacheDir      string
	ForWriting    bool
}

// Plan is what the pin step would record for each entity it was given, computed
// once and then either checked, reported or written.
//
// The three are one computation read three ways, so a run that reports what it
// would write and a run that checks the lock on disk describe the run that
// writes by construction rather than by three computations kept in step.
type Plan struct {
	workspaceRoot string
	entities      []entityPlan

	// view is the catalog the plan was computed over. The rows EntityDigests
	// returns are read through it, so they are the rows a closure resolved
	// through the same view holds for the same entities.
	view *catalog.LazyLookup

	// documents maps every URI the plan resolved to an entity with a writable
	// home to that entity's document, so the row a lockfile records for the URI can
	// be taken over the entity as the step leaves it (EntityDigests).
	documents map[string]string

	// forWriting records that the plan was computed for writing, so each of its
	// entities was held to a directory of its own before it was planned. Write
	// and Report refuse a plan without it rather than write a lock no such check
	// preceded.
	forWriting bool
}

// errPlanNotForWriting is the refusal Write and Report give a plan computed only
// to be checked. It is a fault in how a caller assembled the step, not anything
// an author can cause.
var errPlanNotForWriting = errors.New("internal: the pin step's plan was computed to be checked, not written; " +
	"compute it with ForWriting so each entity is first held to a directory of its own")

// entityPlan is one entity the step would pin: the document it was read from,
// the kind and name that identify it to a reader, the pins its inputs need now
// and the pins its lock records for them — a lock's local source pins only where
// the entity declares a local source for them to pin.
type entityPlan struct {
	path     string
	kind     string
	name     string
	want     recipepin.Pins
	recorded recipepin.Pins
}

// Outcome reports what the step did, or would do, to one entity's lock.
type Outcome struct {
	// EntityPath is the entity document the lock sits beside.
	EntityPath string
	// LockPath is the lock the step writes.
	LockPath string
	// Kind and Name identify the entity.
	Kind string
	Name string
	// Changed reports that the lock on disk moved, or would move on a run that
	// reports rather than writes.
	Changed bool
	// Removed reports that the move is a removal: the entity reads nothing the
	// lock records a pin for, so no file is left beside it.
	Removed bool
	// Pins counts the pins the lock holds after the step, class by class.
	Pins []recipepin.ClassCount
}

// Compute runs the pin step's resolution over the entities in, and returns the
// plan it would record.
//
// Every entity is read through in.View, and the entities sharing one document —
// a Recipe named by its bare name and by its capability-qualified one is two URIs
// over one file — are planned once, under the first of their URIs. The sources
// are resolved with no Release in reach, each authored-identity source once per
// entity; see [closure.DescendAuthoredSources]. Which of the resolved sources
// belong beside which entity is read off each back-pointer's own record of the
// route its source took, so this pass acts on the descent's reading rather than
// reading the source a second time.
//
// A failure to acquire a source, or to read an imported file, is returned as is:
// nothing is written by Compute, so a run that stops here has changed nothing.
//
// A plan computed for writing refuses, before any source is resolved, every
// entity that shares its directory with another entity where its kind has a
// directory of its own, all of them in one run. The refusal comes first because
// it is free and local, and because resolving a source for an entity that could
// never be pinned would spend a fetch on a verdict already decided — or, for a
// source that does not resolve, report the fetch instead of the rule.
func Compute(in PlanInputs) (Plan, error) {
	plan := Plan{workspaceRoot: in.WorkspaceRoot, view: in.View, documents: map[string]string{}, forWriting: in.ForWriting}
	loader := in.View.ClosureRecipeLoader()

	type homed struct {
		uri, kindHint string
		plan          entityPlan
		artifacts     types.OrderedMap[types.ArtifactEntry]
	}
	var planned []homed
	var refused []error
	byPath := map[string]int{}
	seen := map[string]bool{}
	for _, uri := range in.URIs {
		if seen[uri] {
			continue
		}
		seen[uri] = true

		var (
			kind, kindHint, name, path string
			artifacts                  types.OrderedMap[types.ArtifactEntry]
			ok                         bool
		)
		if r, err := loader.LoadRecipe(uri); err == nil {
			kind, kindHint, name, artifacts = "Recipe", "recipe", r.Metadata.Name, r.Spec.Artifacts
			path, ok = WritableHome(in.View, kind, r.Spec.Capability, r.Metadata.Name, r.Metadata.Version)
		} else if fr, err := loader.LoadFeatureRecipe(uri); err == nil {
			kind, kindHint, name, artifacts = "FeatureRecipe", "feature_recipe", fr.Metadata.Name, fr.Spec.Artifacts
			path, ok = WritableHome(in.View, kind, fr.Spec.Capability, fr.Metadata.Name, fr.Metadata.Version)
		}
		if !ok {
			continue
		}
		plan.documents[uri] = path
		if _, dup := byPath[path]; dup {
			continue
		}
		if in.ForWriting {
			if err := RefuseSharedDirectory(path); err != nil {
				refused = append(refused, err)
				continue
			}
		}
		byPath[path] = len(planned)
		planned = append(planned, homed{
			uri:       uri,
			kindHint:  kindHint,
			artifacts: artifacts,
			plan:      entityPlan{path: path, kind: kind, name: name, recorded: in.View.RecordedPins().For(path)},
		})
	}
	// One refusal is returned as itself, so the diagnostic a reader meets is the
	// one a publication gives over the same document; several are reported
	// together, as the unpinned-entity halt reports every entity it finds.
	switch len(refused) {
	case 0:
	case 1:
		return Plan{}, refused[0]
	default:
		return Plan{}, errors.Join(refused...)
	}

	var cl closure.Closure
	for _, h := range planned {
		cl.Entities = append(cl.Entities, closure.ResolvedRef{URI: h.uri, KindHint: h.kindHint})
	}
	srcArts, err := closure.DescendAuthoredSources(cl, loader, in.Resolver, in.WorkspaceRoot, in.CacheDir)
	if err != nil {
		return Plan{}, err
	}
	srcByURI := sourcePinsByEntity(srcArts)

	for _, h := range planned {
		artPins, err := recipepin.ArtifactPinsForRecipe(h.artifacts, h.plan.kind, h.plan.name)
		if err != nil {
			return Plan{}, err
		}
		// A local source is pinned file by file over the files the entity's
		// declared set says it contributes, which is what a publication of the
		// entity carries for it and what a build compares. The lock's local
		// source pins are held to it only where the entity declares one.
		local, err := catalog.LocalSourceAt(h.plan.path)
		if err != nil {
			return Plan{}, err
		}
		h.plan.want = recipepin.Pins{SourceDigests: srcByURI[h.uri], ArtifactDigests: artPins, LocalSourceDigests: local.Pins}
		h.plan.recorded.LocalSourceDigests = local.RecordedPinsInForce(h.plan.recorded.LocalSourceDigests)
		plan.entities = append(plan.entities, h.plan)
	}
	sort.Slice(plan.entities, func(i, j int) bool { return plan.entities[i].path < plan.entities[j].path })
	return plan, nil
}

// sourcePinsByEntity routes each resolved source to the entities whose own
// record of it says its identity was written out in full, and returns the pins
// keyed by the entity URI that reached them.
//
// That record is the routing reading, and it is carried here rather than taken
// again because it was taken once, by the descent, before anything was acquired:
// a source whose identity is still template text names an artifact only per
// Release, and a file every Release shares cannot hold a value only one of them
// decided. An artifact carries one back-pointer per instance that reached it, so
// an entity is handed each artifact once however many of its back-pointers name
// it.
func sourcePinsByEntity(srcArts []closure.SourceArtifact) map[string][]types.SourceDigest {
	out := map[string][]types.SourceDigest{}
	handed := map[string]map[int]bool{}
	for i, sa := range srcArts {
		pin := types.SourceDigest{
			Kind:           sa.Kind,
			URL:            sa.URL,
			Chart:          sa.Chart,
			ModuleSubdir:   sa.ModuleSubdir,
			Version:        sa.PinnedVersion,
			Digest:         sa.Digest,
			ManifestDigest: sa.ManifestDigest,
		}
		for _, ref := range sa.ReferencedBy {
			if !ref.IdentityAuthored || handed[ref.URI][i] {
				continue
			}
			if handed[ref.URI] == nil {
				handed[ref.URI] = map[int]bool{}
			}
			handed[ref.URI][i] = true
			out[ref.URI] = append(out[ref.URI], pin)
		}
	}
	return out
}

// WritableHome reports the document an entity of kind and identity was authored
// in, where the catalog view holds a writable file for it, and whether it holds
// one.
//
// It is the one predicate the pin step's restriction to an entity with a
// writable home reads, for every caller. The meaning of "local" it answers is
// the catalog's — whether the directory walk recorded a file for the entity — and
// never the reference-text meaning a source's own spelling carries: an entity
// with a remote source can have a writable home, and an entity with a local
// source can have none. An entity a resolution reached through the artifact
// cache answers false, because its slot holds what a fetch placed there under the
// entity's coordinate and version — no document its author edits or commits; an
// entity a caller names by its document answers true, because the view the step
// reads it through walked that document's own directory.
func WritableHome(view *catalog.LazyLookup, kind, capability, name, version string) (string, bool) {
	switch kind {
	case "Recipe":
		return view.LocalRecipePath(capability, name, version)
	case "FeatureRecipe":
		return view.LocalFeatureRecipePath(capability, name, version)
	}
	return "", false
}

// EntityDigests returns, for every URI the plan resolved to an entity with a
// writable home, the digest the lockfile's entity row records for that entity:
// over what a publication of it would carry, the lock beside it included, read
// through the view the plan was computed over (catalog.LazyLookup.EntityRowDigestAt).
//
// The lock is one of the files the digest covers, so the digest is taken after
// the step has done whatever it does to the lock. After Write, and where the step
// only holds the locks on disk to its pins, the view reads each lock from disk. A
// run that reports rather than writes installs its report on the view first
// (catalog.LazyLookup.SetUnwrittenLocks, over Unwritten), so the view reads each
// lock as the writing run would leave it. Either way the row is the one a closure
// resolved through the same view holds for the entity — which is what a build of
// the same Release compares it with — because both are one reading of one view.
func (p Plan) EntityDigests() (map[string]string, error) {
	byPath := map[string]string{}
	out := make(map[string]string, len(p.documents))
	for uri, path := range p.documents {
		d, ok := byPath[path]
		if !ok {
			var err error
			if d, err = p.view.EntityRowDigestAt(path); err != nil {
				return nil, err
			}
			byPath[path] = d
		}
		out[uri] = d
	}
	return out, nil
}

// Unwritten returns what Report reported as the locks a catalog view reads in
// place of the files on disk (catalog.UnwrittenLocks): keyed by the entity
// document each lock sits beside, the bytes Report names for a lock that would
// move, and nil for one the step would remove. A lock the step would leave as it
// is has no entry, because the file on disk already holds what a writing run
// would leave there.
//
// It reads Report's own outcomes and files rather than previewing any lock again,
// so the lock an entity row is taken over is, byte for byte, the lock the report
// shows.
func Unwritten(outcomes []Outcome, files []dryrunreport.File) catalog.UnwrittenLocks {
	content := make(map[string][]byte, len(files))
	for _, f := range files {
		content[f.Path] = f.Content
	}
	out := catalog.UnwrittenLocks{}
	for _, o := range outcomes {
		if !o.Changed {
			continue
		}
		if o.Removed {
			out[o.EntityPath] = nil
			continue
		}
		out[o.EntityPath] = content[o.LockPath]
	}
	return out
}

// Unpinned returns the halt for every entity whose lock does not record, at the
// value the step would record it, a pin for each input the entity reads — and
// nil where every lock already does.
//
// Every such entity is reported in one run, in document order, and each report
// names every input it is missing and the class that input belongs to, so one
// halt hands the reader the whole list of what to pin. remedy is what the
// operation holding the lock can do instead of halting, in its own words; it
// changes the advice the halt ends with and nothing the halt found.
func (p Plan) Unpinned(remedy recipepin.Remedy) error {
	var findings []error
	for _, e := range p.entities {
		missing := recipepin.UnrecordedPins(e.want, e.recorded)
		if len(missing) == 0 {
			continue
		}
		findings = append(findings, recipepin.UnpinnedEntity(e.path,
			catalog.RelativeToWorkspace(p.workspaceRoot, e.path), e.kind, e.name, missing, remedy))
	}
	return errors.Join(findings...)
}

// Write records every entity's pins in the lock beside it, and reports what
// moved.
//
// An entity whose lock already holds exactly what the step would write is left
// byte-identical, and an entity that reads nothing a lock records a pin for has
// its lock removed rather than emptied.
func (p Plan) Write() ([]Outcome, error) {
	if !p.forWriting {
		return nil, errPlanNotForWriting
	}
	out := make([]Outcome, 0, len(p.entities))
	for _, e := range p.entities {
		changed, err := recipepin.WriteLock(e.path, e.want)
		if err != nil {
			return out, err
		}
		out = append(out, e.outcome(changed))
	}
	return out, nil
}

// Report returns, without writing anything, the outcome for every entity and the
// file each lock that would move would hold, ordered by path.
//
// A lock the step would leave byte-identical is absent from the files, because
// naming it would tell a reader a write was coming for it. A lock the step would
// remove is named with no content.
//
// Every entity in a plan for writing has a directory of its own, so no two of
// them name one lock. The report is still keyed by lock path, naming each lock's
// final content, so that what it shows is what a writing run leaves behind
// without that depending on the directory rule having held.
func (p Plan) Report() ([]Outcome, []dryrunreport.File, error) {
	if !p.forWriting {
		return nil, nil, errPlanNotForWriting
	}
	outcomes := make([]Outcome, 0, len(p.entities))
	type last struct {
		content []byte
		moves   bool
	}
	final := map[string]last{}
	var locks []string
	for _, e := range p.entities {
		content, moves, err := recipepin.PreviewLock(e.path, e.want)
		if err != nil {
			return nil, nil, err
		}
		outcomes = append(outcomes, e.outcome(moves))
		lock := recipepin.LockPath(e.path)
		if _, seen := final[lock]; !seen {
			locks = append(locks, lock)
		}
		final[lock] = last{content: content, moves: moves}
	}
	var files []dryrunreport.File
	for _, lock := range locks {
		if f := final[lock]; f.moves {
			files = append(files, dryrunreport.File{Path: lock, Content: f.content})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return outcomes, files, nil
}

// outcome describes this entity's lock after a step that did, or would, move it
// when changed says so.
func (e entityPlan) outcome(changed bool) Outcome {
	return Outcome{
		EntityPath: e.path,
		LockPath:   recipepin.LockPath(e.path),
		Kind:       e.kind,
		Name:       e.name,
		Changed:    changed,
		Removed:    changed && e.want.Empty(),
		Pins:       recipepin.CountPins(e.want),
	}
}
