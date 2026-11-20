// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The tests in this file hold the instance-version table to the rule it states:
// a component reference resolves to the highest version every constraint declared
// on its instances admits — the type as the Aggregate writes it, the Manifest key
// that reaches the instance, and the spec.implements of the Recipe the mapping
// names — and where no held version satisfies them all, the table refuses with the
// conflict naming each constraint and the file it is written in.
//
// Every case runs over both catalog views a build reads: the eagerly-loaded
// catalog and the lazy lookup over the same directory.

// The Release the instances belong to, and the two files the instances and the
// mapping are written in, as a caller that loaded them names them.
const (
	instanceVersionsRelease       = "quickstart-web-local"
	instanceVersionsAggregateFile = "aggregate.yaml"
	instanceVersionsManifestFile  = "manifest.yaml"
)

// instanceVersionsComponentYAML is a minimal loadable Component of name at
// version, extending extends where it is not empty.
func instanceVersionsComponentYAML(name, version, extends string) string {
	spec := "spec: {}\n"
	if extends != "" {
		spec = "spec:\n  extends: " + extends + "\n  schema: inherited\n"
	}
	return "apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  name: " + name +
		"\n  version: \"" + version + "\"\n  labels:\n    component.aggrega.ai/type: server\n" + spec
}

// instanceVersionsRecipeYAML is a minimal loadable Recipe of the compose
// capability, named name at version, declaring implements.
func instanceVersionsRecipeYAML(name, version, implements string) string {
	return "apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: " + name +
		"\n  version: \"" + version + "\"\nspec:\n  capability: compose\n  implements: \"" + implements +
		"\"\n  artifacts:\n    probe.txt: \"probe\"\n"
}

// instanceVersionsCatalog writes, under a fresh directory, the catalog every case
// here reads: the Component core/compute/server at 1.0.0 and 1.1.0; the Recipe
// compose/server at 1.0.0 implementing core/compute/server:1.0.0 and at 2.0.0
// implementing core/compute/server:1.1.0; and example/web at 1.0.0 and 1.1.0, both
// extending core/compute/server:1.0.0. extra adds further documents, each a path
// beside its content. It returns the directory.
func instanceVersionsCatalog(t *testing.T, extra ...[2]string) string {
	t.Helper()
	dir := t.TempDir()
	writeCatalogEntity(t, dir, "component/core/compute/server/1.0.0/component.yaml",
		instanceVersionsComponentYAML("core/compute/server", "1.0.0", ""))
	writeCatalogEntity(t, dir, "component/core/compute/server/1.1.0/component.yaml",
		instanceVersionsComponentYAML("core/compute/server", "1.1.0", ""))
	writeCatalogEntity(t, dir, "component/example/web/1.0.0/component.yaml",
		instanceVersionsComponentYAML("example/web", "1.0.0", "core/compute/server:1.0.0"))
	writeCatalogEntity(t, dir, "component/example/web/1.1.0/component.yaml",
		instanceVersionsComponentYAML("example/web", "1.1.0", "core/compute/server:1.0.0"))
	writeCatalogEntity(t, dir, "recipe/compose/server/1.0.0/recipe.yaml",
		instanceVersionsRecipeYAML("server", "1.0.0", "core/compute/server:1.0.0"))
	writeCatalogEntity(t, dir, "recipe/compose/server/2.0.0/recipe.yaml",
		instanceVersionsRecipeYAML("server", "2.0.0", "core/compute/server:1.1.0"))
	for _, e := range extra {
		writeCatalogEntity(t, dir, e[0], e[1])
	}
	return dir
}

// instanceVersionViews returns both catalog views a build reads over dir: the
// eagerly-loaded catalog and the lazy lookup.
func instanceVersionViews(t *testing.T, dir string) []struct {
	name string
	view InstanceVersionView
} {
	t.Helper()
	eager, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return []struct {
		name string
		view InstanceVersionView
	}{
		{name: "the eager catalog", view: eager},
		{name: "the lazy lookup", view: NewLazyLookup(dir, "", []string{dir}, nil)},
	}
}

