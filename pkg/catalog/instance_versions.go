// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The kinds of constraint an instance's version is held to, one per place a
// constraint is declared.
const (
	// ConstraintType is the instance's type as the Aggregate writes it.
	ConstraintType = "type"
	// ConstraintMappingKey is the Manifest's type-axis key that reached the
	// instance.
	ConstraintMappingKey = "mapping key"
	// ConstraintImplements is the spec.implements of the Recipe the mapping that
	// reached the instance names.
	ConstraintImplements = "implements"
)

// VersionConstraint is one constraint declared on an instance's version: the
// instance it is declared on, where that instance is declared, which kind of
// constraint it is, its value as written, and the file it is written in — the
// Aggregate's file for the type, the Manifest's file for a mapping key, and the
// Recipe's reference as the mapping writes it for an implements.
//
// Site is the locator of the instance's declaration in the document its type is
// written in, such as .spec.components.web, and every constraint declared on one
// instance carries the same Site.
type VersionConstraint struct {
	Instance string
	Site     string
	Kind     string
	Value    string
	File     string
}

// InstanceVersions is the version a Release resolves each component reference
// to, and the constraints that reference's instances declare.
//
// Both are keyed by the reference as written and ordered by the first instance
// that wrote it, in the order the instances were visited. Every instance writing
// one reference resolves to one version, so the table has one row per reference
// rather than per instance. Constraints holds each instance's constraints in turn,
// each instance's opening with its type.
type InstanceVersions struct {
	Selected    types.OrderedMap[string]
	Constraints types.OrderedMap[[]VersionConstraint]
}

// at returns reference at the version the table resolves it to (ReferenceAt),
// and whether the table holds reference at all.
func (t InstanceVersions) at(reference string) (string, bool) {
	version, held := t.Selected.Get(reference)
	if !held {
		return "", false
	}
	return ReferenceAt(reference, version), true
}

// ReferenceAt returns a type reference as an exact reference to version: the
// reference's base, whatever version or range it is written with, then version.
// It is how an instance's type is named at the version the instance compiled at,
// the version an IR node records as its resolved version, so every reader asking
// for that instance's Component asks the same reference. An empty version, an
// entry carrying none, yields the base alone, as no exact reference can name it.
func ReferenceAt(reference, version string) string {
	return versionedSlotURI(types.ParseVersionPin(reference).Base(), version)
}

// InstanceVersionView is what the instance-version table reads of a catalog: the
// versions of a Component's name it can list without asking a registry, the
// Recipe a mapping's reference names, and the extends ancestry of a component type
// at one version. Both catalog views a build reads, the eagerly-loaded *Catalog
// and the *LazyLookup, satisfy it.
type InstanceVersionView interface {
	// ComponentVersions returns the versions of the Component named name the view
	// can list without asking a registry, in the order inVersionOrder gives: every
	// version an eagerly-loaded catalog holds, and every version a lazy lookup's
	// catalog directories hold, which leaves out a version only a cache slot or a
	// registry holds.
	ComponentVersions(name string) []string
	// RecipeByURI resolves a Recipe reference as the mapping lookup the build
	// renders through resolves it.
	RecipeByURI(uri string) (types.Recipe, bool)
	// ComponentAncestry returns the extends ancestry of a component type, as
	// the mapping-consistency judgement reads it.
	ComponentAncestry(componentType string) ([]string, error)
}

// recordedVersions is the optional InstanceVersionView capability of a view that
// holds a reference to the version a lockfile records for it, where none of its
// catalog directories holds the reference's Component: RecordedVersion returns
// that version and the lockfile's file. The version is the reference's one
// candidate, and a conflict over it names the lockfile among the sources.
type recordedVersions interface {
	RecordedVersion(reference string) (version, file string, ok bool)
}

// listedVersions is the optional InstanceVersionView capability of a view that
// lists the versions a registry publishes a Component name at, read where neither
// a catalog directory nor a lockfile holds the reference. A registry holding no
// version of the name lists none; any other failure to list is returned, and it
// settles nothing (versionListingFailure).
type listedVersions interface {
	ListedVersions(name string) ([]string, error)
}

// versionListingFailure is a failure to list the versions a registry publishes a
// Component name at. It is not a refusal of the reference: the settlement stops on
// it, returning it as it is, because a selection over versions read in part would
// select another version with no word said. It reads as the registry's own
// failure (Unwrap).
type versionListingFailure struct{ err error }

