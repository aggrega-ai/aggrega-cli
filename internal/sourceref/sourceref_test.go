// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package sourceref

import (
	"path/filepath"
	"reflect"
	"testing"
)

// TestIsLocalURLReadsTheReferenceAlone covers the rule over every reference shape
// it has to place. The empty row is the one worth stating on its own: it is the
// answer both callers depend on, because a reference naming nothing is not a
// reference naming something near, and calling it local would hand the empty
// string onward as a path.
func TestIsLocalURLReadsTheReferenceAlone(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		ref  string
		want bool
	}{
		{"names nothing", "", false},
		{"a relative path", "./chart", true},
		{"a parent-relative path", "../chart", true},
		{"an absolute path", "/srv/chart", true},
		{"a bare name", "chart", true},
		{"a registry reference", "oci://example.test/chart", false},
		{"a secure web reference", "https://example.test/chart.tgz", false},
		{"a plain web reference", "http://example.test/chart.tgz", false},
		{"a repository over secure shell", "git+ssh://example.test/repo", false},
		{"a repository over its own transport", "git://example.test/repo", false},
		{"a secure shell reference", "ssh://example.test/repo", false},
		{"a repository named by suffix", "example.test/repo.git", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsLocalURL(tc.ref); got != tc.want {
				t.Errorf("IsLocalURL(%q) = %v, want %v", tc.ref, got, tc.want)
			}
		})
	}
}

// TestIsLocalReadsTheShapeItIsGiven covers how each source shape reaches the rule
// above. The structured rows are what the shared home buys: both callers read the
// same field of a structured source and treat an unfamiliar shape the same way,
// which two implementations agreeing today would not guarantee tomorrow.
func TestIsLocalReadsTheShapeItIsGiven(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		source any
		want   bool
	}{
		{"absent", nil, false},
		{"a scalar naming nothing", "", false},
		{"a scalar path", "./chart", true},
		{"a scalar registry reference", "oci://example.test/chart", false},
		{"structured, naming a path", map[string]any{"url": "./chart"}, true},
		{"structured, naming a registry reference", map[string]any{"url": "oci://example.test/c"}, false},
		{"structured, naming no url", map[string]any{"chart": "api"}, false},
		{"structured, url is not text", map[string]any{"url": 7}, false},
		{"a shape the rule cannot place", 7, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsLocal(tc.source); got != tc.want {
				t.Errorf("IsLocal(%#v) = %v, want %v", tc.source, got, tc.want)
			}
		})
	}
}

// TestIsTemplatedReadsWhetherTheReferenceIsStillText covers the reading that
// decides WHEN a reference is anchored. The rows pair each templated form with
// the plain form nearest to it, because the failure this reading exists to
// prevent is a plain reference deferred (it would then never be anchored) or a
// templated one anchored early (its value would be resolved against text).
func TestIsTemplatedReadsWhetherTheReferenceIsStillText(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		ref  string
		want bool
	}{
		{"names nothing", "", false},
		{"a relative path", "./charts/app", false},
		{"a bare name", "chart", false},
		{"a registry reference", "oci://example.test/chart", false},
		{"the whole reference is one action", "{{ .locals.chart_dir }}", true},
		{"an action inside a path", "charts/{{ .locals.flavor }}/app", true},
		{"an action inside a registry reference", "oci://{{ .locals.repo }}/chart:1.0.0", true},
		{"a trailing action", "./charts/{{ .values.name }}", true},
		// A closing delimiter alone is not an action. The reading is of the opening
		// one because that is what the evaluation parses on, and a reference the
		// evaluation would not treat as a template must not be deferred by this.
		{"a closing delimiter alone", "./charts/app}}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsTemplated(tc.ref); got != tc.want {
				t.Errorf("IsTemplated(%q) = %v, want %v", tc.ref, got, tc.want)
			}
		})
	}
}

