// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

// One version rule on every path that reads a catalog directory.
//
// The fixture holds every by-name kind at two versions, and each row asks one
// reference form of one kind through each reader in turn: the eager catalog a
// pull resolves through, the lazy view a build resolves through, the per-request
// view the daemon forks from it, the resolver guards, the sub-aggregate loaders
// and the retained-bytes accessor. The rows disagree on their answer — 1.0.0 or
// 2.0.0 — so a reader that ignores the version, or that hashes one version's
// bytes for another, fails a row whichever file it happens to read.
//
// The two versions sit in lexically ordered directories, so a reader keyed by the
// bare name finds 1.0.0 first while a map written in walk order holds 2.0.0 last:
// each of those two shortcuts gets some rows right by coincidence and the others
// wrong, which is what makes the table discriminate between them.

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// versionedFixtureDoc is one document of the multi-version fixture.
type versionedFixtureDoc struct {
	kind, name, version, rel, body string
}

// multiVersionDocs returns every document of the multi-version fixture: a
// Component, a Feature, an Aggregate and a pipeline-step Plugin, each at 1.0.0 and
// 2.0.0, in the per-version layout. Each version's body names its version in a
// description so a reader that returns the wrong file is visible in any field.
func multiVersionDocs() []versionedFixtureDoc {
	var docs []versionedFixtureDoc
	for _, v := range []string{"1.0.0", "2.0.0"} {
		docs = append(docs,
			versionedFixtureDoc{"Component", "test/svc", v, "component/test/svc/" + v + "/component.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/svc
  version: "` + v + `"
  description: svc at ` + v + `
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      greeting: { type: string, default: "svc-` + v + `" }
`},
			versionedFixtureDoc{"Feature", "test/tag", v, "feature/test/tag/" + v + "/feature.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: test/tag
  version: "` + v + `"
  description: tag at ` + v + `
spec:
  schema:
    values:
      label: { type: string, default: "tag-` + v + `" }
`},
			versionedFixtureDoc{"Aggregate", "test/sub", v, "aggregate/test/sub/" + v + "/aggregate.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test/sub
  version: "` + v + `"
  description: sub at ` + v + `
spec:
  components:
    inner:
      type: test/svc
`},
			versionedFixtureDoc{"Plugin", "pipeline-step/patch", v, "plugin/pipeline-step/patch/" + v + "/plugin.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: patch
  pluginType: pipeline-step
  version: "` + v + `"
  description: patch at ` + v + `
spec:
  abiVersion: v1alpha1
  exports:
    - name: step
      operates: patch
      scope: own
`},
		)
	}
	return docs
}

// writeMultiVersionCatalog lays the fixture into a catalog directory inside a
// workspace root and returns the root, the directory, and each document's own
// digest keyed by kind and version.
func writeMultiVersionCatalog(t *testing.T) (ws, catalogDir string, digests map[string]string) {
	t.Helper()
	ws = t.TempDir()
	catalogDir = filepath.Join(ws, "catalog")
	digests = map[string]string{}
	for _, d := range multiVersionDocs() {
		writeCatalogFile(t, catalogDir, d.rel, d.body)
		digests[d.kind+"@"+d.version] = contentDigest([]byte(d.body))
	}
	return ws, catalogDir, digests
}

// rowDigests returns, keyed as writeMultiVersionCatalog keys the documents' own
// digests, the digest a lockfile row records for each fixture document laid into
// catalogDir: taken over what a publication of that version's own document would
// carry, through the product's own reading of it (AuthoredEntityDigestAt). The
// two versions of a name must row-digest apart, or a reader that digests one
// version's document for the other would pass the rows below by coincidence.
func rowDigests(t *testing.T, catalogDir string) map[string]string {
	t.Helper()
	rows := map[string]string{}
	for _, d := range multiVersionDocs() {
		row, err := AuthoredEntityDigestAt(filepath.Join(catalogDir, filepath.FromSlash(d.rel)))
		if err != nil {
			t.Fatalf("row digest of %s: %v", d.rel, err)
		}
		rows[d.kind+"@"+d.version] = row
	}
	for _, k := range fixtureKinds {
		if rows[k.kind+"@1.0.0"] == rows[k.kind+"@2.0.0"] {
			t.Fatalf("the two %s documents row-digest alike; the table below would not discriminate", k.kind)
		}
	}
	return rows
}