func (f *versionListingFailure) Error() string { return f.err.Error() }

func (f *versionListingFailure) Unwrap() error { return f.err }

// candidates are the versions of a reference's Component the table selects among.
// recorded reports that they are the one version a lockfile records the reference
// at, and lockfile names that lockfile's file where the view names one.
type candidates struct {
	versions []string
	recorded bool
	lockfile string
}

// candidateVersions returns the versions of reference's Component the table
// selects among, base being the Component's name: the versions view holds of the
// name; where it holds none, the version a lockfile records the reference at
// (recordedVersions); where none records it, the versions view lists
// (listedVersions).
func candidateVersions(view InstanceVersionView, reference, base string) (candidates, error) {
	if held := view.ComponentVersions(base); len(held) > 0 {
		return candidates{versions: held}, nil
	}
	if recorded, ok := view.(recordedVersions); ok {
		if version, file, ok := recorded.RecordedVersion(reference); ok {
			return candidates{versions: []string{version}, recorded: true, lockfile: file}, nil
		}
	}
	listed, err := listedVersionsOf(view, base)
	return candidates{versions: listed}, err
}

// listedVersionsOf returns the versions view lists of the Component name
// (listedVersions), in the order inVersionOrder gives, and none for a view that
// lists none. A failure to list is returned as a versionListingFailure.
func listedVersionsOf(view InstanceVersionView, name string) ([]string, error) {
	listing, ok := view.(listedVersions)
	if !ok {
		return nil, nil
	}
	listed, err := listing.ListedVersions(name)
	if err != nil {
		return nil, &versionListingFailure{err: err}
	}
	return inVersionOrder(listed), nil
}

// HeldToLockfile returns view holding, where none of its catalog directories
// holds a Component reference's Component, that reference to the version lf's row
// records for it, with file named as where it is written (recordedVersions). It is
// the view a build from a lockfile settles instance versions over: a reference a
// catalog directory holds is selected again from the versions there, and a
// reference only the lockfile's pin reaches — one a registry served — is held to
// its row, which every constraint declared on its instances then judges. A row
// they do not admit is refused with the conflicting-version-constraints condition,
// naming the lockfile; the table never selects past the row. A nil lf leaves view
// as it is.
func HeldToLockfile(view InstanceVersionView, lf *lockfile.Lockfile, file string) InstanceVersionView {
	if lf == nil {
		return view
	}
	held := lockfileHeldView{InstanceVersionView: view, rows: map[string]string{}, file: file}
	for _, e := range lf.Entities {
		if e.Kind == closure.KindComponent && e.ResolvedVersion != "" {
			held.rows[e.URI] = e.ResolvedVersion
		}
	}
	return held
}

// lockfileHeldView is the view HeldToLockfile returns: view, and the version each
// Component reference the lockfile records is recorded at.
type lockfileHeldView struct {
	InstanceVersionView
	rows map[string]string
	file string
}

// RecordedVersion returns the version the lockfile's row records reference at,
// satisfying recordedVersions.
func (v lockfileHeldView) RecordedVersion(reference string) (string, string, bool) {
	version, ok := v.rows[reference]
	return version, v.file, ok
}