// TestReferenceReadsTheOneTermOutOfEitherShape covers the walk both readings and
// the anchoring share. The two columns are separate on purpose: a readable shape
// naming nothing and a shape no reference can be read out of both yield the empty
// string, and every caller that has to tell them apart can only do so from the
// second column.
func TestReferenceReadsTheOneTermOutOfEitherShape(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		source       any
		wantRef      string
		wantReadable bool
	}{
		{"absent", nil, "", false},
		{"a scalar naming nothing", "", "", true},
		{"a scalar path", "./charts/app", "./charts/app", true},
		{"a scalar registry reference", "oci://example.test/chart", "oci://example.test/chart", true},
		{"structured, naming a path", map[string]any{"kind": "helm-oci-chart", "url": "./charts/app"}, "./charts/app", true},
		{"structured, naming a registry reference", map[string]any{"url": "oci://example.test/c"}, "oci://example.test/c", true},
		{"structured, naming no url", map[string]any{"chart": "api"}, "", true},
		{"structured, url is not text", map[string]any{"url": 7}, "", true},
		{"a shape no reference can be read out of", 7, "", false},
		{"an ordered container is not the map arm", []any{"url", "./charts/app"}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ref, readable := Reference(tc.source)
			if ref != tc.wantRef || readable != tc.wantReadable {
				t.Errorf("Reference(%#v) = (%q, %v), want (%q, %v)",
					tc.source, ref, readable, tc.wantRef, tc.wantReadable)
			}
		})
	}
}

// TestIdentityTemplatedSeparatesTheIdentityFromTheRest covers the reading a pin
// turns on, in BOTH directions, because a reading that answered "still text" for
// everything would satisfy every positive row on its own.
//
// The rows are built as pairs. Each identity-bearing field appears once carrying a
// template and the whole set appears once written out in full, so a field dropped
// from the set fails its own row rather than hiding behind a sibling. The
// provider-defined rows are the negative half and are the only ones that separate
// this reading from a substring test over the whole block: a template is present,
// the artifact named is the same one whatever it renders to, and the answer is
// that the identity is knowable.
func TestIdentityTemplatedSeparatesTheIdentityFromTheRest(t *testing.T) {
	t.Parallel()
	const concreteOCI = "oci://example.test/charts/app:1.0.0"
	full := func(overrides map[string]any) map[string]any {
		out := map[string]any{
			"kind":          "helm-oci-chart",
			"url":           concreteOCI,
			"chart":         "app",
			"module_subdir": "modules/app",
			"version":       "1.0.0",
			"ref":           "v1.0.0",
		}
		for k, v := range overrides {
			out[k] = v
		}
		return out
	}
	for _, tc := range []struct {
		name   string
		source any
		want   bool
	}{
		// Concrete: the identity is written out in full and is knowable now.
		{"absent", nil, false},
		{"a scalar naming nothing", "", false},
		{"a scalar registry reference", concreteOCI, false},
		{"a scalar relative path", "./charts/app", false},
		{"structured, every identity field written out", full(nil), false},
		{"structured, a literal version alone", map[string]any{"kind": "helm-oci-chart", "url": concreteOCI, "version": "1.0.0"}, false},
		{"structured, a relative url and a literal version", map[string]any{"kind": "helm-oci-chart", "url": "./charts/app", "version": "1.0.0"}, false},
		// Concrete despite a template: the key is the provider's, not the identity's.
		{"structured, a template in a provider-defined key", full(map[string]any{"registry_alias": "{{ .infra.alias }}"}), false},
		{"structured, a template in a provider-defined key and nothing else", map[string]any{"url": concreteOCI, "workspace": "{{ .values.ws }}"}, false},
		// Still text: one identity-bearing field at a time.
		{"structured, a templated kind", full(map[string]any{"kind": "{{ .infra.kind }}"}), true},
		{"structured, a templated url", full(map[string]any{"url": "oci://{{ .infra.registry }}/charts/app"}), true},
		{"structured, a templated chart", full(map[string]any{"chart": "{{ .values.flavor }}"}), true},
		{"structured, a templated module_subdir", full(map[string]any{"module_subdir": "modules/{{ .values.flavor }}"}), true},
		{"structured, a templated version", full(map[string]any{"version": "{{ .infra.source_version }}"}), true},
		{"structured, a templated ref", full(map[string]any{"ref": "{{ .infra.commit }}"}), true},
		{"a scalar carrying a template", "oci://{{ .infra.registry }}/charts/app:1.0.0", true},
		// A shape no reference can be read out of: the identity was never read, and
		// the lock-time spec build refuses such a value by name.
		{"a shape no reference can be read out of", 7, false},
		{"an ordered container is not the map arm", []any{"url", concreteOCI}, false},
		// A non-text identity field carries no template text to find.
		{"structured, a numeric version", map[string]any{"url": concreteOCI, "version": 1}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IdentityTemplated(tc.source); got != tc.want {
				t.Errorf("IdentityTemplated(%#v) = %v, want %v", tc.source, got, tc.want)
			}
		})
	}
}

