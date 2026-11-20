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
//
// Feature references resolve in a table of their own, FeatureSelected and
// FeatureConstraints, ordered the same way: a Component and a Feature written with
// one reference string are two references and never share a row.
type InstanceVersions struct {
	Selected    types.OrderedMap[string]
	Constraints types.OrderedMap[[]VersionConstraint]

	FeatureSelected    types.OrderedMap[string]
	FeatureConstraints types.OrderedMap[[]VersionConstraint]

	// Newest and FeatureNewest hold, for each reference Selected and FeatureSelected
	// hold, the highest version the reference as written admits among the versions
	// the selection was made over, whatever the constraints declared on its
	// instances admit: the newest version held. A reference selected below its
	// newest is held there by the constraints that exclude it (HeldBelowNewest).
	Newest        types.OrderedMap[string]
	FeatureNewest types.OrderedMap[string]
}

// at returns the component reference at the version the table resolves it to
// (ReferenceAt), and whether the table holds that component reference at all.
func (t InstanceVersions) at(reference string) (string, bool) {
	return atVersion(t.Selected, reference)
}

// featureAt is at for a feature reference.
func (t InstanceVersions) featureAt(reference string) (string, bool) {
	return atVersion(t.FeatureSelected, reference)
}

func atVersion(selected types.OrderedMap[string], reference string) (string, bool) {
	version, held := selected.Get(reference)
	if !held {
		return "", false
	}
	return ReferenceAt(reference, version), true
}

// versionTier is the kind of entity a table row resolves: the components tier or
// the features tier. The features tier judges a FeatureRecipe's implements without
// extends ancestry, as the mapping-consistency rule does for the features mapping.
type versionTier struct{ feature bool }

var (
	componentTier = versionTier{}
	featureTier   = versionTier{feature: true}
)

// kind is the closure kind of the entities the tier resolves.
func (k versionTier) kind() string {
	if k.feature {
		return closure.KindFeature
	}
	return closure.KindComponent
}

// versions returns the versions of the entity named name the view holds.
func (k versionTier) versions(view InstanceVersionView, name string) []string {
	if k.feature {
		return view.FeatureVersions(name)
	}
	return view.ComponentVersions(name)
}

// mappingEntry is the entry the Manifest's mapping for the tier settles on for an
// instance, as the build asks it.
func (k versionTier) mappingEntry(manifest *types.Manifest, name, reference string) (mappingMatch, bool, error) {
	if k.feature {
		return featureMappingEntry(manifest, name, reference)
	}
	return componentMappingEntry(manifest, name, reference)
}

// implements returns the spec.implements of the Recipe or FeatureRecipe a mapping
// names, and whether the view holds it.
func (k versionTier) implements(view InstanceVersionView, reference string) (string, bool) {
	if k.feature {
		recipe, held := view.FeatureRecipeByURI(reference)
		return recipe.Spec.Implements, held
	}
	recipe, held := view.RecipeByURI(reference)
	return recipe.Spec.Implements, held
}

// mappingKeyLabel names the Manifest key of the tier in a refusal.
func (k versionTier) mappingKeyLabel() string {
	if k.feature {
		return "feature key"
	}
	return "mapping key"
}

// recipeLabel names the kind of recipe a mapping of the tier names in a refusal.
func (k versionTier) recipeLabel() string {
	if k.feature {
		return "feature recipe"
	}
	return "recipe"
}