// instanceVersionsManifest is a Manifest whose components mapping carries the
// type-axis entries and the name-axis entries given, each a key beside the Recipe
// it maps to, in order.
func instanceVersionsManifest(typeEntries, nameEntries [][2]string) *types.Manifest {
	tier := &types.MappingTier{}
	for _, e := range typeEntries {
		tier.Type.Set(e[0], e[1])
	}
	for _, e := range nameEntries {
		tier.Name.Set(e[0], e[1])
	}
	return &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: tier}}}
}

// componentInstance is a top-level component instance named name, of the type
// reference, written in the Aggregate's file.
func componentInstance(name, reference string) closure.InstanceReference {
	return closure.InstanceReference{
		Path:      "aggregate.spec.components." + name + ".type",
		Name:      name,
		Reference: reference,
		File:      instanceVersionsAggregateFile,
	}
}

// selectedVersion reads the version the table selected for reference.
func selectedVersion(table InstanceVersions, reference string) (string, bool) {
	return table.Selected.Get(reference)
}

// resolveCase is one table resolution and the version it must select for the
// instances' one reference.
type resolveCase struct {
	name      string
	manifest  *types.Manifest
	instances []closure.InstanceReference
	reference string
	want      string
}

// runResolveCases resolves each case over both views of dir and requires the
// version it names, with no refusal.
func runResolveCases(t *testing.T, dir string, cases []resolveCase) {
	t.Helper()
	for _, v := range instanceVersionViews(t, dir) {
		for _, tc := range cases {
			t.Run(v.name+"/"+tc.name, func(t *testing.T) {
				table, err := ResolveInstanceVersions(instanceVersionsRelease, tc.instances, tc.manifest, instanceVersionsManifestFile, v.view)
				if err != nil {
					t.Fatalf("resolve: %v", err)
				}
				got, ok := selectedVersion(table, tc.reference)
				if !ok || got != tc.want {
					t.Errorf("%q selected %q (held %t); want %q", tc.reference, got, ok, tc.want)
				}
			})
		}
	}
}

// TestInstanceVersionsHoldsBareTypeAtMappedVersion holds a type naming no version
// to the version the Manifest key and its Recipe name: the key
// core/compute/server:1.0.0 maps to a Recipe implementing 1.0.0, so the instance
// resolves to 1.0.0 although 1.1.0 is held.
//
// 🔴 PLANTED DEFECT: in ResolveInstanceVersions, pass a nil admission predicate to
// the selection: both rows redden, selecting 1.1.0.
func TestInstanceVersionsHoldsBareTypeAtMappedVersion(t *testing.T) {
	t.Parallel()
	runResolveCases(t, instanceVersionsCatalog(t), []resolveCase{{
		name: "a bare type under a key naming 1.0.0",
		manifest: instanceVersionsManifest([][2]string{
			{"core/compute/server:1.0.0", "compose/server:1.0.0"},
		}, nil),
		instances: []closure.InstanceReference{componentInstance("web", "core/compute/server")},
		reference: "core/compute/server",
		want:      "1.0.0",
	}})
}

// TestInstanceVersionsBareKeyFollowsRecipe holds a key naming no version to adding
// nothing of its own, so the mapped Recipe's implements alone narrows the version:
// a Recipe pinned at 2.0.0 implements 1.1.0, a Recipe reference naming no version
// selects the highest Recipe (2.0.0, so 1.1.0), and a Recipe pinned at 1.0.0
// implements 1.0.0.
func TestInstanceVersionsBareKeyFollowsRecipe(t *testing.T) {
	t.Parallel()
	bareKey := func(recipe string) *types.Manifest {
		return instanceVersionsManifest([][2]string{{"core/compute/server", recipe}}, nil)
	}
	web := []closure.InstanceReference{componentInstance("web", "core/compute/server")}
	runResolveCases(t, instanceVersionsCatalog(t), []resolveCase{
		{name: "to a Recipe pinned at 2.0.0", manifest: bareKey("compose/server:2.0.0"), instances: web,
			reference: "core/compute/server", want: "1.1.0"},
		{name: "to a Recipe reference naming no version", manifest: bareKey("compose/server"), instances: web,
			reference: "core/compute/server", want: "1.1.0"},
		{name: "to a Recipe pinned at 1.0.0", manifest: bareKey("compose/server:1.0.0"), instances: web,
			reference: "core/compute/server", want: "1.0.0"},
	})
}

