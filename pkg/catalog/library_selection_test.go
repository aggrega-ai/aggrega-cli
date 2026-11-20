// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"errors"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func libraryIndex(t *testing.T, held ...[2]string) map[string]types.MappingLibrary {
	t.Helper()
	idx := map[string]types.MappingLibrary{}
	for _, h := range held {
		var l types.MappingLibrary
		l.Metadata.Name, l.Metadata.Version = h[0], h[1]
		idx[entityIndexKey(h[0], h[1])] = l
	}
	return idx
}

func TestSelectLibraryVersion(t *testing.T) {
	lib := func(vs ...string) [][2]string {
		var out [][2]string
		for _, v := range vs {
			out = append(out, [2]string{"Lib", v})
		}
		return out
	}
	tests := []struct {
		name    string
		held    [][2]string
		ref     string
		want    string
		wantOK  bool
		wantErr bool
	}{
		{"bare picks highest", lib("1.0.0", "1.1.0"), "Lib", "Lib:1.1.0", true, false},
		{"bare skips prerelease", lib("1.0.0", "1.1.0", "1.2.0-rc.1"), "Lib", "Lib:1.1.0", true, false},
		{"floor held exactly and a higher one held", lib("1.0.0", "1.1.0"), "Lib:1.0.0", "Lib:1.1.0", true, false},
		{"floor held exactly and alone", lib("1.0.0"), "Lib:1.0.0", "Lib:1.0.0", true, false},
		{"floor selects highest above", lib("1.0.0", "1.2.0", "1.3.0"), "Lib:1.1.0", "Lib:1.3.0", true, false},
		{"short floor reads as 1.1.0", lib("1.0.0", "1.1.0"), "Lib:1.1", "Lib:1.1.0", true, false},
		{"prerelease named literally", lib("1.2.0-rc.1"), "Lib:1.2.0-rc.1", "Lib:1.2.0-rc.1", true, false},
		{"floor above all held", lib("1.0.0"), "Lib:1.1.0", "", false, true},
		{"other major only", lib("2.0.0"), "Lib:1.0.0", "", false, true},
		{"major 0 is one major", lib("0.5.0", "0.6.0"), "Lib:0.5.1", "Lib:0.6.0", true, false},
		{"name not held", lib("1.0.0"), "Other", "", false, false},
		{"floor tie keeps the smaller key", lib("1.0.0+b", "1.0.0+a"), "Lib:1.0.0", "Lib:1.0.0+a", true, false},
		{"bare tie keeps the smaller key", lib("1.0.0+b", "1.0.0+a"), "Lib", "Lib:1.0.0+a", true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok, err := selectLibraryVersion(libraryIndex(t, tc.held...), tc.ref, mappingLibraryMeta)
			if tc.wantErr {
				if !errors.Is(err, ErrUnsatisfiableEntityVersion()) {
					t.Fatalf("err = %v, want ErrUnsatisfiableEntityVersion", err)
				}
			} else if err != nil {
				t.Fatalf("unexpected err %v", err)
			}
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("got (%q, %v), want (%q, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestSelectLibraryVersionMalformedFloor(t *testing.T) {
	idx := libraryIndex(t, [2]string{"Lib", "1.0.0"})
	_, ok, err := selectLibraryVersion(idx, "Lib:one", mappingLibraryMeta)
	if ok || err == nil || errors.Is(err, ErrUnsatisfiableEntityVersion()) {
		t.Fatalf("got ok=%v err=%v, want the malformed-pin fault", ok, err)
	}
}

func TestDefaultLibrarySelection(t *testing.T) {
	idx := libraryIndex(t, [2]string{"A", "1.0.0"}, [2]string{"A", "1.1.0"}, [2]string{"B", "2.0.0"})
	want := map[string]types.MappingLibrary{
		"A": idx[entityIndexKey("A", "1.1.0")],
		"B": idx[entityIndexKey("B", "2.0.0")],
	}
	for i := 0; i < 20; i++ {
		if got := defaultLibrarySelection(idx, mappingLibraryMeta); !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d: got %v, want %v", i, got, want)
		}
	}
}

func TestClosureLibrarySelection(t *testing.T) {
	idx := libraryIndex(t, [2]string{"A", "1.0.0"}, [2]string{"A", "1.1.0"}, [2]string{"B", "2.0.0"})
	got := closureLibrarySelection(idx, []closure.ResolvedLibrary{
		{URI: "A", ResolvedVersion: "1.0.0"},
		{URI: "C", ResolvedVersion: "1.0.0"},
	})
	want := map[string]types.MappingLibrary{"A": idx[entityIndexKey("A", "1.0.0")]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