// referenceForms are the three reference forms, written so the four rows do not
// agree on the version they select.
var referenceForms = []struct {
	form, suffix, want string
}{
	{"exact 1.0.0", ":1.0.0", "1.0.0"},
	{"exact 2.0.0", ":2.0.0", "2.0.0"},
	{"caret within major 1", ":^1.0.0", "1.0.0"},
	{"unversioned", "", "2.0.0"},
}

// fixtureKinds are the four by-name kinds a catalog directory holds per version,
// with the hint the dependency walk collects each under.
var fixtureKinds = []struct {
	kind, name, hint string
}{
	{"Component", "test/svc", "component"},
	{"Feature", "test/tag", "feature"},
	{"Aggregate", "test/sub", "aggregate"},
	{"Plugin", "pipeline-step/patch", "plugin"},
}

// resolveForKind asks lookup to resolve uri the way the dependency walk asks for
// that kind.
func resolveForKind(ctx context.Context, lookup closure.Lookup, kind, uri, hint string) (closure.Resolution, error) {
	if kind == "Aggregate" {
		return lookup.ResolveAggregate(ctx, uri)
	}
	return lookup.ResolveCatalogEntity(ctx, uri, hint)
}

// TestEveryPathSelectsOneVersionAndHashesItsOwnBytes is the rule stated once and
// asked everywhere: for each reference form and each kind, the pull's eager
// catalog, the build's lazy view and the daemon's per-request view resolve to the
// highest satisfying version, report that version — a caret reference included,
// which is recorded at the version it selected rather than as its constraint — and
// take the digest over that version's own document: the row a publication of that
// document records, which no other version's document digests to.
func TestEveryPathSelectsOneVersionAndHashesItsOwnBytes(t *testing.T) {
	t.Parallel()
	ws, catalogDir, _ := writeMultiVersionCatalog(t)
	rows := rowDigests(t, catalogDir)
	ctx := context.Background()

	eager, _, err := LoadPassOneTolerant(ws, "", catalogDir)
	if err != nil {
		t.Fatalf("eager load: %v", err)
	}
	lazy := NewLazyLookup(ws, "", []string{catalogDir}, nil)
	paths := []struct {
		name   string
		lookup closure.Lookup
	}{
		{"the eager whole-catalog load", eager.ClosureLookup(nil, ws, "")},
		{"the lazy view a build and a pull resolve through", lazy.ClosureLookup(nil, ws, "", nil, nil, nil)},
		{"the daemon's per-request view", lazy.ForRequest(nil).ClosureLookup(nil, ws, "", nil, nil, nil)},
	}

	for _, k := range fixtureKinds {
		for _, f := range referenceForms {
			uri := k.name + f.suffix
			want := rows[k.kind+"@"+f.want]
			for _, p := range paths {
				t.Run(k.kind+"/"+f.form+"/"+p.name, func(t *testing.T) {
					res, err := resolveForKind(ctx, p.lookup, k.kind, uri, k.hint)
					if err != nil {
						t.Fatalf("resolve %s: %v", uri, err)
					}
					if res.NotFound {
						t.Fatalf("%s did not resolve through %s, though the catalog directory holds %s at %s",
							uri, p.name, k.name, f.want)
					}
					if res.ResolvedVersion != f.want {
						t.Errorf("%s resolved to version %q through %s; the highest satisfying version is %q",
							uri, res.ResolvedVersion, p.name, f.want)
					}
					if res.Digest != want {
						t.Errorf("%s: the digest %s is not the row digest %s of the %s document it resolved to",
							uri, res.Digest, want, f.want)
					}
				})
			}
		}
	}
}

