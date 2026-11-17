// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package closure

import (
	"errors"
	"strings"
	"testing"
)

// holdRefusingSource is a LibrarySource whose hold of a resolved library returns
// refusal, and which holds nothing else.
type holdRefusingSource struct {
	recipesOnlySource
	refusal error
}

func (s holdRefusingSource) HoldResolvedLibrary(LibraryAxis, ResolvedLibrary) error { return s.refusal }

// TestHeldLibraryBodiesNamesWhatDisagrees: the body accessor reads a resolved
// library at the version its resolution recorded. A copy at that version is read.
// Otherwise the discovery halts with the unresolvable-reference condition naming
// the library at that version and saying what disagrees with the resolution: the
// version the copy held declares, or that no copy at that version could be held,
// followed by the refusal the source met holding one where it names one.
func TestHeldLibraryBodiesNamesWhatDisagrees(t *testing.T) {
	t.Parallel()
	lib := ResolvedLibrary{URI: "Lib", ResolvedVersion: "1.1.0"}
	held := func(version string) func(string) (string, []string, bool) {
		return func(string) (string, []string, bool) { return version, []string{"body of " + version}, true }
	}
	none := func(string) (string, []string, bool) { return "", nil, false }
	rows := []struct {
		name    string
		refusal error
		read    func(ref string) (string, []string, bool)
		// want is the end of the halt's text; empty where the body is read.
		want string
	}{
		{name: "a copy at the resolved version", read: held("1.1.0")},
		{name: "a copy held at another version", read: held("1.2.0"),
			want: `library "Lib:1.1.0": the copy held declares version 1.2.0`},
		{name: "no copy held, the source naming its refusal", read: none, refusal: errors.New("the slot holds other bytes"),
			want: `library "Lib:1.1.0": no copy at that version could be held: the slot holds other bytes`},
		{name: "no copy held, the source naming no refusal", read: none,
			want: `library "Lib:1.1.0": no copy at that version could be held`},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			var asked string
			read := func(ref string) (string, []string, bool) {
				asked = ref
				return row.read(ref)
			}
			body := heldLibraryBodies(holdRefusingSource{refusal: row.refusal}, LibraryAxisNamed("MappingLibraries"), read)
			bodies, found, err := body(lib)
			if asked != "Lib:1.1.0" {
				t.Errorf("the accessor read %q; want the resolved Lib:1.1.0", asked)
			}
			if row.want == "" {
				if err != nil || !found || len(bodies) != 1 {
					t.Fatalf("body = %q (found %v), %v; want the resolved copy's body", bodies, found, err)
				}
				return
			}
			if found || !errors.Is(err, ErrUnresolvableURI()) {
				t.Fatalf("body = found %v, %v; want the unresolvable-reference condition", found, err)
			}
			if !strings.HasSuffix(err.Error(), row.want) {
				t.Errorf("the halt %q does not end with %q", err.Error(), row.want)
			}
		})
	}
}
