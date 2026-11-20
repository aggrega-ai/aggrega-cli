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
// instance it is declared on, which kind of constraint it is, its value as
// written, and the file it is written in — the Aggregate's file for the type,
// the Manifest's file for a mapping key, and the Recipe's reference as the
// mapping writes it for an implements.
type VersionConstraint struct {
	Instance string
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

// InstanceVersionView is what the instance-version table reads of a catalog: the
// versions held under a Component's name, the Recipe a mapping's reference names,
// and the extends ancestry of a component type at one version. Both catalog views
// a build reads, the eagerly-loaded *Catalog and the *LazyLookup, satisfy it.
type InstanceVersionView interface {
	// ComponentVersions returns the version of every Component named name the
	// view holds, in semantic-version order.
	ComponentVersions(name string) []string
	// RecipeByURI resolves a Recipe reference as the mapping lookup the build
	// renders through resolves it.
	RecipeByURI(uri string) (types.Recipe, bool)
	// ComponentAncestry returns the extends ancestry of a component type, as
	// the mapping-consistency judgement reads it.
	ComponentAncestry(componentType string) ([]string, error)
}

// ResolveInstanceVersions resolves each component reference the instances write
// to the highest version every constraint declared on its instances admits:
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
// the version-less fallback are the select's own.
//
// Where the versions held include some the reference alone admits and none every
// constraint admits, the reference is refused with the conflicting-version-
// constraints condition, naming each instance writing it, each constraint with
// the file it is written in, and the versions held. Where a mapping key or a
// spec.implements carries a malformed pin, that pin admits no version, so the
// refusal is the malformed-pin condition naming where the pin is written instead.
// Every refused reference is reported, and the table holds the others.
//
// A reference the table cannot judge is left out of Selected and resolves as it
// does without the table: one whose name the view holds at no version, one no
// held version satisfies on its own pin, and one whose own pin is malformed are
// reported where references are resolved. An instance no mapping reaches carries
// its type alone and resolves as its reference does; one whose mapping lookup
// refuses, as an ambiguous key, carries its type alone too, and the lookup's
// refusal is the build's to report. A mapping whose Recipe the view does not hold
// adds no implements.
//
// Feature instances resolve as their references do, and the table holds no row
// for them.
func ResolveInstanceVersions(instances []closure.InstanceReference, manifest *types.Manifest, manifestFile string, view InstanceVersionView) (InstanceVersions, error) {
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
		version, selected, err := selectComponentVersion(reference, declared, manifestFile, view)
		switch {
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
	declared := []VersionConstraint{{Instance: in.Name, Kind: ConstraintType, Value: in.Reference, File: in.File}}
	match, mapped, err := componentMappingEntry(manifest, in.Name, in.Reference)
	if err != nil || !mapped {
		return declared
	}
	if match.byType {
		declared = append(declared, VersionConstraint{
			Instance: in.Name, Kind: ConstraintMappingKey, Value: match.typeKey, File: manifestFile,
		})
	}
	if recipe, held := view.RecipeByURI(match.reference); held {
		declared = append(declared, VersionConstraint{
			Instance: in.Name, Kind: ConstraintImplements, Value: recipe.Spec.Implements, File: match.reference,
		})
	}
	return declared
}

// selectComponentVersion selects the version of reference every constraint in
// declared admits, among the versions the view holds under its name. It reports
// the version and true on a selection, false with no error for a reference the
// table leaves to its own resolution, and the refusal for one it refuses.
func selectComponentVersion(reference string, declared []VersionConstraint, manifestFile string, view InstanceVersionView) (string, bool, error) {
	base := types.ParseVersionPin(reference).Base()
	held := view.ComponentVersions(base)
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
		return "", false, conflictingVersionConstraints(declared, held, manifestFile)
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

// conflictingVersionConstraints is the refusal of a reference whose held versions
// no version of satisfies every constraint in declared: each instance in turn,
// with each of its constraints and the file it is written in, then the versions
// held. It is positioned at the Manifest, whose mappings bring every constraint
// beyond the type, and at the first instance.
func conflictingVersionConstraints(declared []VersionConstraint, held []string, manifestFile string) error {
	var detail []constraint.DetailValue
	for _, c := range declared {
		switch c.Kind {
		case ConstraintType:
			detail = append(detail,
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
	detail = append(detail, constraint.Captioned("catalog holds", strings.Join(held, ", ")))

	condition := ErrConflictingVersionConstraints()
	at := cerrors.SourceLocation{File: manifestFile}
	if len(declared) > 0 {
		at.Instance = declared[0].Instance
	}
	return cerrors.Wrap(condition, cerrors.OpsError, at,
		cerrors.ComposeMessage(condition, constraint.RenderDetail(detail...), conflictingVersionAdvice))
}

// inVersionOrder returns versions in semantic-version order, a version that is no
// semantic version after every one that is, as the version select ranks them.
func inVersionOrder(versions []string) []string {
	candidates := make([]versionCandidate, 0, len(versions))
	for _, v := range versions {
		c := versionCandidate{key: v, raw: v}
		if parsed, err := semver.NewVersion(v); err == nil {
			c.parsed = parsed
		}
		candidates = append(candidates, c)
	}
	sort.Slice(candidates, func(i, j int) bool { return higherCandidate(candidates[j], candidates[i]) })
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.raw)
	}
	return out
}

// ComponentVersions returns the version of every Component named name this
// catalog's own Component index holds, in semantic-version order.
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
// view's catalog directories hold, in semantic-version order. A version only a
// cache slot or a registry holds is not among them, and reading them asks no
// registry.
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