// TestEveryReaderOfTheIndexSelectsTheSameVersion asks the readers that hand back
// an entity rather than a resolution: the resolver guards a build resolves types
// through, the Release's aggregate lookup, the two sub-aggregate loaders, the
// plugin lookup and the retained-bytes accessor. Each must return the version
// the rule selects, on both backings.
func TestEveryReaderOfTheIndexSelectsTheSameVersion(t *testing.T) {
	t.Parallel()
	ws, catalogDir, digests := writeMultiVersionCatalog(t)
	eager, _, err := LoadPassOneTolerant(ws, "", catalogDir)
	if err != nil {
		t.Fatalf("eager load: %v", err)
	}
	lazy := NewLazyLookup(ws, "", []string{catalogDir}, nil)
	view := lazy.ForRequest(nil)

	for _, f := range referenceForms {
		t.Run(f.form, func(t *testing.T) {
			svc, tag, sub, patch := "test/svc"+f.suffix, "test/tag"+f.suffix, "test/sub"+f.suffix, "pipeline-step/patch"+f.suffix

			for _, g := range []struct {
				name  string
				guard func() (string, bool)
			}{
				{"lazy component guard", func() (string, bool) {
					c, ok := lazy.AsCatalogLookup().Component(svc)
					return c.Metadata.Version, ok
				}},
				{"per-request component guard", func() (string, bool) {
					c, ok := view.AsCatalogLookup().Component(svc)
					return c.Metadata.Version, ok
				}},
				{"lazy feature guard", func() (string, bool) {
					c, ok := lazy.AsCatalogLookup().Feature(tag)
					return c.Metadata.Version, ok
				}},
				{"lazy aggregate lookup", func() (string, bool) {
					a, ok := lazy.AggregateLookup()(sub)
					if !ok || a.Aggregate == nil {
						return "", false
					}
					if got := contentDigest(a.Bytes); got != digests["Aggregate@"+f.want] {
						t.Errorf("the aggregate lookup handed back bytes hashing to %s for %s; the %s document hashes to %s",
							got, sub, f.want, digests["Aggregate@"+f.want])
					}
					return a.Aggregate.Metadata.Version, true
				}},
				{"lazy sub-aggregate loader", func() (string, bool) {
					a, _, _, err := lazy.SubAggregateLoader()(sub, nil)
					if err != nil || a == nil {
						return "", false
					}
					return a.Metadata.Version, true
				}},
				{"eager sub-aggregate loader", func() (string, bool) {
					a, _, _, err := eager.SubAggregateLoader()(sub, nil)
					if err != nil || a == nil {
						return "", false
					}
					return a.Metadata.Version, true
				}},
				{"lazy plugin lookup", func() (string, bool) {
					p, ok := lazy.PluginByURI(patch)
					return p.Metadata.Version, ok
				}},
			} {
				got, ok := g.guard()
				if !ok {
					t.Errorf("%s: nothing resolved, though the catalog directory holds that name at %s", g.name, f.want)
					continue
				}
				if got != f.want {
					t.Errorf("%s: resolved to %q; the highest satisfying version is %q", g.name, got, f.want)
				}
			}

			// The descent that walks a sub-aggregate's body reads the retained bytes of
			// the version the walk resolved, on both backings.
			for _, b := range []struct {
				name   string
				lookup closure.Lookup
			}{
				{"eager", eager.ClosureLookup(nil, ws, "")},
				{"lazy", lazy.ClosureLookup(nil, ws, "", nil, nil, nil)},
			} {
				res, err := b.lookup.ResolveAggregate(context.Background(), sub)
				if err != nil || res.NotFound {
					t.Fatalf("%s: resolve %s: %v (not found: %v)", b.name, sub, err, res.NotFound)
				}
				loader, err := closure.RequireAggregateLoader(b.lookup)
				if err != nil {
					t.Fatalf("%s: %v", b.name, err)
				}
				agg, err := loader.LoadAggregate(sub, res.ResolvedVersion, res.Digest)
				if err != nil {
					t.Fatalf("%s: load %s: %v", b.name, sub, err)
				}
				if agg.Metadata.Version != f.want {
					t.Errorf("%s: the descent loaded the %s body for %s; the walk resolved %s",
						b.name, agg.Metadata.Version, sub, f.want)
				}
			}

			// The retained-bytes accessor answers a reference the way every other
			// reader does, so the assistant-facing catalog resource a name is read
			// through serves the selected version's document.
			for _, k := range fixtureKinds {
				got, ok := eager.EntityBytes(k.kind, k.name+f.suffix)
				if !ok {
					t.Errorf("EntityBytes(%s, %s): nothing retained", k.kind, k.name+f.suffix)
					continue
				}
				if contentDigest(got) != digests[k.kind+"@"+f.want] {
					t.Errorf("EntityBytes(%s, %s) returned bytes of another version than %s", k.kind, k.name+f.suffix, f.want)
				}
			}
		})
	}
}

