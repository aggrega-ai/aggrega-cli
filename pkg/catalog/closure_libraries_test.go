// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// closureAxisCase is one library kind as the closure-reading tests drive it: how a
// closure resolving Lib at a version is written for the kind's axis, and which
// versions a LibrarySet carries on that axis.
type closureAxisCase struct {
	kind     string
	resolved func(version string) closure.Closure
	read     func(s LibrarySet) []string
}

func closureAxisCases() []closureAxisCase {
	lib := func(version string) []closure.ResolvedLibrary {
		return []closure.ResolvedLibrary{{URI: "Lib", ResolvedVersion: version}}
	}
	return []closureAxisCase{
		{
			kind:     "SchemaLibrary",
			resolved: func(v string) closure.Closure { return closure.Closure{SchemaLibraries: lib(v)} },
			read: func(s LibrarySet) []string {
				var out []string
				for name, l := range s.Schemas {
					out = append(out, name+":"+l.Metadata.Version)
				}
				return out
			},
		},
		{
			kind:     "MappingLibrary",
			resolved: func(v string) closure.Closure { return closure.Closure{MappingLibraries: lib(v)} },
			read: func(s LibrarySet) []string {
				var out []string
				for name, l := range s.Mappings {
					out = append(out, name+":"+l.Metadata.Version)
				}
				return out
			},
		},
		{
			kind:     "WorkloadLibrary",
			resolved: func(v string) closure.Closure { return closure.Closure{WorkloadLibraries: lib(v)} },
			read: func(s LibrarySet) []string {
				var out []string
				for name, l := range s.Workloads {
					out = append(out, name+":"+l.Metadata.Version)
				}
				return out
			},
		},
		{
			kind:     "ArtifactLibrary",
			resolved: func(v string) closure.Closure { return closure.Closure{ArtifactLibraries: lib(v)} },
			read: func(s LibrarySet) []string {
				var out []string
				for name, l := range s.Artifacts {
					out = append(out, name+":"+l.Metadata.Version)
				}
				return out
			},
		},
	}
}

// TestClosureLibraries: a view holding Lib at 1.0.0 and 1.1.0 answers a closure
// that resolved Lib at 1.0.0 with exactly 1.0.0 on that library's axis, and one
// that resolved 1.1.0 with 1.1.0 — never the version a bare reference selects —
// on each of the four axes. A closure resolving no library answers none.
func TestClosureLibraries(t *testing.T) {
	t.Parallel()
	kinds := libraryKindCases()
	for i, axis := range closureAxisCases() {
		k := kinds[i]
		if k.kind != axis.kind {
			t.Fatalf("case tables disagree on kind order: %s vs %s", k.kind, axis.kind)
		}
		for _, version := range []string{"1.0.0", "1.1.0"} {
			t.Run(axis.kind+"/"+version, func(t *testing.T) {
				t.Parallel()
				ws := t.TempDir()
				writeFixture(t, ws, "a-lib.yaml", k.doc("1.0.0"))
				writeFixture(t, ws, "b-lib.yaml", k.doc("1.1.0"))
				view := NewLazyLookup(ws, "", []string{ws}, nil)

				got := axis.read(view.ClosureLibraries(axis.resolved(version)))
				if want := []string{"Lib:" + version}; !reflect.DeepEqual(got, want) {
					t.Errorf("libraries for a closure resolving Lib at %s = %v, want %v", version, got, want)
				}
				if got := axis.read(view.ClosureLibraries(closure.Closure{})); len(got) != 0 {
					t.Errorf("libraries for a closure resolving none = %v, want none", got)
				}
			})
		}
	}
}

// TestClosureLibrariesOmitsAnUnheldVersion: a closure naming a version the view
// does not hold gets no library for it, rather than another version of the name.
func TestClosureLibrariesOmitsAnUnheldVersion(t *testing.T) {
	t.Parallel()
	for i, axis := range closureAxisCases() {
		k := libraryKindCases()[i]
		t.Run(axis.kind, func(t *testing.T) {
			t.Parallel()
			ws := t.TempDir()
			writeFixture(t, ws, "lib.yaml", k.doc("1.0.0"))
			view := NewLazyLookup(ws, "", []string{ws}, nil)
			if got := axis.read(view.ClosureLibraries(axis.resolved("1.5.0"))); len(got) != 0 {
				t.Errorf("libraries for a closure resolving Lib at an unheld 1.5.0 = %v, want none", got)
			}
		})
	}
}

// TestValidateClosureScopedPillarsReadsClosureVersion: a Component whose schema
// references Lib's Settings schema, which only Lib 1.0.0 declares, validates when
// the closure resolved Lib at 1.0.0 and is refused when it resolved 1.1.0, though
// 1.1.0 is what a bare reference selects.
func TestValidateClosureScopedPillarsReadsClosureVersion(t *testing.T) {
	t.Parallel()
	write := func(ws, name, body string) {
		path := filepath.Join(ws, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	const comp = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: test/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      settings:
        $ref: "Lib#/schemas/Settings"
`
	const lib = `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: Lib
  version: %s
spec:
  schemas:
    %s:
      level: { type: string }
`
	for _, tc := range []struct {
		resolved string
		refused  bool
	}{
		{resolved: "1.0.0", refused: false},
		{resolved: "1.1.0", refused: true},
	} {
		t.Run(tc.resolved, func(t *testing.T) {
			t.Parallel()
			ws := t.TempDir()
			write(ws, "comp.yaml", comp)
			write(ws, "a-lib.yaml", fmt.Sprintf(lib, "1.0.0", "Settings"))
			write(ws, "b-lib.yaml", fmt.Sprintf(lib, "1.1.0", "Renamed"))
			view := NewLazyLookup(ws, "", []string{ws}, nil)
			cl := closure.Closure{
				Entities:        []closure.ResolvedRef{{URI: "test/svc:1.0.0"}},
				SchemaLibraries: []closure.ResolvedLibrary{{URI: "Lib", ResolvedVersion: tc.resolved}},
			}
			err := view.ValidateClosureScopedPillars(t.Context(), cl, view.ClosureLibraries(cl).Schemas)
			if tc.refused {
				if err == nil {
					t.Fatalf("closure resolving Lib at %s passed; the schema reference needs 1.0.0's Settings", tc.resolved)
				}
				if !strings.Contains(cerrors.CauseChain(err), "Lib#/schemas/Settings") {
					t.Errorf("the refusal does not name the unresolved fragment Lib#/schemas/Settings: %s", cerrors.CauseChain(err))
				}
				return
			}
			if !tc.refused && err != nil {
				t.Errorf("closure resolving Lib at %s was refused: %v", tc.resolved, err)
			}
		})
	}
}