// TestInstanceVersionsNameEntryConstrainsByRecipe holds an instance a name-axis
// entry reaches to its Recipe's implements: the name says nothing about a type,
// and the Recipe it maps to implements 1.0.0.
func TestInstanceVersionsNameEntryConstrainsByRecipe(t *testing.T) {
	t.Parallel()
	runResolveCases(t, instanceVersionsCatalog(t), []resolveCase{{
		name:      "a name entry mapping to a Recipe implementing 1.0.0",
		manifest:  instanceVersionsManifest(nil, [][2]string{{"web", "compose/server:1.0.0"}}),
		instances: []closure.InstanceReference{componentInstance("web", "core/compute/server")},
		reference: "core/compute/server",
		want:      "1.0.0",
	}})
}

// TestInstanceVersionsCaretOnBothSides holds caret ranges on every side to their
// intersection: a type ^1.0.0, a key ^1.0.0 and a Recipe implementing ^1.1.0 admit
// the versions in >=1.1.0 <2.0.0, and the highest of those is selected. A key
// ^1.0.0 over a bare type, mapped to a Recipe implementing the type at any version,
// keeps 2.0.0 out where the catalog holds it. A bare key over a bare type, mapped
// to a Recipe implementing ^1.0.0, leaves the Recipe's caret alone to keep 2.0.0
// out, which is the row where the caret on the implements side decides.
func TestInstanceVersionsCaretOnBothSides(t *testing.T) {
	t.Parallel()
	dir := instanceVersionsCatalog(t,
		[2]string{"component/core/compute/server/2.0.0/component.yaml",
			instanceVersionsComponentYAML("core/compute/server", "2.0.0", "")},
		[2]string{"recipe/compose/server-caret/1.0.0/recipe.yaml",
			instanceVersionsRecipeYAML("server-caret", "1.0.0", "core/compute/server:^1.1.0")},
		[2]string{"recipe/compose/server-any/1.0.0/recipe.yaml",
			instanceVersionsRecipeYAML("server-any", "1.0.0", "core/compute/server")},
		[2]string{"recipe/compose/server-major/1.0.0/recipe.yaml",
			instanceVersionsRecipeYAML("server-major", "1.0.0", "core/compute/server:^1.0.0")},
	)
	runResolveCases(t, dir, []resolveCase{
		{
			name: "a caret type, a caret key and a caret implements",
			manifest: instanceVersionsManifest([][2]string{
				{"core/compute/server:^1.0.0", "compose/server-caret:1.0.0"},
			}, nil),
			instances: []closure.InstanceReference{componentInstance("web", "core/compute/server:^1.0.0")},
			reference: "core/compute/server:^1.0.0",
			want:      "1.1.0",
		},
		{
			name: "a caret key over a bare type",
			manifest: instanceVersionsManifest([][2]string{
				{"core/compute/server:^1.0.0", "compose/server-any:1.0.0"},
			}, nil),
			instances: []closure.InstanceReference{componentInstance("web", "core/compute/server")},
			reference: "core/compute/server",
			want:      "1.1.0",
		},
		{
			name: "a bare key over a bare type, mapped to a Recipe implementing a caret range",
			manifest: instanceVersionsManifest([][2]string{
				{"core/compute/server", "compose/server-major:1.0.0"},
			}, nil),
			instances: []closure.InstanceReference{componentInstance("web", "core/compute/server")},
			reference: "core/compute/server",
			want:      "1.1.0",
		},
	})
}

// TestInstanceVersionsExtendsThroughAncestry holds a Recipe written against a base
// to admitting each version of an extending type whose chain reaches the base
// version it implements: example/web 1.0.0 and 1.1.0 both extend
// core/compute/server:1.0.0, the Recipe implements core/compute/server:1.0.0, and
// the key example/web:1.0.0 keeps the instance at 1.0.0.
//
// 🔴 PLANTED DEFECT: in ResolveInstanceVersions, hand the judgement an ancestry walk
// that finds no base: both rows redden with the conflict.
func TestInstanceVersionsExtendsThroughAncestry(t *testing.T) {
	t.Parallel()
	runResolveCases(t, instanceVersionsCatalog(t), []resolveCase{{
		name: "a key naming example/web 1.0.0 mapped to a Recipe of its base",
		manifest: instanceVersionsManifest([][2]string{
			{"example/web:1.0.0", "compose/server:1.0.0"},
		}, nil),
		instances: []closure.InstanceReference{componentInstance("web", "example/web")},
		reference: "example/web",
		want:      "1.0.0",
	}})
}

