// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package closure_test

import (
	"context"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// firstAnswerLookup answers a library the way a registry-backed lookup does over a
// workspace's cache: the first time it is asked for a library it fetches it, and
// every later time it reads the copy that fetch cached. It counts how often it is
// asked for each library by name, and records the version each ask named.
type firstAnswerLookup struct {
	asked   map[string]int
	floors  map[string][]string
	version map[string]string
}

func (f *firstAnswerLookup) ResolveCatalogEntity(context.Context, string, string) (closure.Resolution, error) {
	return closure.Resolution{NotFound: true}, nil
}

func (f *firstAnswerLookup) ResolveAggregate(context.Context, string) (closure.Resolution, error) {
	return closure.Resolution{NotFound: true}, nil
}

func (f *firstAnswerLookup) ResolvePlugin(context.Context, string, string) (closure.Resolution, error) {
	return closure.Resolution{NotFound: true}, nil
}

func (f *firstAnswerLookup) ResolveLibrary(_ context.Context, _ closure.LibraryAxis, uri string) (closure.LibraryResolution, error) {
	name, floor, _ := strings.Cut(uri, ":")
	f.asked[name]++
	f.floors[name] = append(f.floors[name], floor)
	version := floor
	if version == "" {
		version = f.version[name]
	}
	res := closure.Resolution{ResolvedVersion: version, Digest: "sha256:" + name + "-" + version}
	if f.asked[name] == 1 {
		res.ManifestDigest = "sha256:manifest-" + name
		res.ResolutionPath = closure.ResolutionPathRegistry
	} else {
		res.ResolutionPath = closure.ResolutionPathCache
	}
	return closure.LibraryResolution{Resolution: res}, nil
}

// TestDiscoveryKeepsTheResolutionOfALibraryItStillSelects drives the discovery
// fixpoint over a consumer naming LibA, whose body names LibB, whose body names
// nothing — three rounds, each re-selecting every library seen so far.
//
// A library is asked of the lookup once while the version selection keeps choosing
// the version it resolved to, so the closure records what that one answer said: the
// library this run fetched is reported as fetched, with the manifest digest the
// fetch returned, rather than as read from the copy the fetch cached. A later
// requirement that raises the selected version re-asks the lookup at the new one.
func TestDiscoveryKeepsTheResolutionOfALibraryItStillSelects(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		// libB is LibB's body; the second row raises LibA's selected version through it.
		libB string
		// wantAsked is how often LibA is asked of the lookup, and wantFloors the
		// version each ask names.
		wantAsked   int
		wantFloors  []string
		wantVersion string
		wantPath    string
	}{
		{
			name:        "a library no later requirement moves",
			libB:        `type: object`,
			wantAsked:   1,
			wantFloors:  []string{""},
			wantVersion: "1.0.0",
			wantPath:    closure.ResolutionPathRegistry,
		},
		{
			name:        "a library a later requirement raises",
			libB:        `$ref: "LibA:1.2.0#/schemas/Foo"`,
			wantAsked:   2,
			wantFloors:  []string{"", "1.2.0"},
			wantVersion: "1.2.0",
			wantPath:    closure.ResolutionPathCache,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lookup := &firstAnswerLookup{
				asked:   map[string]int{},
				floors:  map[string][]string{},
				version: map[string]string{"LibA": "1.0.0", "LibB": "1.0.0"},
			}
			bodies := map[string]*yaml.Node{
				"LibA": mustParse(t, `$ref: "LibB#/schemas/Bar"`),
				"LibB": mustParse(t, tc.libB),
			}
			body := func(lib closure.ResolvedLibrary) ([]*yaml.Node, bool, error) {
				return []*yaml.Node{bodies[lib.URI]}, true, nil
			}

			var c closure.Closure
			if err := c.DiscoverSchemaLibraries(context.Background(), lookup,
				[]closure.SchemaConsumer{{URI: "consumer", Schema: mustParse(t, `$ref: "LibA#/schemas/Foo"`)}}, body); err != nil {
				t.Fatalf("DiscoverSchemaLibraries: %v", err)
			}

			if got := lookup.asked["LibA"]; got != tc.wantAsked {
				t.Errorf("LibA was asked of the lookup %d times (versions %q); want %d", got, lookup.floors["LibA"], tc.wantAsked)
			}
			if got := strings.Join(lookup.floors["LibA"], ","); got != strings.Join(tc.wantFloors, ",") {
				t.Errorf("LibA was asked at the versions %q; want %q", got, strings.Join(tc.wantFloors, ","))
			}
			if got := lookup.asked["LibB"]; got != 1 {
				t.Errorf("LibB was asked of the lookup %d times; want once", got)
			}
			byURI := map[string]closure.ResolvedLibrary{}
			for _, lib := range c.SchemaLibraries {
				byURI[lib.URI] = lib
			}
			libA := byURI["LibA"]
			if libA.ResolvedVersion != tc.wantVersion || libA.ResolutionPath != tc.wantPath {
				t.Errorf("LibA is recorded at version %q from %q; want %q from %q",
					libA.ResolvedVersion, libA.ResolutionPath, tc.wantVersion, tc.wantPath)
			}
			if tc.wantPath == closure.ResolutionPathRegistry && libA.ManifestDigest != "sha256:manifest-LibA" {
				t.Errorf("LibA is recorded with manifest digest %q; want the one its fetch returned", libA.ManifestDigest)
			}
			if libB := byURI["LibB"]; libB.ResolutionPath != closure.ResolutionPathRegistry || libB.ManifestDigest != "sha256:manifest-LibB" {
				t.Errorf("LibB is recorded from %q with manifest digest %q; want the fetch's answer", libB.ResolutionPath, libB.ManifestDigest)
			}
		})
	}
}