// ResolveInstanceVersions resolves each component reference the instances of the
// Release named release write to the highest version every constraint declared
// on its instances admits:
//
//   - the type as written, by the pin grammar (types.ParseVersionPin);
//   - the Manifest's type-axis key that reaches the instance, the one the
//     mapping lookup settles on, asked with the instance's name and type as the
//     build asks it; a key naming no version adds nothing, and a name-axis entry
//     adds no key, because a name says nothing about a type;
//   - the spec.implements of the Recipe the reaching mapping names, judged by the
//     mapping-consistency judgement (resolver.ManifestMappingAccepts) asked with
//     the type at each held version, through the view's extends ancestry, so a
//     Recipe written against a base admits each version whose chain reaches a
//     base version it implements.
//
// The selection is the version select's, under an admission predicate built
// from those constraints, so "highest", the pre-release rule, the tie-break and
// the version-less fallback are the select's own. The versions selected among are
// those the view holds of the type's name; where it holds none, the one version a
// lockfile records the reference at, where the view holds the reference to one
// (HeldToLockfile), which the constraints then judge rather than select past; and
// where no lockfile records it, the versions the view lists from a registry. A
// recorded version the constraints refuse yields to the registry's versions where
// the view lists any, as a pull's does, and is refused where it lists none, as a
// build's is.
//
// Where the versions held include some the reference alone admits and none every
// constraint admits, the reference is refused with the conflicting-version-
// constraints condition, naming the Release, then each instance writing it with
// where it is declared and each constraint with the file it is written in, then
// the versions held — or the version locked and the lockfile it is written in —
// and positioned where the first of those instances is declared. Where a mapping
// key or a spec.implements carries a malformed pin, that pin admits no version, so
// the refusal is the malformed-pin condition naming where the pin is written
// instead.
//
// A type-axis entry whose Recipe's spec.implements does not accept the entry's
// own key — judged as the rule over the mapping entry judges it, through
// resolver.ManifestMappingAccepts — adds neither its key nor its implements: the
// entry disagrees with itself, that rule reports it once as the mapping fault it
// is, and the instance resolves as its other constraints admit. A malformed pin
// on such an entry is that rule's to report too.
// Every refused reference is reported, and the table holds the others. A failure
// to list the versions a registry publishes is no refusal: it is returned alone,
// as the registry's failure, and the table is empty.
//
// A reference the table cannot judge is left out of Selected and resolves as it
// does without the table: one with no version to select among, one no
// held version satisfies on its own pin, and one whose own pin is malformed are
// reported where references are resolved. An instance no mapping reaches carries
// its type alone and resolves as its reference does; one whose mapping lookup
// refuses, as an ambiguous key, carries its type alone too, and the lookup's
// refusal is the build's to report. A mapping whose Recipe the view does not hold
// adds no implements.
//
// Feature instances resolve as their references do, and the table holds no row
// for them.
func ResolveInstanceVersions(release string, instances []closure.InstanceReference, manifest *types.Manifest, manifestFile string, view InstanceVersionView) (InstanceVersions, error) {
	var table InstanceVersions
	for _, in := range instances {
		if in.Feature {
			continue
		}
		declared, _ := table.Constraints.Get(in.Reference)
		table.Constraints.Set(in.Reference, append(declared, componentConstraints(in, manifest, manifestFile, view)...))
	}

	var refusals []error
	for reference, declared := range table.Constraints.All() {
		version, selected, err := selectComponentVersion(release, reference, declared, view)
		var listing *versionListingFailure
		switch {
		case errors.As(err, &listing):
			return InstanceVersions{}, err
		case err != nil:
			refusals = append(refusals, err)
		case selected:
			table.Selected.Set(reference, version)
		}
	}
	return table, errors.Join(refusals...)
}

// componentConstraints returns the constraints declared on one component
// instance, in the order a refusal names them: its type, the type-axis key that
// reached it, and the reaching Recipe's spec.implements.
func componentConstraints(in closure.InstanceReference, manifest *types.Manifest, manifestFile string, view InstanceVersionView) []VersionConstraint {
	site := instanceSite(in.Path)
	declared := []VersionConstraint{{Instance: in.Name, Site: site, Kind: ConstraintType, Value: in.Reference, File: in.File}}
	match, mapped, err := componentMappingEntry(manifest, in.Name, in.Reference)
	if err != nil || !mapped {
		return declared
	}
	recipe, held := view.RecipeByURI(match.reference)
	// A type-axis entry whose Recipe the rule over the mapping entry refuses for
	// the entry's own key is that rule's to report, once: the key and the Recipe
	// disagree with each other whatever the instance writes, so the entry is the
	// fault and it holds the instance to nothing. The judgement is the rule's own,
	// asked as the rule asks it, with the key standing as the request.
	if held && match.byType && !resolver.ManifestMappingAccepts(recipe.Spec.Implements, match.typeKey, false, view.ComponentAncestry) {
		return declared
	}
	if match.byType {
		declared = append(declared, VersionConstraint{
			Instance: in.Name, Site: site, Kind: ConstraintMappingKey, Value: match.typeKey, File: manifestFile,
		})
	}
	if held {
		declared = append(declared, VersionConstraint{
			Instance: in.Name, Site: site, Kind: ConstraintImplements, Value: recipe.Spec.Implements, File: match.reference,
		})
	}
	return declared
}