// TestLazyViewAnswersAnUnsatisfiablePinAsTheEagerOneDoes holds the single-version
// case: a pin that no version on disk satisfies is a miss on the build's lazy view
// exactly as on the pull's eager catalog — never the name's other file — so a
// dependency walk over either reports the same unresolvable reference. The
// satisfiable pin beside it is the control that the directory is read at all.
func TestLazyViewAnswersAnUnsatisfiablePinAsTheEagerOneDoes(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	catalogDir := filepath.Join(ws, "catalog")
	for _, d := range multiVersionDocs() {
		if d.version == "1.0.0" && d.kind == "Component" {
			writeCatalogFile(t, catalogDir, d.rel, d.body)
		}
	}
	eager, _, err := LoadPassOneTolerant(ws, "", catalogDir)
	if err != nil {
		t.Fatalf("eager load: %v", err)
	}
	lazy := NewLazyLookup(ws, "", []string{catalogDir}, nil)
	ctx := context.Background()

	for _, p := range []struct {
		name   string
		lookup closure.Lookup
	}{
		{"eager", eager.ClosureLookup(nil, ws, "")},
		{"lazy", lazy.ClosureLookup(nil, ws, "", nil, nil, nil)},
	} {
		control, err := p.lookup.ResolveCatalogEntity(ctx, "test/svc:1.0.0", "component")
		if err != nil || control.NotFound {
			t.Fatalf("%s: control: test/svc:1.0.0 did not resolve (err %v), so the refusal below proves nothing", p.name, err)
		}
		res, err := p.lookup.ResolveCatalogEntity(ctx, "test/svc:2.0.0", "component")
		if err != nil {
			t.Fatalf("%s: resolve: %v", p.name, err)
		}
		if !res.NotFound {
			t.Errorf("%s: test/svc:2.0.0 resolved to version %q (digest %s) with only 1.0.0 on disk; it must be a miss",
				p.name, res.ResolvedVersion, res.Digest)
		}
	}
	if c, ok := lazy.AsCatalogLookup().Component("test/svc:2.0.0"); ok {
		t.Errorf("the lazy component guard answered test/svc:2.0.0 with the %s document; it must be a miss",
			c.Metadata.Version)
	}
}

// TestLazyIndexIsFirstWinPerIdentityAcrossDirectories pins the precedence the
// lazy view shares with the eager merge: the first directory declaring a name AT A
// VERSION claims that identity, and a later directory's other version of the same
// name is a second identity rather than a shadow. So the earlier directory's 1.0.0
// wins its identity, the later directory's 2.0.0 is still selectable, and an
// unversioned reference selects it as the highest — on both backings alike.
func TestLazyIndexIsFirstWinPerIdentityAcrossDirectories(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	dirA, dirB := filepath.Join(ws, "a"), filepath.Join(ws, "b")
	component := func(version, desc string) string {
		return `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/svc
  version: "` + version + `"
  description: ` + desc + `
  labels:
    component.aggrega.ai/type: server
spec: {}
`
	}
	writeCatalogFile(t, dirA, "component/test/svc/1.0.0/component.yaml", component("1.0.0", "a-one"))
	writeCatalogFile(t, dirB, "component/test/svc/1.0.0/component.yaml", component("1.0.0", "b-one"))
	writeCatalogFile(t, dirB, "component/test/svc/2.0.0/component.yaml", component("2.0.0", "b-two"))

	eager, _, err := LoadPassOneTolerant(ws, "", dirA, dirB)
	if err != nil {
		t.Fatalf("eager load: %v", err)
	}
	lazy := NewLazyLookup(ws, "", []string{dirA, dirB}, nil)

	for _, tc := range []struct{ ref, want string }{
		{"test/svc:1.0.0", "a-one"},
		{"test/svc:2.0.0", "b-two"},
		{"test/svc", "b-two"},
	} {
		e, eok := eager.CatalogLookup().Component(tc.ref)
		l, lok := lazy.AsCatalogLookup().Component(tc.ref)
		if !eok || !lok {
			t.Errorf("%s: resolved eager=%v lazy=%v; both must resolve", tc.ref, eok, lok)
			continue
		}
		if e.Metadata.Description != tc.want || l.Metadata.Description != tc.want {
			t.Errorf("%s: eager resolved %q and lazy %q; want %q on both", tc.ref,
				e.Metadata.Description, l.Metadata.Description, tc.want)
		}
	}

	// The document reported for an identity is that identity's own.
	got, ok := lazy.LocalEntityPath("Component", "", "test/svc", "2.0.0")
	if want := filepath.Join("b", "component", "test", "svc", "2.0.0", "component.yaml"); !ok || got != want {
		t.Errorf("the document reported for test/svc 2.0.0 is (%q, %v); want %q", got, ok, want)
	}
	got, ok = lazy.LocalEntityPath("Component", "", "test/svc", "1.0.0")
	if want := filepath.Join("a", "component", "test", "svc", "1.0.0", "component.yaml"); !ok || got != want {
		t.Errorf("the document reported for test/svc 1.0.0 is (%q, %v); want %q", got, ok, want)
	}
}