// TestInstanceVersionsConflictNamesEveryConstraint holds a reference no held
// version satisfies under every constraint to the conflict, naming the Release,
// where the instance is declared and its name, each constraint with the file it is
// written in, and the versions held, in that order: the type
// core/compute/server:1.0.0 in the Aggregate's file, the key
// core/compute/server:1.1.0 in the Manifest's, and the Recipe the key maps to with
// its spec.implements. The conflict is positioned where the instance is declared.
func TestInstanceVersionsConflictNamesEveryConstraint(t *testing.T) {
	t.Parallel()
	manifest := instanceVersionsManifest([][2]string{
		{"core/compute/server:1.1.0", "compose/server:2.0.0"},
	}, nil)
	instances := []closure.InstanceReference{componentInstance("web", "core/compute/server:1.0.0")}
	const want = `no version of a component or feature type satisfies every constraint declared on the instance ` +
		`(Release "quickstart-web-local", at ".spec.components.web", ` +
		`instance "web", type "core/compute/server:1.0.0", written in "aggregate.yaml", ` +
		`Manifest mapping key "core/compute/server:1.1.0", written in "manifest.yaml", ` +
		`mapped to recipe "compose/server:2.0.0", whose spec.implements is "core/compute/server:1.1.0", ` +
		`versions held "1.0.0, 1.1.0"). change the Aggregate's version, or map this type at a version its recipe implements`

	for _, v := range instanceVersionViews(t, instanceVersionsCatalog(t)) {
		t.Run(v.name, func(t *testing.T) {
			table, err := ResolveInstanceVersions(instanceVersionsRelease, instances, manifest, instanceVersionsManifestFile, v.view)
			if !errors.Is(err, ErrConflictingVersionConstraints()) {
				t.Fatalf("err = %v; want the conflicting-version-constraints condition", err)
			}
			var raised *cerrors.CompilerError
			if !errors.As(err, &raised) {
				t.Fatalf("err = %T; want a diagnostic carrying its category and position", err)
			}
			if raised.Message() != want {
				t.Errorf("message:\n got %s\nwant %s", raised.Message(), want)
			}
			if raised.Category() != cerrors.OpsError {
				t.Errorf("category = %q, want %q", raised.Category(), cerrors.OpsError)
			}
			wantAt := cerrors.SourceLocation{File: "aggregate.yaml", Locator: ".spec.components.web", Instance: "web"}
			if at := raised.Source(); at != wantAt {
				t.Errorf("positioned at %+v; want where the instance is declared, %+v", at, wantAt)
			}
			if _, held := selectedVersion(table, "core/compute/server:1.0.0"); held {
				t.Error("the table selected a version for the reference it refused")
			}
			const site = ".spec.components.web"
			wantConstraints := []VersionConstraint{
				{Instance: "web", Site: site, Kind: ConstraintType, Value: "core/compute/server:1.0.0", File: "aggregate.yaml"},
				{Instance: "web", Site: site, Kind: ConstraintMappingKey, Value: "core/compute/server:1.1.0", File: "manifest.yaml"},
				{Instance: "web", Site: site, Kind: ConstraintImplements, Value: "core/compute/server:1.1.0", File: "compose/server:2.0.0"},
			}
			got, _ := table.Constraints.Get("core/compute/server:1.0.0")
			if !slices.Equal(got, wantConstraints) {
				t.Errorf("constraints:\n got %+v\nwant %+v", got, wantConstraints)
			}
		})
	}
}