// TestIdentityTemplatedReadsEveryFieldItDeclares drives the declared field set
// itself, so a reading that declares a field and then does not consult it fails
// here. That is the shape a hand-written switch over field names would take.
//
// It cannot see a field REMOVED from the declared set, because a removed field
// takes its own row away with it — that half is covered by the named rows in the
// table above, one per field, which fail on the field they name.
func TestIdentityTemplatedReadsEveryFieldItDeclares(t *testing.T) {
	t.Parallel()
	for _, field := range identityFields {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			source := map[string]any{"url": "oci://example.test/charts/app:1.0.0"}
			source[field] = "{{ .infra.x }}"
			if !IdentityTemplated(source) {
				t.Errorf("a template in %q left the identity reported as knowable", field)
			}
		})
	}
}

// TestAnchorSourceRewritesNothingButTheReference covers the anchoring at the level
// a source is authored at, over both shapes.
//
// Two properties are asserted together because a rewrite that got either alone
// would read as correct: the reference comes back resolved, and every other key of
// a structured source comes back exactly as authored — including one carrying a
// template, which this step must not evaluate, and including a version, which
// decides nothing about where a relative path is anchored.
func TestAnchorSourceRewritesNothingButTheReference(t *testing.T) {
	t.Parallel()
	const document = "/srv/catalog/recipes/app.yaml"
	for _, tc := range []struct {
		name     string
		document string
		source   any
		want     any
	}{
		{
			"a scalar relative path is anchored",
			document, "./charts/app", "/srv/catalog/recipes/charts/app",
		},
		{
			"a scalar registry reference is carried through",
			document, "oci://example.test/chart:1.0.0", "oci://example.test/chart:1.0.0",
		},
		{
			"a scalar template is left for the value it produces",
			document, "./charts/{{ .values.flavor }}", "./charts/{{ .values.flavor }}",
		},
		{
			"a structured relative url is anchored and the rest is carried",
			document,
			map[string]any{"kind": "helm-oci-chart", "url": "./charts/app", "version": "1.0.0"},
			map[string]any{"kind": "helm-oci-chart", "url": "/srv/catalog/recipes/charts/app", "version": "1.0.0"},
		},
		{
			"a structured absolute url already names where it is",
			document,
			map[string]any{"kind": "helm-oci-chart", "url": "/opt/charts/app", "version": "1.0.0"},
			map[string]any{"kind": "helm-oci-chart", "url": "/opt/charts/app", "version": "1.0.0"},
		},
		{
			"a structured registry url is carried through",
			document,
			map[string]any{"kind": "helm-oci-chart", "url": "oci://example.test/c", "version": "1.0.0"},
			map[string]any{"kind": "helm-oci-chart", "url": "oci://example.test/c", "version": "1.0.0"},
		},
		{
			"a templated version does not defer the url",
			document,
			map[string]any{"kind": "helm-oci-chart", "url": "./charts/app", "version": "{{ .infra.source_version }}"},
			map[string]any{"kind": "helm-oci-chart", "url": "/srv/catalog/recipes/charts/app", "version": "{{ .infra.source_version }}"},
		},
		{
			"a templated url is left for the value it produces",
			document,
			map[string]any{"kind": "helm-oci-chart", "url": "./charts/{{ .values.flavor }}", "version": "1.0.0"},
			map[string]any{"kind": "helm-oci-chart", "url": "./charts/{{ .values.flavor }}", "version": "1.0.0"},
		},
		{
			"a provider-defined key is carried through unevaluated",
			document,
			map[string]any{"url": "./charts/app", "workspace": "{{ .values.ws }}"},
			map[string]any{"url": "/srv/catalog/recipes/charts/app", "workspace": "{{ .values.ws }}"},
		},
		{
			"a structured source naming no url is untouched",
			document, map[string]any{"chart": "api"}, map[string]any{"chart": "api"},
		},
		{
			"a url that is not text is untouched",
			document, map[string]any{"url": 7}, map[string]any{"url": 7},
		},
		{
			"no declaring document leaves a relative url alone",
			"",
			map[string]any{"url": "./charts/app"},
			map[string]any{"url": "./charts/app"},
		},
		{
			"a shape no reference can be read out of is untouched",
			document, 7, 7,
		},
		{"an absent source stays absent", document, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := AnchorSource(tc.document, tc.source)
			if err != nil {
				t.Fatalf("AnchorSource(%q, %#v): %v", tc.document, tc.source, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("AnchorSource(%q, %#v) = %#v, want %#v", tc.document, tc.source, got, tc.want)
			}
		})
	}
}