// instanceSite is the locator of an instance's declaration in its document, read
// off the authoring site of its type as the closure walk records it: the walk
// names the document the type is written in, then the path inside it to the
// instance's key, then the key type — aggregate.spec.components.web.type for the
// instance web of the Release's Aggregate — and the locator is that path inside
// the document, .spec.components.web. A site recording no spec block yields no
// locator.
func instanceSite(path string) string {
	start := strings.Index(path, ".spec.")
	if start < 0 {
		return ""
	}
	return strings.TrimSuffix(path[start:], ".type")
}

// selectComponentVersion selects the version of reference every constraint in
// declared admits, among the versions the view offers it (candidateVersions). It
// reports the version and true on a selection, false with no error for a reference
// the table leaves to its own resolution, and the refusal for one it refuses.
//
// A version a lockfile records, which the constraints do not admit, yields to the
// versions the view lists from a registry where it lists any: a pull resolving a
// reference its prior lockfile pins re-selects a pin its constraints have moved
// away from, while a build, whose view lists none, is held to the row.
func selectComponentVersion(release, reference string, declared []VersionConstraint, view InstanceVersionView) (string, bool, error) {
	base := types.ParseVersionPin(reference).Base()
	offered, err := candidateVersions(view, reference, base)
	if err != nil {
		return "", false, err
	}
	version, selected, err := selectAmong(release, reference, base, declared, view, offered)
	if offered.recorded && errors.Is(err, ErrConflictingVersionConstraints()) {
		listed, lerr := listedVersionsOf(view, base)
		if lerr != nil {
			return "", false, lerr
		}
		if len(listed) > 0 {
			return selectAmong(release, reference, base, declared, view, candidates{versions: listed})
		}
	}
	return version, selected, err
}

// selectAmong is selectComponentVersion over the versions offered.
func selectAmong(release, reference, base string, declared []VersionConstraint, view InstanceVersionView, offered candidates) (string, bool, error) {
	held := offered.versions
	index := make(map[string]string, len(held))
	for _, version := range held {
		index[entityIndexKey(base, version)] = version
	}
	key, selected, err := selectEntityVersionWhere(index, reference,
		func(version string) (string, string) { return base, version },
		admittedBy(base, declared, view.ComponentAncestry))
	switch {
	case selected:
		return index[key], true, nil
	case errors.Is(err, ErrConflictingVersionConstraints()):
		if fault := malformedConstraintPin(declared, reference); fault != nil {
			return "", false, fault
		}
		return "", false, conflictingVersionConstraints(release, declared, held, offered.lockfile)
	default:
		return "", false, nil
	}
}

// admittedBy is the admission predicate the mapping keys and the
// spec.implements values in declared state over the versions of base. The type
// constraints are the reference the select is asked with, and need no predicate.
func admittedBy(base string, declared []VersionConstraint, ancestry func(string) ([]string, error)) func(string) bool {
	return func(version string) bool {
		request := base
		if version != "" {
			request = base + ":" + version
		}
		for _, c := range declared {
			switch c.Kind {
			case ConstraintMappingKey:
				// A key is read as the pin grammar reads a declaration admitting a
				// concrete version: exact, caret range, or naming no version.
				if !resolver.ImplementsSatisfies(c.Value, request) {
					return false
				}
			case ConstraintImplements:
				if !resolver.ManifestMappingAccepts(c.Value, request, false, ancestry) {
					return false
				}
			}
		}
		return true
	}
}

// malformedConstraintPin returns the malformed-pin condition for the first
// mapping key or spec.implements in declared whose pin is malformed, naming where
// it is written, and nil when none is.
func malformedConstraintPin(declared []VersionConstraint, reference string) error {
	for _, c := range declared {
		var where string
		switch c.Kind {
		case ConstraintMappingKey:
			where = fmt.Sprintf("Manifest mapping key for %q, written in %q", reference, c.File)
		case ConstraintImplements:
			where = fmt.Sprintf("spec.implements of recipe %q", c.File)
		default:
			continue
		}
		if pe, malformed := types.ParseVersionPin(c.Value).Malformed(); malformed {
			return pe.At(where)
		}
	}
	return nil
}

// conflictingVersionAdvice is what a conflicting-version-constraints refusal
// tells the author to do.
const conflictingVersionAdvice = "change the Aggregate's version, or map this type at a version its recipe implements"

// lockedVersionAdvice is what a conflicting-version-constraints refusal over a
// version a lockfile records adds: a pull resolves the reference again, and
// records the version its constraints admit.
const lockedVersionAdvice = ", or run `aggrega pull` to lock the version every constraint admits"