// TestEntityNamesReportsEachNameOnceAcrossVersions pins that the enumeration of
// what a catalog directory declares is by name: an entity held at two versions is
// one name, and resolving it selects the highest. Each kind the directory index
// holds is a row, so a kind whose enumeration reported one entry per version
// would fail on its own row.
func TestEntityNamesReportsEachNameOnceAcrossVersions(t *testing.T) {
	t.Parallel()
	_, catalogDir, _ := writeMultiVersionCatalog(t)
	lazy := NewLazyLookup(filepath.Dir(catalogDir), "", []string{catalogDir}, nil)

	for _, tc := range []struct {
		kind, want string
		resolve    func(string) (string, bool)
	}{
		{"Component", "test/svc", func(n string) (string, bool) {
			c, ok := lazy.Component(n)
			return c.Metadata.Version, ok
		}},
		{"Feature", "test/tag", func(n string) (string, bool) {
			f, ok := lazy.Feature(n)
			return f.Metadata.Version, ok
		}},
	} {
		names := lazy.EntityNames(tc.kind)
		if len(names) != 1 || names[0] != tc.want {
			t.Errorf("EntityNames(%q) = %q; want [%s] once", tc.kind, names, tc.want)
			continue
		}
		version, ok := tc.resolve(names[0])
		if !ok || version != "2.0.0" {
			t.Errorf("resolving the enumerated %s name gave (%q, %v); an unversioned name selects the highest, 2.0.0",
				tc.kind, version, ok)
		}
	}
}

// TestClosureScopedPillarsValidateTheSelectedVersion holds the build's early
// pillar gate to the version a reference selects: a fault authored only in the
// 2.0.0 document is reported for a closure reaching 2.0.0 and not for one reaching
// 1.0.0, so the gate reads the document the reference resolves to rather than
// whichever file carries the name.
func TestClosureScopedPillarsValidateTheSelectedVersion(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	catalogDir := filepath.Join(ws, "catalog")
	writeCatalogFile(t, catalogDir, "component/test/svc/1.0.0/component.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      greeting: { type: string }
`)
	writeCatalogFile(t, catalogDir, "component/test/svc/2.0.0/component.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/svc
  version: "2.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      storage:
        $ref: "Nonexistent#/schemas/Storage"
`)

	clean := NewLazyLookup(ws, "", []string{catalogDir}, nil)
	if err := clean.ValidateClosureScopedPillars(closure.Closure{Entities: []closure.ResolvedRef{{URI: "test/svc:1.0.0"}}}); err != nil {
		t.Fatalf("a closure reaching only the clean 1.0.0 document was refused: %v", err)
	}
	faulty := NewLazyLookup(ws, "", []string{catalogDir}, nil)
	if err := faulty.ValidateClosureScopedPillars(closure.Closure{Entities: []closure.ResolvedRef{{URI: "test/svc:2.0.0"}}}); err == nil {
		t.Error("a closure reaching the 2.0.0 document, whose schema names a library nothing declares, passed; " +
			"the gate validated another version's document")
	}
}