// TestAnchorSourceDoesNotWriteIntoTheAuthoredMap pins the property the table above
// structurally cannot see: the caller's own map is a parsed document that other
// readers hold, so the rewrite has to produce a new one rather than edit it.
func TestAnchorSourceDoesNotWriteIntoTheAuthoredMap(t *testing.T) {
	t.Parallel()
	authored := map[string]any{"kind": "helm-oci-chart", "url": "./charts/app", "version": "1.0.0"}
	if _, err := AnchorSource("/srv/catalog/recipes/app.yaml", authored); err != nil {
		t.Fatalf("AnchorSource: %v", err)
	}
	if authored["url"] != "./charts/app" {
		t.Errorf("the authored map was rewritten in place: url is now %#v", authored["url"])
	}
}

// TestAnchorToDocumentResolvesOnlyWhatItShould covers the one statement of what
// a local relative source is resolved against, over every reference shape and
// both document states.
//
// The four pass-through rows are the point of the table rather than edge cases:
// each is a value that joining a directory onto would destroy, and each is a
// value a source template can produce. A resolution that anchored them would be
// indistinguishable, at this level, from the text-anchoring this replaces.
func TestAnchorToDocumentResolvesOnlyWhatItShould(t *testing.T) {
	t.Parallel()
	const document = "/srv/catalog/recipes/app.yaml"
	for _, tc := range []struct {
		name     string
		document string
		ref      string
		want     string
	}{
		{"a relative path is anchored to the declaring directory", document, "./charts/app", "/srv/catalog/recipes/charts/app"},
		{"a bare name is anchored too", document, "charts/app", "/srv/catalog/recipes/charts/app"},
		{"a parent-relative path resolves out of the directory", document, "../shared/charts/app", "/srv/catalog/shared/charts/app"},
		{"an absolute path already names where it is", document, "/opt/charts/app", "/opt/charts/app"},
		{"a reference naming nothing stays naming nothing", document, "", ""},
		{"a registry reference is carried through", document, "oci://example.test/chart:1.0.0", "oci://example.test/chart:1.0.0"},
		{"a web reference is carried through", document, "https://example.test/chart.tgz", "https://example.test/chart.tgz"},
		{"a repository reference is carried through", document, "example.test/repo.git", "example.test/repo.git"},
		// No declaring document: an entity resolved from a registry lives in a
		// content-addressed slot rather than a file, so there is no directory for a
		// relative reference to be relative to and it is handed back.
		{"no declaring document leaves a relative path alone", "", "./charts/app", "./charts/app"},
		{"no declaring document leaves an absolute path alone", "", "/opt/charts/app", "/opt/charts/app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := AnchorToDocument(tc.document, tc.ref)
			if err != nil {
				t.Fatalf("AnchorToDocument(%q, %q): %v", tc.document, tc.ref, err)
			}
			if got != tc.want {
				t.Errorf("AnchorToDocument(%q, %q) = %q, want %q", tc.document, tc.ref, got, tc.want)
			}
		})
	}
}

