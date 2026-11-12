// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package entitypin

import (
	"errors"
	"path/filepath"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/dryrunreport"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// PlanInputs is what one run of the pin step reads.
//
// It is not named Inputs, the compiler pipeline's own inputs type: the pin step
// drives no pipeline and loads no plugin, so it has no plugin pin source to name,
// and a type sharing that name would be read as a pipeline construction by every
// check that matches the pipeline's inputs by name. The one plugin fact it reads —
// the ignore rules a serving provider's entity declares — comes through
// ServingProvider, below.
//
// View is the catalog the entities are read through, and the one whose walk
// decides which of them has a writable home. Entities names the entities to
// consider, each by its kind and reference: a Recipe is read as a Recipe and a
// FeatureRecipe as a FeatureRecipe, so the two of one name are two entities, and an
// entity of any other kind, or one with no writable home in View, is passed over.
// Resolver acquires each source the step pins, and WorkspaceRoot and CacheDir are
// where it stores what it fetched.
//
// ForWriting says the caller will write the plan, or report what writing it
// would do, rather than only hold the locks on disk to it. A plan for writing
// claims each entity's lock file, so every entity it covers is first held to
// having a directory of its own — see [RefuseSharedDirectory] — before anything
// is resolved; a plan computed only to be checked claims nothing and is not held
// to that, which is what keeps a pull that does not pin from refusing a
// directory it writes nothing into. Only a plan computed for writing can be
// written or reported.
//
// ServingProvider resolves the plugin serving a capability in the workspace the
// step runs in — its provider binding, or else the plugin built into the binary —
// and is asked only by a plan for writing, for an entity whose local source is a
// directory: that plan composes the plugin's ignore rules with the author's
// .aggregaignore and records them, with the plugin, in the lock. A plan computed
// only to be checked applies the rules the lock already records and asks no
// plugin, so a binding changed or a binary upgraded since the pin moves nothing it
// compares. Nil records no plugin and composes the author's rules alone.
//
// NamedDirs are the catalog directories of View's stack that somebody named — the
// directory of a document named by path, a directory typed on the command line, a
// directory the configuration lists — as opposed to the workspace's conventional
// one where nobody named it; the same list View was built with. A lock beside an
// entity found beneath a named directory is held to the root that directory picks
// ([workspaceconfig.CatalogDirRoot]): the workspace root where it lies inside the
// workspace, and the directory itself where it lies outside it. A lock beside any
// other entity is held to WorkspaceRoot.
type PlanInputs struct {
	View            *catalog.LazyLookup
	Entities        []closure.EntityKey
	Resolver        closure.SourceResolver
	WorkspaceRoot   string
	CacheDir        string
	ForWriting      bool
	ServingProvider func(capability string) (catalog.ServingProvider, bool, error)
	NamedDirs       []string
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

	// documents maps the identity of every entity the plan resolved to a writable
	// home — its kind and the URI it was asked for — to that entity's document, so
	// the row a lockfile records for exactly that entity can be taken over it as the
	// step leaves it (EntityDigests).
	documents map[closure.EntityKey]string

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
// the entity declares a local source for them to pin — and the root its lock is
// held to.
type entityPlan struct {
	path     string
	kind     string
	name     string
	want     recipepin.Pins
	recorded recipepin.Pins
	lockRoot string
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
// Every entity is read through in.View as the kind it is named with, and the
// entities sharing one document — a Recipe named by its bare name and by its
// capability-qualified one is two URIs over one file — are planned once, under the
// first of their URIs. The sources
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
	plan := Plan{workspaceRoot: in.WorkspaceRoot, view: in.View, documents: map[closure.EntityKey]string{}, forWriting: in.ForWriting}
	loader := in.View.ClosureRecipeLoader()

	type homed struct {
		key        closure.EntityKey
		capability string
		plan       entityPlan
		artifacts  types.OrderedMap[types.ArtifactEntry]
	}
	var planned []homed
	var refused []error
	byPath := map[string]int{}
	seen := map[closure.EntityKey]bool{}
	for _, key := range in.Entities {
		if seen[key] {
			continue
		}
		seen[key] = true

		local, ok := LocalEntityFor(in.View, loader, key)
		if !ok {
			continue
		}
		path := local.Path
		plan.documents[key] = path
		if _, dup := byPath[path]; dup {
			continue
		}
		root := lockRoot(in, path)
		if in.ForWriting {
			if err := RefuseSharedDirectory(root, path); err != nil {
				refused = append(refused, err)
				continue
			}
		}
		byPath[path] = len(planned)
		planned = append(planned, homed{
			key:        key,
			capability: local.Capability,
			artifacts:  local.Artifacts,
			plan: entityPlan{path: path, kind: local.Kind, name: local.Name, recorded: in.View.RecordedPins().For(path),
				lockRoot: root},
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
		// A Recipe's and a FeatureRecipe's collection-site hint is the kind's own
		// word, so the key's kind is the hint the descent reads the entity by.
		cl.Entities = append(cl.Entities, closure.ResolvedRef{URI: h.key.URI, KindHint: h.key.Kind})
	}
	srcArts, err := closure.DescendAuthoredSources(cl, loader, in.Resolver, in.WorkspaceRoot, in.CacheDir)
	if err != nil {
		return Plan{}, err
	}
	srcByEntity := sourcePinsByEntity(srcArts)

	for _, h := range planned {
		artPins, err := recipepin.ArtifactPinsForRecipe(h.artifacts, h.plan.kind, h.plan.name)
		if err != nil {
			return Plan{}, err
		}
		// A local source is pinned file by file over the files the entity's
		// declared set says it contributes, which is what a publication of the
		// entity carries for it and what a build compares. The lock's local
		// source pins are held to it only where the entity declares one.
		//
		// Which files a local source directory contributes is decided by ignore
		// rules. A plan for writing composes them afresh — the serving plugin's,
		// then the author's — and pins the source under them, so the lock it
		// writes records the rules its own pins were taken under. A plan only
		// checked applies the rules the lock records, as every other reading of
		// the source does.
		ignore := h.plan.recorded.Ignore
		var local catalog.LocalSource
		if in.ForWriting {
			capability := h.capability
			serving := func() (*catalog.ServingProvider, error) {
				if in.ServingProvider == nil {
					return nil, nil
				}
				served, found, err := in.ServingProvider(capability)
				if err != nil || !found {
					return nil, err
				}
				return &served, nil
			}
			if ignore, err = catalog.ComposeIgnore(h.plan.path, serving); err != nil {
				return Plan{}, err
			}
			local, err = catalog.LocalSourceUnder(h.plan.path, ignore)
		} else {
			local, err = catalog.LocalSourceAt(h.plan.path)
		}
		if err != nil {
			return Plan{}, err
		}
		h.plan.want = recipepin.Pins{SourceDigests: srcByEntity[h.key], ArtifactDigests: artPins, LocalSourceDigests: local.Pins,
			Ignore: ignore}
		h.plan.recorded.LocalSourceDigests = local.RecordedPinsInForce(h.plan.recorded.LocalSourceDigests)
		plan.entities = append(plan.entities, h.plan)
	}
	sort.Slice(plan.entities, func(i, j int) bool { return plan.entities[i].path < plan.entities[j].path })
	return plan, nil
}

// lockRoot returns the root the lock beside the entity document at path is held
// to: the root the first of in.NamedDirs that path lies beneath picks, by the one
// answer for a catalog directory (workspaceconfig.CatalogDirRoot, handed
// in.NamedDirs as the named part of the stack) — the same root the lookup that
// found the entity read it under — and the workspace root where it lies beneath
// none of them.
//
// Which named directory a document lies beneath is a question of its spelling,
// and rightly so: the catalog walk recorded path by joining the directory it
// walked with the names beneath it, so the directory that spells it is the one
// the entity was found in. The stack is first-win in the order NamedDirs keeps,
// so the first directory that spells it is the one whose walk read it.
func lockRoot(in PlanInputs, path string) string {
	for _, dir := range in.NamedDirs {
		if _, beneath := pathscope.Relative(dir, path); beneath {
			return workspaceconfig.CatalogDirRoot(in.WorkspaceRoot, dir, in.NamedDirs)
		}
	}
	return in.WorkspaceRoot
}

// NamedDocumentRoot returns the root what is written and read beside the entity
// document at entityPath is held to, where the document was named by path rather
// than found beneath a catalog directory, in the workspace at workspaceRoot: the
// document's own directory is then the directory somebody named, so the root is the
// one that directory picks — the workspace root where it lies inside the workspace,
// and the directory itself where it lies outside.
//
// It is the root the pin step holds the lock beside such a document to, asked
// through lockRoot over the one named directory the step's view of a named document
// reads (namedDocumentDirs), so a publication holding what it reads beside the
// document asks the answer the step's write is held to rather than a second one.
func NamedDocumentRoot(workspaceRoot, entityPath string) string {
	abs, err := filepath.Abs(entityPath)
	if err != nil {
		abs = filepath.Clean(entityPath)
	}
	return lockRoot(PlanInputs{WorkspaceRoot: workspaceRoot, NamedDirs: namedDocumentDirs(abs)}, abs)
}

// namedDocumentDirs is the named part of the stack the pin step's view of a document
// named by path reads: the document's own directory, and nothing else.
func namedDocumentDirs(absEntityPath string) []string {
	return []string{filepath.Dir(absEntityPath)}
}

// sourcePinsByEntity routes each resolved source to the entities whose own
// record of it says its identity was written out in full, and returns the pins
// keyed by the identity of the entity that reached them — the kind the
// back-pointer records and its URI, so a Recipe and a FeatureRecipe of one name are
// each handed their own sources.
//
// That record is the routing reading, and it is carried here rather than taken
// again because it was taken once, by the descent, before anything was acquired:
// a source whose identity is still template text names an artifact only per
// Release, and a file every Release shares cannot hold a value only one of them
// decided. An artifact carries one back-pointer per instance that reached it, so
// an entity is handed each artifact once however many of its back-pointers name
// it.
func sourcePinsByEntity(srcArts []closure.SourceArtifact) map[closure.EntityKey][]types.SourceDigest {
	out := map[closure.EntityKey][]types.SourceDigest{}
	handed := map[closure.EntityKey]map[int]bool{}
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
			key := closure.EntityKey{Kind: ref.Entity, URI: ref.URI}
			if !ref.IdentityAuthored || handed[key][i] {
				continue
			}
			if handed[key] == nil {
				handed[key] = map[int]bool{}
			}
			handed[key][i] = true
			out[key] = append(out[key], pin)
		}
	}
	return out
}

// LocalEntity is one Recipe or FeatureRecipe with a writable home in a catalog
// view: the document it was read from, and what a lock beside it pins or is
// compared against. Kind is the catalog kind ("Recipe", "FeatureRecipe"). For a
// Recipe the artifacts are the entries its lock pins, its boundary block's included
// under their pin names (recipepin.PinnedArtifactEntries).
type LocalEntity struct {
	Path       string
	Kind       string
	Name       string
	Capability string
	Artifacts  types.OrderedMap[types.ArtifactEntry]
}

// LocalEntityFor reads the entity key names through loader as the kind the key
// records — a Recipe as a Recipe, a FeatureRecipe as a FeatureRecipe, so the two of
// one name are two entities — and returns it with its writable home in view
// (WritableHome). It reports false for any other kind, for an entity loader cannot
// read, and for one with no writable home.
//
// It is the one reading of which entities a lock sits beside, shared by the step
// that writes the locks (Compute) and every check that holds a lock to its entity,
// so the two cannot come to read different entities for one key.
func LocalEntityFor(view *catalog.LazyLookup, loader closure.RecipeLoader, key closure.EntityKey) (LocalEntity, bool) {
	var e LocalEntity
	var version string
	switch key.Kind {
	case closure.KindRecipe:
		r, err := loader.LoadRecipe(key.URI)
		if err != nil {
			return LocalEntity{}, false
		}
		e = LocalEntity{Name: r.Metadata.Name, Capability: r.Spec.Capability, Artifacts: recipepin.PinnedArtifactEntries(r)}
		version = r.Metadata.Version
	case closure.KindFeatureRecipe:
		fr, err := loader.LoadFeatureRecipe(key.URI)
		if err != nil {
			return LocalEntity{}, false
		}
		e = LocalEntity{Name: fr.Metadata.Name, Capability: fr.Spec.Capability, Artifacts: fr.Spec.Artifacts}
		version = fr.Metadata.Version
	default:
		return LocalEntity{}, false
	}
	e.Kind = catalog.EntityKindForHint(key.Kind)
	path, ok := WritableHome(view, e.Kind, e.Capability, e.Name, version)
	if !ok {
		return LocalEntity{}, false
	}
	e.Path = path
	return e, true
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

// EntityDigests returns, keyed by the identity of every entity the plan resolved
// to a writable home — its kind and the URI it was asked for, the identity its
// lockfile row is found by — the digest that row records for that entity:
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
func (p Plan) EntityDigests() (map[closure.EntityKey]string, error) {
	byPath := map[string]string{}
	out := make(map[closure.EntityKey]string, len(p.documents))
	for key, path := range p.documents {
		d, ok := byPath[path]
		if !ok {
			var err error
			if d, err = p.view.EntityRowDigestAt(path); err != nil {
				return nil, err
			}
			byPath[path] = d
		}
		out[key] = d
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
//
// Before any lock is written or removed, the write asks [Plan.Report] for what it
// would do, and halts where the report halts: a lock that would move and leads out
// of the workspace through a link stops the step with every lock left as it was.
// The write is held by the report itself rather than by a second reading of the
// plan, so a run that reports and a run that writes halt on the same locks by
// construction.
func (p Plan) Write() ([]Outcome, error) {
	if !p.forWriting {
		return nil, errPlanNotForWriting
	}
	if _, _, err := p.Report(); err != nil {
		return nil, err
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
//
// Every lock that would move is held to the workspace: one the step would write
// must not lead out of it through a link, and nor may one it would remove. A lock
// leading out halts the report, as it halts [Plan.Write], which is held by this
// report. A lock the step would leave byte-identical is not judged, because
// nothing would be written to it.
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
		lock := recipepin.LockPath(e.path)
		if moves {
			if err := holdLock(e.lockRoot, lock, content); err != nil {
				return nil, nil, err
			}
		}
		outcomes = append(outcomes, e.outcome(moves))
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

// holdLock holds one lock the step would move to root, given the bytes the step
// would leave there: no bytes is a removal, judged as one, and anything else is a
// write.
func holdLock(root, lock string, content []byte) error {
	if len(content) == 0 {
		return writescope.CheckDelete(root, lock)
	}
	return writescope.CheckWrite(root, lock)
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
