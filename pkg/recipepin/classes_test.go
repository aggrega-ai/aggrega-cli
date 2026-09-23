// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package recipepin

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestEveryPinListTheLockCarriesIsAClassTheHaltKnows holds the class table to the
// two structs that carry the pin lists, field for field and in both directions.
//
// The unpinned-entity halt is built over the table, so a pin list the lock gains
// with no row here is a class no default pull would ever stop on — the silent
// half of adding a class. And a row naming a field neither struct carries is a
// class the halt reports that nothing can hold. So adding a class is an added
// field on both structs plus an added row, and this refuses any of the three
// arriving alone.
func TestEveryPinListTheLockCarriesIsAClassTheHaltKnows(t *testing.T) {
	rows := map[string]bool{}
	for _, class := range lockClasses {
		if rows[class.field] {
			t.Errorf("two class rows name the field %q", class.field)
		}
		rows[class.field] = true
		if class.noun == "" || class.unrecorded == nil || class.count == nil {
			t.Errorf("the class row for %q is incomplete: noun %q, comparison set %v, count set %v",
				class.field, class.noun, class.unrecorded != nil, class.count != nil)
		}
	}
	for _, pinSet := range []reflect.Type{reflect.TypeOf(Lock{}), reflect.TypeOf(Pins{})} {
		carried := map[string]bool{}
		for i := 0; i < pinSet.NumField(); i++ {
			f := pinSet.Field(i)
			if f.Name == "Version" {
				continue
			}
			carried[f.Name] = true
			if !rows[f.Name] {
				t.Errorf("%s carries the pin list %s and the class table has no row for it, so no default pull would "+
					"halt on an entity missing a pin of that class", pinSet.Name(), f.Name)
			}
		}
		for field := range rows {
			if !carried[field] {
				t.Errorf("the class table names %s, which %s does not carry", field, pinSet.Name())
			}
		}
	}
}

// TestUnrecordedPinsReportsMissingAndStaleAndNothingElse drives the comparison
// over each case it has to tell apart, class by class.
func TestUnrecordedPinsReportsMissingAndStaleAndNothingElse(t *testing.T) {
	src := types.SourceDigest{Kind: "helm-https-chart", URL: "https://example.invalid/c.tgz", Version: "1.0.0", Digest: "sha256:aa"}
	art := types.ArtifactDigest{Name: "netpol.yaml", Digest: "sha256:bb"}
	want := Pins{SourceDigests: []types.SourceDigest{src}, ArtifactDigests: []types.ArtifactDigest{art}}

	staleSrc := src
	staleSrc.Digest = "sha256:00"
	staleArt := art
	staleArt.Digest = "sha256:00"
	extra := types.ArtifactDigest{Name: "gone.yaml", Digest: "sha256:cc"}

	cases := []struct {
		name     string
		recorded Pins
		want     []Unrecorded
	}{
		{
			name:     "every pin recorded at its value reports nothing",
			recorded: want,
		},
		{
			name: "nothing recorded reports both classes, missing",
			want: []Unrecorded{
				{Class: "remote source", Input: "https://example.invalid/c.tgz@1.0.0"},
				{Class: "imported file", Input: "netpol.yaml"},
			},
		},
		{
			name:     "a pin recorded at another value is stale",
			recorded: Pins{SourceDigests: []types.SourceDigest{staleSrc}, ArtifactDigests: []types.ArtifactDigest{staleArt}},
			want: []Unrecorded{
				{Class: "remote source", Input: "https://example.invalid/c.tgz@1.0.0", Stale: true},
				{Class: "imported file", Input: "netpol.yaml", Stale: true},
			},
		},
		{
			name:     "one class present, the other missing, names only the missing one",
			recorded: Pins{SourceDigests: []types.SourceDigest{src}},
			want:     []Unrecorded{{Class: "imported file", Input: "netpol.yaml"}},
		},
		{
			name:     "a recorded pin for an input the entity no longer reads is not a finding",
			recorded: Pins{SourceDigests: []types.SourceDigest{src}, ArtifactDigests: []types.ArtifactDigest{art, extra}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := UnrecordedPins(want, tc.recorded)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("UnrecordedPins = %+v; want %+v", got, tc.want)
			}
		})
	}
}

// TestTheUnpinnedHaltNamesTheEntityEachClassAndTheStep pins what a reader of the
// halt is given: the registered condition and its short code, the entity, every
// missing input under the class it belongs to, and the command that records them.
func TestTheUnpinnedHaltNamesTheEntityEachClassAndTheStep(t *testing.T) {
	err := UnpinnedEntity("/ws/catalog/recipes/server/recipe.yaml", "catalog/recipes/server/recipe.yaml",
		"Recipe", "server", []Unrecorded{
			{Class: "remote source", Input: "https://example.invalid/c.tgz@1.0.0"},
			{Class: "imported file", Input: "netpol.yaml", Stale: true},
		})
	if !errors.Is(err, ErrEntityNotPinned()) {
		t.Fatalf("the halt must carry the unpinned-entity condition; got %v", err)
	}
	if code, ok := cerrors.ShortCodeOf(err); !ok || code != "E0302" {
		t.Errorf("the halt's short code = %q (found %v); want E0302", code, ok)
	}
	msg := err.Error()
	for _, want := range []string{
		`Recipe "server"`,
		"remote source https://example.invalid/c.tgz@1.0.0: no pin recorded",
		"imported file netpol.yaml: the recorded pin no longer matches it",
		"aggrega pin catalog/recipes/server/recipe.yaml",
		"pin_sources_at_pull: true",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not carry %q:\n%s", want, msg)
		}
	}
}

// TestCountPinsReportsEveryClassEvenWhenEmpty holds the per-class count to the
// class table, so a reader of a pin outcome sees every class the lock can hold.
func TestCountPinsReportsEveryClassEvenWhenEmpty(t *testing.T) {
	got := CountPins(Pins{ArtifactDigests: []types.ArtifactDigest{{Name: "a"}, {Name: "b"}}})
	want := []ClassCount{{Class: "remote source", Pins: 0}, {Class: "imported file", Pins: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CountPins = %+v; want %+v", got, want)
	}
}