// TestAnchorToDocumentAnchorsARelativeDocumentAgainstTheProcessDirectory pins
// the one case the table above cannot state as a literal: a declaring document
// named relatively still yields an absolute anchor, so a value resolved through
// it does not change meaning when the build is run from elsewhere.
func TestAnchorToDocumentAnchorsARelativeDocumentAgainstTheProcessDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	got, err := AnchorToDocument("catalog/recipes/app.yaml", "./charts/app")
	if err != nil {
		t.Fatalf("AnchorToDocument: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("a relative document must still yield an absolute anchor; got %q", got)
	}
	// EvalSymlinks on the expectation: a temp directory is reached through a
	// symlink on some platforms, and the property under test is which directory
	// was anchored to rather than which spelling of it the process reports.
	want := filepath.Join(dir, "catalog", "recipes", "charts", "app")
	if resolved, rerr := filepath.EvalSymlinks(dir); rerr == nil {
		want = filepath.Join(resolved, "catalog", "recipes", "charts", "app")
	}
	if got != want && got != filepath.Join(dir, "catalog", "recipes", "charts", "app") {
		t.Errorf("anchored against the wrong base: got %q, want %q", got, want)
	}
}

// TestDeclaredSeparatesNamingNothingFromNamingSomething covers the one condition
// two readers depend on agreeing about: a source that evaluated to an empty
// string leaves the entity with none just as an absent source does.
func TestDeclaredSeparatesNamingNothingFromNamingSomething(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		source any
		want   bool
	}{
		{"absent", nil, false},
		{"an empty reference", "", false},
		{"a relative path", "./chart", true},
		{"a registry reference", "oci://example.test/chart", true},
		{"a structured source", map[string]any{"url": "oci://example.test/chart"}, true},
		{"a structured source naming no url", map[string]any{"kind": "helm-oci-chart"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Declared(tc.source); got != tc.want {
				t.Errorf("Declared(%#v) = %v, want %v", tc.source, got, tc.want)
			}
		})
	}
}

// TestNeedsFetchAsksAboutTheTransportNotTheShape covers the rule a run consults
// about its own reach. Two rows carry the whole point and would each be wrong
// under a shape-based reading: a STRUCTURED source naming a path the workspace
// already holds needs no fetch, and a bare STRING naming a transport does.
// Reading "is it a structured source" instead would get both backwards.
func TestNeedsFetchAsksAboutTheTransportNotTheShape(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		source any
		want   bool
	}{
		{"absent", nil, false},
		{"an empty reference", "", false},
		{"a relative path", "./module", false},
		{"an absolute path", "/srv/chart", false},
		{"a structured source naming a relative path", map[string]any{"url": "./chart"}, false},
		{"a registry reference", "oci://example.test/chart", true},
		{"a web reference", "https://example.test/chart.tgz", true},
		{"a repository named by suffix", "example.test/repo.git", true},
		{"a structured registry source", map[string]any{"kind": "helm-oci-chart", "url": "oci://example.test/chart"}, true},
		{"a structured web source", map[string]any{"kind": "helm-https-chart", "url": "https://example.test/charts"}, true},
		{"a structured source naming no url", map[string]any{"kind": "helm-oci-chart"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := NeedsFetch(tc.source); got != tc.want {
				t.Errorf("NeedsFetch(%#v) = %v, want %v", tc.source, got, tc.want)
			}
		})
	}
}