// TestInstanceVersionsOneVersionPerReference holds every instance writing one
// reference to one version: two instances of the bare type, one a name entry maps
// to a Recipe implementing 1.0.0 and one the type key maps to a Recipe implementing
// 1.1.0, admit no version together, so the conflict names both.
func TestInstanceVersionsOneVersionPerReference(t *testing.T) {
	t.Parallel()
	manifest := instanceVersionsManifest(
		[][2]string{{"core/compute/server", "compose/server:2.0.0"}},
		[][2]string{{"web", "compose/server:1.0.0"}},
	)
	instances := []closure.InstanceReference{
		componentInstance("web", "core/compute/server"),
		componentInstance("api", "core/compute/server"),
	}
	for _, v := range instanceVersionViews(t, instanceVersionsCatalog(t)) {
		t.Run(v.name, func(t *testing.T) {
			_, err := ResolveInstanceVersions(instanceVersionsRelease, instances, manifest, instanceVersionsManifestFile, v.view)
			if !errors.Is(err, ErrConflictingVersionConstraints()) {
				t.Fatalf("err = %v; want the conflicting-version-constraints condition", err)
			}
			for _, named := range []string{
				`instance "web", type "core/compute/server", written in "aggregate.yaml", mapped to recipe "compose/server:1.0.0"`,
				`instance "api", type "core/compute/server", written in "aggregate.yaml", Manifest mapping key "core/compute/server"`,
			} {
				if !strings.Contains(err.Error(), named) {
					t.Errorf("the conflict does not name %s:\n%v", named, err)
				}
			}
		})
	}
}

// TestInstanceVersionsKeepsReferenceFormsApart holds two references to one type
// written in two forms to two rows resolved independently: the bare type a key
// maps to a Recipe implementing 1.1.0 resolves to 1.1.0, and the type pinned at
// 1.0.0 an exact key maps to a Recipe implementing 1.0.0 resolves to 1.0.0, with
// no conflict between them.
func TestInstanceVersionsKeepsReferenceFormsApart(t *testing.T) {
	t.Parallel()
	manifest := instanceVersionsManifest([][2]string{
		{"core/compute/server", "compose/server:2.0.0"},
		{"core/compute/server:1.0.0", "compose/server:1.0.0"},
	}, nil)
	instances := []closure.InstanceReference{
		componentInstance("web", "core/compute/server"),
		componentInstance("api", "core/compute/server:1.0.0"),
	}
	for _, v := range instanceVersionViews(t, instanceVersionsCatalog(t)) {
		t.Run(v.name, func(t *testing.T) {
			table, err := ResolveInstanceVersions(instanceVersionsRelease, instances, manifest, instanceVersionsManifestFile, v.view)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got := table.Selected.Keys(); !slices.Equal(got, []string{"core/compute/server", "core/compute/server:1.0.0"}) {
				t.Errorf("rows = %v; want one per reference, in the order the instances were visited", got)
			}
			for ref, want := range map[string]string{"core/compute/server": "1.1.0", "core/compute/server:1.0.0": "1.0.0"} {
				if got, _ := selectedVersion(table, ref); got != want {
					t.Errorf("%q selected %q, want %q", ref, got, want)
				}
			}
		})
	}
}

// TestInstanceVersionsLeavesUnmappedInstanceToItsReference holds an instance no
// mapping reaches to its own reference: it carries the type constraint alone and
// resolves to the version it resolves to without the table, with nothing refused —
// the halt over an instance nothing maps stays the build's own.
func TestInstanceVersionsLeavesUnmappedInstanceToItsReference(t *testing.T) {
	t.Parallel()
	manifest := instanceVersionsManifest([][2]string{{"example/web", "compose/server:1.0.0"}}, nil)
	instances := []closure.InstanceReference{componentInstance("web", "core/compute/server")}
	for _, v := range instanceVersionViews(t, instanceVersionsCatalog(t)) {
		t.Run(v.name, func(t *testing.T) {
			table, err := ResolveInstanceVersions(instanceVersionsRelease, instances, manifest, instanceVersionsManifestFile, v.view)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got, _ := selectedVersion(table, "core/compute/server"); got != "1.1.0" {
				t.Errorf("selected %q, want 1.1.0, the highest version held", got)
			}
			got, _ := table.Constraints.Get("core/compute/server")
			want := []VersionConstraint{{
				Instance: "web", Site: ".spec.components.web", Kind: ConstraintType,
				Value: "core/compute/server", File: "aggregate.yaml",
			}}
			if !slices.Equal(got, want) {
				t.Errorf("constraints = %+v, want the type alone %+v", got, want)
			}
		})
	}
}