// accepts is the mapping-consistency judgement of the tier: a component type is
// judged through the view's extends ancestry, a feature type without.
func (k versionTier) accepts(view InstanceVersionView, implements, request string) bool {
	if k.feature {
		return resolver.ManifestMappingAccepts(implements, request, true, nil)
	}
	return resolver.ManifestMappingAccepts(implements, request, false, view.ComponentAncestry)
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
// versions of a Component's or a Feature's name it can list without asking a
// registry, the Recipe or FeatureRecipe a mapping's reference names, and the
// extends ancestry of a component type at one version. Both catalog views a build reads, the eagerly-loaded *Catalog
// and the *LazyLookup, satisfy it.
type InstanceVersionView interface {
	// ComponentVersions returns the versions of the Component named name the view
	// can list without asking a registry, in the order inVersionOrder gives: every
	// version an eagerly-loaded catalog holds, and every version a lazy lookup's
	// catalog directories hold, which leaves out a version only a cache slot or a
	// registry holds.
	ComponentVersions(name string) []string
	// FeatureVersions is ComponentVersions for the Feature named name.
	FeatureVersions(name string) []string
	// RecipeByURI resolves a Recipe reference as the mapping lookup the build
	// renders through resolves it.
	RecipeByURI(uri string) (types.Recipe, bool)
	// FeatureRecipeByURI resolves a FeatureRecipe reference as the features
	// mapping lookup the build renders through resolves it.
	FeatureRecipeByURI(uri string) (types.FeatureRecipe, bool)
	// ComponentAncestry returns the extends ancestry of a component type, as
	// the mapping-consistency judgement reads it.
	ComponentAncestry(componentType string) ([]string, error)
}

// recordedVersions is the optional InstanceVersionView capability of a view that
// holds a reference to the version a lockfile records for it, where none of its
// catalog directories holds the reference's Component or Feature, kind being the
// closure kind of the entity: RecordedVersion returns
// that version and the lockfile it is recorded in. The version is the reference's
// one candidate, and a conflict over it names the lockfile among the sources.
type recordedVersions interface {
	RecordedVersion(kind, reference string) (version string, record lockfileRecord, ok bool)
}

// lockfileRecord names a lockfile a version is recorded in, for a conflict over
// that version to name: the lockfile's file, and the file of the Release it locks,
// which the command that resolves the Release again is run with. Either may be
// empty where the view knows no file to name.
type lockfileRecord struct {
	lockfile string
	release  string
}

// listedVersions is the optional InstanceVersionView capability of a view that
// lists the versions a registry publishes the entity of closure kind kind named
// name at, read where neither
// a catalog directory nor a lockfile holds the reference. A registry holding no
// version of the name lists none; any other failure to list is returned, and it
// settles nothing (versionListingFailure).
type listedVersions interface {
	ListedVersions(kind, name string) ([]string, error)
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
// at, and record names that lockfile.
type candidates struct {
	versions []string
	recorded bool
	record   lockfileRecord
}

// candidateVersions returns the versions of reference's Component the table
// selects among, base being the Component's name: the versions view holds of the
// name; where it holds none, the version a lockfile records the reference at
// (recordedVersions); where none records it, the versions view lists
// (listedVersions).
func candidateVersions(view InstanceVersionView, tier versionTier, reference, base string) (candidates, error) {
	if held := tier.versions(view, base); len(held) > 0 {
		return candidates{versions: held}, nil
	}
	if recorded, ok := view.(recordedVersions); ok {
		if version, record, ok := recorded.RecordedVersion(tier.kind(), reference); ok {
			return candidates{versions: []string{version}, recorded: true, record: record}, nil
		}
	}
	listed, err := listedVersionsOf(view, tier, base)
	return candidates{versions: listed}, err
}

// listedVersionsOf returns the versions view lists of the Component name
// (listedVersions), in the order inVersionOrder gives, and none for a view that
// lists none. A failure to list is returned as a versionListingFailure.
func listedVersionsOf(view InstanceVersionView, tier versionTier, name string) ([]string, error) {
	listing, ok := view.(listedVersions)
	if !ok {
		return nil, nil
	}
	listed, err := listing.ListedVersions(tier.kind(), name)
	if err != nil {
		return nil, &versionListingFailure{err: err}
	}
	return inVersionOrder(listed), nil
}

// HeldToLockfile returns view holding, where none of its catalog directories
// holds a Component reference's Component, that reference to the version lf's row
// records for it, with lockfileFile named as where it is written and releaseFile
// as the Release a pull resolves again (recordedVersions). It is
// the view a build from a lockfile settles instance versions over: a reference a
// catalog directory holds is selected again from the versions there, and a
// reference only the lockfile's pin reaches — one a registry served — is held to
// its row, which every constraint declared on its instances then judges. A row
// they do not admit is refused with the conflicting-version-constraints condition,
// naming the lockfile; the table never selects past the row. A nil lf leaves view
// as it is.
func HeldToLockfile(view InstanceVersionView, lf *lockfile.Lockfile, lockfileFile, releaseFile string) InstanceVersionView {
	if lf == nil {
		return view
	}
	held := lockfileHeldView{InstanceVersionView: view, rows: map[recordedRow]string{},
		record: lockfileRecord{lockfile: lockfileFile, release: releaseFile}}
	for _, e := range lf.Entities {
		if (e.Kind == closure.KindComponent || e.Kind == closure.KindFeature) && e.ResolvedVersion != "" {
			held.rows[recordedRow{kind: e.Kind, reference: e.URI}] = e.ResolvedVersion
		}
	}
	return held
}

// lockfileHeldView is the view HeldToLockfile returns: view, and the version each
// Component reference the lockfile records is recorded at.
type lockfileHeldView struct {
	InstanceVersionView
	rows   map[recordedRow]string
	record lockfileRecord
}

// recordedRow identifies a lockfile row by the kind of entity it records and the
// reference, so a Component and a Feature written alike keep rows of their own.
type recordedRow struct{ kind, reference string }

// RecordedVersion returns the version the lockfile's row records reference at,
// satisfying recordedVersions.
func (v lockfileHeldView) RecordedVersion(kind, reference string) (string, lockfileRecord, bool) {
	version, ok := v.rows[recordedRow{kind: kind, reference: reference}]
	return version, v.record, ok
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
// Feature instances resolve the same way in a table of their own, by the features
// tier: the type as written, the Manifest's features-mapping key that reaches the
// instance, and the spec.implements of the FeatureRecipe the reaching entry names,
// judged by the same mapping-consistency judgement without extends ancestry, as
// the features mapping is judged. A Feature reference and a Component reference
// written alike are two rows.
func ResolveInstanceVersions(release string, instances []closure.InstanceReference, manifest *types.Manifest, manifestFile string, view InstanceVersionView) (InstanceVersions, error) {
	var table InstanceVersions
	for _, in := range instances {
		tier, constraints := componentTier, &table.Constraints
		if in.Feature {
			tier, constraints = featureTier, &table.FeatureConstraints
		}
		declared, _ := constraints.Get(in.Reference)
		constraints.Set(in.Reference, append(declared, tierConstraints(tier, in, manifest, manifestFile, view)...))
	}

	var refusals []error
	for _, tier := range []versionTier{componentTier, featureTier} {
		constraints, selection, newest := &table.Constraints, &table.Selected, &table.Newest
		if tier.feature {
			constraints, selection, newest = &table.FeatureConstraints, &table.FeatureSelected, &table.FeatureNewest
		}
		for reference, declared := range constraints.All() {
			version, highest, selected, err := selectTierVersion(release, tier, reference, declared, view)
			var listing *versionListingFailure
			switch {
			case errors.As(err, &listing):
				return InstanceVersions{}, err
			case err != nil:
				refusals = append(refusals, err)
			case selected:
				selection.Set(reference, version)
				newest.Set(reference, highest)
			}
		}
	}
	return table, errors.Join(refusals...)
}

// tierConstraints returns the constraints declared on one instance of tier, in the
// order a refusal names them: its type, the type-axis key that reached it, and the
// reaching Recipe's or FeatureRecipe's spec.implements.
func tierConstraints(tier versionTier, in closure.InstanceReference, manifest *types.Manifest, manifestFile string, view InstanceVersionView) []VersionConstraint {
	site := instanceSite(in.Path)
	declared := []VersionConstraint{{Instance: in.Name, Site: site, Kind: ConstraintType, Value: in.Reference, File: in.File}}
	match, mapped, err := tier.mappingEntry(manifest, in.Name, in.Reference)
	if err != nil || !mapped {
		return declared
	}
	implements, held := tier.implements(view, match.reference)
	// A type-axis entry whose Recipe the rule over the mapping entry refuses for
	// the entry's own key is that rule's to report, once: the key and the Recipe
	// disagree with each other whatever the instance writes, so the entry is the
	// fault and it holds the instance to nothing. The judgement is the rule's own,
	// asked as the rule asks it, with the key standing as the request.
	if held && match.byType && !tier.accepts(view, implements, match.typeKey) {
		return declared
	}
	if match.byType {
		declared = append(declared, VersionConstraint{
			Instance: in.Name, Site: site, Kind: ConstraintMappingKey, Value: match.typeKey, File: manifestFile,
		})
	}
	if held {
		declared = append(declared, VersionConstraint{
			Instance: in.Name, Site: site, Kind: ConstraintImplements, Value: implements, File: match.reference,
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

// selectTierVersion selects the version of reference of tier every constraint in
// declared admits, among the versions the view offers it (candidateVersions). It
// reports the version, the highest version the reference as written admits among
// the same versions (the newest held), and true on a selection, false with no
// error for a reference the table leaves to its own resolution, and the refusal
// for one it refuses.
//
// A version a lockfile records, which the constraints do not admit, yields to the
// versions the view lists from a registry where it lists any: a pull resolving a
// reference its prior lockfile pins re-selects a pin its constraints have moved
// away from, while a build, whose view lists none, is held to the row.
func selectTierVersion(release string, tier versionTier, reference string, declared []VersionConstraint, view InstanceVersionView) (string, string, bool, error) {
	base := types.ParseVersionPin(reference).Base()
	offered, err := candidateVersions(view, tier, reference, base)
	if err != nil {
		return "", "", false, err
	}
	version, newest, selected, err := selectAmong(release, tier, reference, base, declared, view, offered)
	if offered.recorded && errors.Is(err, ErrConflictingVersionConstraints()) {
		listed, lerr := listedVersionsOf(view, tier, base)
		if lerr != nil {
			return "", "", false, lerr
		}
		if len(listed) > 0 {
			return selectAmong(release, tier, reference, base, declared, view, candidates{versions: listed})
		}
	}
	return version, newest, selected, err
}

// selectAmong is selectTierVersion over the versions offered.
func selectAmong(release string, tier versionTier, reference, base string, declared []VersionConstraint, view InstanceVersionView, offered candidates) (string, string, bool, error) {
	held := offered.versions
	index := make(map[string]string, len(held))
	for _, version := range held {
		index[entityIndexKey(base, version)] = version
	}
	meta := func(version string) (string, string) { return base, version }
	key, selected, err := selectEntityVersionWhere(index, reference, meta, admittedBy(tier, base, declared, view))
	switch {
	case selected:
		newest := index[key]
		if highest, ok, _ := selectEntityVersionWhere(index, reference, meta, nil); ok {
			newest = index[highest]
		}
		return index[key], newest, true, nil
	case errors.Is(err, ErrConflictingVersionConstraints()):
		if fault := malformedConstraintPin(tier, declared, reference); fault != nil {
			return "", "", false, fault
		}
		return "", "", false, conflictingVersionConstraints(release, tier, declared, held, offered.record)
	default:
		return "", "", false, nil
	}
}

// admittedBy is the admission predicate the mapping keys and the
// spec.implements values in declared state over the versions of base. The type
// constraints are the reference the select is asked with, and need no predicate.
func admittedBy(tier versionTier, base string, declared []VersionConstraint, view InstanceVersionView) func(string) bool {
	return func(version string) bool {
		request := versionRequest(base, version)
		for _, c := range declared {
			if !admitsVersion(tier, c, request, view) {
				return false
			}
		}
		return true
	}
}

// versionRequest is the type a constraint is judged against: base at version, or
// base alone for a version-less entry.
func versionRequest(base, version string) string {
	if version == "" {
		return base
	}
	return base + ":" + version
}

// admitsVersion reports whether the constraint c admits the type request, the
// base at one held version. A mapping key is read as the pin grammar reads a
// declaration admitting a concrete version: exact, caret range, or naming no
// version; a spec.implements is judged by the mapping-consistency judgement of
// the tier. The type as written admits through the same pin grammar, though the
// select is asked with that reference and does not consult it here.
func admitsVersion(tier versionTier, c VersionConstraint, request string, view InstanceVersionView) bool {
	switch c.Kind {
	case ConstraintType, ConstraintMappingKey:
		return resolver.ImplementsSatisfies(c.Value, request)
	case ConstraintImplements:
		return tier.accepts(view, c.Value, request)
	}
	return true
}

// malformedConstraintPin returns the malformed-pin condition for the first
// mapping key or spec.implements in declared whose pin is malformed, naming where
// it is written, and nil when none is.
func malformedConstraintPin(tier versionTier, declared []VersionConstraint, reference string) error {
	for _, c := range declared {
		var where string
		switch c.Kind {
		case ConstraintMappingKey:
			where = fmt.Sprintf("Manifest %s for %q, written in %q", tier.mappingKeyLabel(), reference, c.File)
		case ConstraintImplements:
			where = fmt.Sprintf("spec.implements of %s %q", tier.recipeLabel(), c.File)
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
// version a lockfile records adds: the pull of the Release in releaseFile, which
// resolves the reference again and records the version its constraints admit. A
// refusal that knows no file names a placeholder in its place.
func lockedVersionAdvice(releaseFile string) string {
	if releaseFile == "" {
		releaseFile = "<release>"
	}
	return fmt.Sprintf(", or run `aggrega pull --release %s` to lock the version every constraint admits", releaseFile)
}

// conflictingVersionConstraints is the refusal of a reference of tier that no version the
// catalog holds satisfies under every constraint in declared. It names the Release
// the instances belong to, then each instance in turn — where it is declared, its
// name, and each of its constraints with the file it is written in — then the
// versions held, a version-less entry among them left unnamed because it has no
// version to name — the versions held by a catalog directory or listed by a
// registry. Where the versions are the ones a lockfile records, record names that
// lockfile, the versions are named as locked there, a source beside the
// constraints, and the advice adds the pull of record's Release that locks the
// version the constraints admit. It is positioned where the first instance is
// declared: the
// document its type is written in, at the instance's site, which is where the
// remedy of changing the version is written.
func conflictingVersionConstraints(release string, tier versionTier, declared []VersionConstraint, held []string, record lockfileRecord) error {
	detail := []constraint.DetailValue{constraint.Captioned("Release", release)}
	for _, c := range declared {
		switch c.Kind {
		case ConstraintType:
			detail = append(detail,
				constraint.Captioned("at", c.Site),
				constraint.Captioned("instance", c.Instance),
				constraint.Captioned("type", c.Value),
				constraint.Captioned("written in", c.File))
		case ConstraintMappingKey, ConstraintImplements:
			for _, n := range mappingConstraintNotes(tier, c) {
				detail = append(detail, constraint.Captioned(n.Caption, n.Value))
			}
		}
	}
	named := make([]string, 0, len(held))
	for _, version := range held {
		if version != "" {
			named = append(named, version)
		}
	}
	advice := conflictingVersionAdvice
	if record.lockfile != "" {
		detail = append(detail,
			constraint.Captioned("locked at", strings.Join(named, ", ")),
			constraint.Captioned("written in", record.lockfile))
		advice += lockedVersionAdvice(record.release)
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

// mappingConstraintNotes is the captioned values naming a Manifest key or a
// spec.implements constraint: the key with the file it is written in, or the
// Recipe the mapping names with the type it implements.
func mappingConstraintNotes(tier versionTier, c VersionConstraint) []constraint.HeldBackNote {
	if c.Kind == ConstraintImplements {
		return []constraint.HeldBackNote{
			{Caption: "mapped to " + tier.recipeLabel(), Value: c.File},
			{Caption: "whose spec.implements is", Value: c.Value}}
	}
	return []constraint.HeldBackNote{
		{Caption: "Manifest " + tier.mappingKeyLabel(), Value: c.Value},
		{Caption: "written in", Value: c.File}}
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

// FeatureVersions returns the version of every Feature named name this catalog's
// own Feature index holds, in the order inVersionOrder gives.
func (c *Catalog) FeatureVersions(name string) []string {
	var held []string
	for _, feat := range c.Features {
		if feat.Metadata.Name == name {
			held = append(held, feat.Metadata.Version)
		}
	}
	return inVersionOrder(held)
}

// FeatureVersions returns the version of every Feature named name this view's
// catalog directories hold, in the order inVersionOrder gives. A version only a
// cache slot or a registry holds is not among them, and reading them asks no
// registry.
func (l *LazyLookup) FeatureVersions(name string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buildFeatureIndex()
	var held []string
	for _, doc := range l.featureIndex {
		if doc.name == name {
			held = append(held, doc.version)
		}
	}
	return inVersionOrder(held)
}