// conflictingVersionConstraints is the refusal of a reference that no version the
// catalog holds satisfies under every constraint in declared. It names the Release
// the instances belong to, then each instance in turn — where it is declared, its
// name, and each of its constraints with the file it is written in — then the
// versions held, a version-less entry among them left unnamed because it has no
// version to name — the versions held by a catalog directory or listed by a
// registry. Where the versions are the ones a lockfile records, recordedIn names
// that lockfile, the versions are named as locked there, a source beside the
// constraints, and the advice adds that a pull locks the version the constraints
// admit. It is positioned where the first instance is declared: the
// document its type is written in, at the instance's site, which is where the
// remedy of changing the version is written.
func conflictingVersionConstraints(release string, declared []VersionConstraint, held []string, recordedIn string) error {
	detail := []constraint.DetailValue{constraint.Captioned("Release", release)}
	for _, c := range declared {
		switch c.Kind {
		case ConstraintType:
			detail = append(detail,
				constraint.Captioned("at", c.Site),
				constraint.Captioned("instance", c.Instance),
				constraint.Captioned("type", c.Value),
				constraint.Captioned("written in", c.File))
		case ConstraintMappingKey:
			detail = append(detail,
				constraint.Captioned("Manifest mapping key", c.Value),
				constraint.Captioned("written in", c.File))
		case ConstraintImplements:
			detail = append(detail,
				constraint.Captioned("mapped to recipe", c.File),
				constraint.Captioned("whose spec.implements is", c.Value))
		}
	}
	named := make([]string, 0, len(held))
	for _, version := range held {
		if version != "" {
			named = append(named, version)
		}
	}
	advice := conflictingVersionAdvice
	if recordedIn != "" {
		detail = append(detail,
			constraint.Captioned("locked at", strings.Join(named, ", ")),
			constraint.Captioned("written in", recordedIn))
		advice += lockedVersionAdvice
	} else {
		detail = append(detail, constraint.Captioned("versions held", strings.Join(named, ", ")))
	}

	condition := ErrConflictingVersionConstraints()
	var at cerrors.SourceLocation
	if len(declared) > 0 {
		at = cerrors.SourceLocation{File: declared[0].File, Locator: declared[0].Site, Instance: declared[0].Instance}
	}
	return cerrors.Wrap(condition, cerrors.OpsError, at,
		cerrors.ComposeMessage(condition, constraint.RenderDetail(detail...), advice))
}

// inVersionOrder returns versions in the order a reader is shown them: every
// semantic version in ascending order, then every version that is no semantic
// version, a version-less entry among them, in lexical order. Two spellings of
// one semantic version, differing in build metadata only, keep lexical order
// between them.
func inVersionOrder(versions []string) []string {
	candidates := make([]versionCandidate, 0, len(versions))
	for _, v := range versions {
		c := versionCandidate{key: v, raw: v}
		if parsed, err := semver.NewVersion(v); err == nil {
			c.parsed = parsed
		}
		candidates = append(candidates, c)
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		switch {
		case a.parsed != nil && b.parsed == nil:
			return true
		case a.parsed == nil && b.parsed != nil:
			return false
		case a.parsed != nil && !a.parsed.Equal(b.parsed):
			return a.parsed.LessThan(b.parsed)
		default:
			return a.raw < b.raw
		}
	})
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.raw)
	}
	return out
}

// ComponentVersions returns the version of every Component named name this
// catalog's own Component index holds, in the order inVersionOrder gives.
func (c *Catalog) ComponentVersions(name string) []string {
	var held []string
	for _, comp := range c.Components {
		if comp.Metadata.Name == name {
			held = append(held, comp.Metadata.Version)
		}
	}
	return inVersionOrder(held)
}

// ComponentVersions returns the version of every Component named name this
// view's catalog directories hold, in the order inVersionOrder gives. A version
// only a cache slot or a registry holds is not among them, and reading them asks
// no registry.
func (l *LazyLookup) ComponentVersions(name string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buildComponentIndex()
	var held []string
	for _, doc := range l.componentIndex {
		if doc.name == name {
			held = append(held, doc.version)
		}
	}
	return inVersionOrder(held)
}

// ComponentAncestry returns the extends ancestry of a component type, walked
// over this view's local copies of its Components as the mapping-consistency
// gate walks it, and asking no registry.
func (l *LazyLookup) ComponentAncestry(componentType string) ([]string, error) {
	return l.lockedComponentAncestry(componentType)
}