// TestInstanceVersionsLeavesASelfContradictingEntryToItsRule holds a type-axis
// entry whose key and Recipe disagree with each other — the key
// core/compute/server:1.1.0 maps to the Recipe implementing 1.0.0 — to holding the
// instance to nothing: the rule over the mapping entry reports that entry, so the
// table neither refuses the reference nor reads the entry's key or implements,
// and the bare type resolves as its reference does, to 1.1.0.
func TestInstanceVersionsLeavesASelfContradictingEntryToItsRule(t *testing.T) {
	t.Parallel()
	manifest := instanceVersionsManifest([][2]string{{"core/compute/server:1.1.0", "compose/server:1.0.0"}}, nil)
	instances := []closure.InstanceReference{componentInstance("web", "core/compute/server")}
	for _, v := range instanceVersionViews(t, instanceVersionsCatalog(t)) {
		t.Run(v.name, func(t *testing.T) {
			table, err := ResolveInstanceVersions(instanceVersionsRelease, instances, manifest, instanceVersionsManifestFile, v.view)
			if err != nil {
				t.Fatalf("the table refuses an entry the rule over it reports: %v", err)
			}
			if got, _ := selectedVersion(table, "core/compute/server"); got != "1.1.0" {
				t.Errorf("selected %q, want 1.1.0, the version the reference alone selects", got)
			}
			got, _ := table.Constraints.Get("core/compute/server")
			want := []VersionConstraint{{
				Instance: "web", Site: ".spec.components.web", Kind: ConstraintType,
				Value: "core/compute/server", File: "aggregate.yaml",
			}}
			if !slices.Equal(got, want) {
				t.Errorf("constraints = %+v, want the type alone %+v", got, want)
			}
		})
	}
}

// TestInstanceVersionsMalformedImplementsIsNotAConflict holds a mapped Recipe whose
// spec.implements carries a malformed pin to the malformed-pin condition: a pin
// naming no version admits none, and reporting that as a conflict would send the
// author comparing versions when the fault is the pin's text.
func TestInstanceVersionsMalformedImplementsIsNotAConflict(t *testing.T) {
	t.Parallel()
	dir := instanceVersionsCatalog(t, [2]string{"recipe/compose/server-malformed/1.0.0/recipe.yaml",
		instanceVersionsRecipeYAML("server-malformed", "1.0.0", "core/compute/server:1.x")})
	manifest := instanceVersionsManifest(nil, [][2]string{{"web", "compose/server-malformed:1.0.0"}})
	instances := []closure.InstanceReference{componentInstance("web", "core/compute/server")}
	for _, v := range instanceVersionViews(t, dir) {
		t.Run(v.name, func(t *testing.T) {
			_, err := ResolveInstanceVersions(instanceVersionsRelease, instances, manifest, instanceVersionsManifestFile, v.view)
			if !errors.Is(err, types.ErrMalformedVersionPin()) {
				t.Fatalf("err = %v; want the malformed-pin condition", err)
			}
			if errors.Is(err, ErrConflictingVersionConstraints()) {
				t.Errorf("a malformed pin is reported as a conflict: %v", err)
			}
			if !strings.Contains(err.Error(), `"compose/server-malformed:1.0.0"`) {
				t.Errorf("the malformed pin does not name the Recipe declaring it: %v", err)
			}
		})
	}
}

// TestInstanceVersionsConflictNamesEveryVersionHeld holds the versions a conflict
// names to the order a reader is shown them — the semantic versions ascending,
// then a version that is no semantic version — with an entry carrying no version
// left unnamed rather than written as an empty name.
func TestInstanceVersionsConflictNamesEveryVersionHeld(t *testing.T) {
	t.Parallel()
	if got, want := inVersionOrder([]string{"1.10.0", "", "latest", "1.2.0"}), []string{"1.2.0", "1.10.0", "", "latest"}; !slices.Equal(got, want) {
		t.Errorf("inVersionOrder = %q, want %q", got, want)
	}
	declared := []VersionConstraint{{
		Instance: "web", Site: ".spec.components.web", Kind: ConstraintType,
		Value: "core/compute/server:1.0.0", File: instanceVersionsAggregateFile,
	}}
	err := conflictingVersionConstraints(instanceVersionsRelease, componentTier, declared, inVersionOrder([]string{"1.10.0", "", "latest", "1.2.0"}), lockfileRecord{})
	if !strings.Contains(err.Error(), `versions held "1.2.0, 1.10.0, latest")`) {
		t.Errorf("the conflict does not name the versions held in order, without an empty one:\n%v", err)
	}
}

// The instance-version table reads both catalog views a build reads through one
// narrow interface.
var (
	_ InstanceVersionView = (*Catalog)(nil)
	_ InstanceVersionView = (*LazyLookup)(nil)
)
