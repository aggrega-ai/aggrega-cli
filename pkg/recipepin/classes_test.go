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
			// The version is the lock's schema, and the ignore rules decide which
			// files the local-source class covers: neither is a pin list a halt
			// could name an input of.
			if f.Name == "Version" || f.Name == "Ignore" {
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

// TestUnrecordedPinsReadsALocalSourceAsOneSet drives the local-source class over
// the cases that make it the one exception to "a recorded pin the entity no
// longer needs is not a finding": a source's files are one input, so a file the
// lock records that the source no longer holds is a change to it, which a build
// refuses and a pull must report too.
func TestUnrecordedPinsReadsALocalSourceAsOneSet(t *testing.T) {
	chart := types.LocalSourceDigest{Path: "chart/Chart.yaml", Digest: "sha256:aa"}
	tmpl := types.LocalSourceDigest{Path: "chart/templates/cm.yaml", Digest: "sha256:bb"}
	editedTmpl := types.LocalSourceDigest{Path: "chart/templates/cm.yaml", Digest: "sha256:cc"}
	added := types.LocalSourceDigest{Path: "chart/templates/new.yaml", Digest: "sha256:dd"}

	cases := []struct {
		name     string
		want     []types.LocalSourceDigest
		recorded []types.LocalSourceDigest
		report   []Unrecorded
	}{
		{name: "the same files at the same digests report nothing", want: []types.LocalSourceDigest{chart, tmpl}, recorded: []types.LocalSourceDigest{chart, tmpl}},
		{
			name: "a source never pinned reports every file, missing",
			want: []types.LocalSourceDigest{chart, tmpl},
			report: []Unrecorded{
				{Class: "local source file", Input: "chart/Chart.yaml"},
				{Class: "local source file", Input: "chart/templates/cm.yaml"},
			},
		},
		{
			name:     "a file whose content moved is stale",
			want:     []types.LocalSourceDigest{chart, editedTmpl},
			recorded: []types.LocalSourceDigest{chart, tmpl},
			report:   []Unrecorded{{Class: "local source file", Input: "chart/templates/cm.yaml", Stale: true}},
		},
		{
			name:     "a file added to a pinned source is missing",
			want:     []types.LocalSourceDigest{chart, tmpl, added},
			recorded: []types.LocalSourceDigest{chart, tmpl},
			report:   []Unrecorded{{Class: "local source file", Input: "chart/templates/new.yaml"}},
		},
		{
			name:     "a file the source no longer holds is stale, reported after the files it holds",
			want:     []types.LocalSourceDigest{chart, editedTmpl},
			recorded: []types.LocalSourceDigest{chart, tmpl, added},
			report: []Unrecorded{
				{Class: "local source file", Input: "chart/templates/cm.yaml", Stale: true},
				{Class: "local source file", Input: "chart/templates/new.yaml", Stale: true},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := UnrecordedPins(Pins{LocalSourceDigests: tc.want}, Pins{LocalSourceDigests: tc.recorded})
			if !reflect.DeepEqual(got, tc.report) {
				t.Errorf("UnrecordedPins = %+v; want %+v", got, tc.report)
			}
		})
	}
}

// TestTheHoldRefusesALocalSourceNobodyPinnedAndOneThatMoved reads the local-source
// class through what the hold composes — the comparison, then the halt built from
// what it found — over the three shapes a reader of the halt meets: a source the
// lock records no pin for, alone and beside an imported file whose pin the lock
// does record, and a source whose lock records its files at digests they no
// longer have. Each is refused under the unpinned-entity condition and its short
// code, naming each file that is missing or stale and nothing that is not.
func TestTheHoldRefusesALocalSourceNobodyPinnedAndOneThatMoved(t *testing.T) {
	chart := types.LocalSourceDigest{Path: "chart/Chart.yaml", Digest: "sha256:aa"}
	tmpl := types.LocalSourceDigest{Path: "chart/templates/cm.yaml", Digest: "sha256:bb"}
	editedTmpl := types.LocalSourceDigest{Path: "chart/templates/cm.yaml", Digest: "sha256:cc"}
	art := types.ArtifactDigest{Name: "netpol.yaml", Digest: "sha256:dd"}
	source := []types.LocalSourceDigest{chart, tmpl}

	hold := func(want, recorded Pins) error {
		missing := UnrecordedPins(want, recorded)
		if len(missing) == 0 {
			return nil
		}
		return UnpinnedEntity("/ws/catalog/recipes/server/recipe.yaml", "catalog/recipes/server/recipe.yaml",
			"Recipe", "server", missing, Remedy{Operation: "pull", Setting: "pin_sources_at_pull"})
	}

	rows := []struct {
		name          string
		want          Pins
		recorded      Pins
		carries, lack []string
	}{
		{
			name:    "a local source the lock records no pin for is refused naming each file",
			want:    Pins{LocalSourceDigests: source},
			carries: []string{"local source file chart/Chart.yaml: no pin recorded", "local source file chart/templates/cm.yaml: no pin recorded"},
		},
		{
			name:     "beside an imported file the lock does pin, only the local source's files are named",
			want:     Pins{ArtifactDigests: []types.ArtifactDigest{art}, LocalSourceDigests: source},
			recorded: Pins{ArtifactDigests: []types.ArtifactDigest{art}},
			carries:  []string{"local source file chart/Chart.yaml: no pin recorded", "local source file chart/templates/cm.yaml: no pin recorded"},
			lack:     []string{"imported file netpol.yaml"},
		},
		{
			name:     "a local source whose recorded pins no longer match is refused naming the moved file",
			want:     Pins{LocalSourceDigests: []types.LocalSourceDigest{chart, editedTmpl}},
			recorded: Pins{LocalSourceDigests: source},
			carries:  []string{"local source file chart/templates/cm.yaml: the recorded pin no longer matches it"},
			lack:     []string{"local source file chart/Chart.yaml"},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			err := hold(row.want, row.recorded)
			if !errors.Is(err, ErrEntityNotPinned()) {
				t.Fatalf("the hold must refuse under the unpinned-entity condition; got %v", err)
			}
			if code, ok := cerrors.ShortCodeOf(err); !ok || code != "E0302" {
				t.Errorf("the halt's short code = %q (found %v); want E0302", code, ok)
			}
			for _, want := range append([]string{`Recipe "server"`}, row.carries...) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the halt does not carry %q:\n%v", want, err)
				}
			}
			for _, not := range row.lack {
				if strings.Contains(err.Error(), not) {
					t.Errorf("the halt names %q, which is not missing or stale:\n%v", not, err)
				}
			}
		})
	}

	t.Run("a local source whose lock records every file at its digest is not refused", func(t *testing.T) {
		if err := hold(Pins{LocalSourceDigests: source}, Pins{LocalSourceDigests: source}); err != nil {
			t.Errorf("a lock recording every file of the source at its digest must be accepted; got %v", err)
		}
	})
}

// TestTheUnpinnedHaltNamesTheEntityEachClassAndTheStep pins what a reader of the
// halt is given: the registered condition and its short code, the entity, every
// missing input under the class it belongs to, and the command that records them.
func TestTheUnpinnedHaltNamesTheEntityEachClassAndTheStep(t *testing.T) {
	err := UnpinnedEntity("/ws/catalog/recipes/server/recipe.yaml", "catalog/recipes/server/recipe.yaml",
		"Recipe", "server", []Unrecorded{
			{Class: "remote source", Input: "https://example.invalid/c.tgz@1.0.0"},
			{Class: "imported file", Input: "netpol.yaml", Stale: true},
		}, Remedy{Operation: "pull", Setting: "pin_sources_at_pull"})
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

// TestOneHaltNamesTheRemedyOfTheOperationThatRaisedIt holds the one condition to
// carrying each operation's own remediation, and nothing else that differs.
//
// Two operations raise the halt, and what a reader can do about it differs by
// which one they ran: the setting is each operation's own, and only a caller that
// has a way to ask for one run names it. Both rows must be the same condition
// under the same short code over the same composed values, so the remediation is
// the one thing a second operation adds — and the pull row is asserted byte for
// byte, because the remediation a pull prints is the one the published reference
// quotes, and parameterising it must not move it.
func TestOneHaltNamesTheRemedyOfTheOperationThatRaisedIt(t *testing.T) {
	missing := []Unrecorded{{Class: "remote source", Input: "https://example.invalid/c.tgz@1.0.0"}}
	const values = `(Recipe "server", remote source https://example.invalid/c.tgz@1.0.0: no pin recorded)`
	rows := []struct {
		name   string
		remedy Remedy
		want   string
	}{
		{
			name:   "a pull names the author's step and its own setting, and no per-run request",
			remedy: Remedy{Operation: "pull", Setting: "pin_sources_at_pull"},
			want: "remediation: run `aggrega pin catalog/recipes/server/recipe.yaml` and commit the entity.lock it writes " +
				"beside the entity; to have every pull in this workspace record the pins itself instead, set " +
				"`pin_sources_at_pull: true` in the workspace configuration",
		},
		{
			name:   "a publication names the author's step, the caller's own per-run request and its own setting",
			remedy: Remedy{Operation: "publication", Setting: "pin_sources_at_push", Invocation: "re-run it with `--pin-sources`"},
			want: "remediation: run `aggrega pin catalog/recipes/server/recipe.yaml` and commit the entity.lock it writes " +
				"beside the entity; to have this publication record the pins itself instead, re-run it with " +
				"`--pin-sources`; to have every publication in this workspace record them, set " +
				"`pin_sources_at_push: true` in the workspace configuration",
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			err := UnpinnedEntity("/ws/catalog/recipes/server/recipe.yaml", "catalog/recipes/server/recipe.yaml",
				"Recipe", "server", missing, row.remedy)
			if !errors.Is(err, ErrEntityNotPinned()) {
				t.Fatalf("every operation raises the one unpinned-entity condition; got %v", err)
			}
			if code, ok := cerrors.ShortCodeOf(err); !ok || code != "E0302" {
				t.Errorf("the halt's short code = %q (found %v); want E0302", code, ok)
			}
			msg := err.Error()
			if !strings.Contains(msg, values) {
				t.Errorf("the composed values differ by operation, and they must not:\n%s", msg)
			}
			if !strings.HasSuffix(msg, row.want) {
				t.Errorf("the remediation is not this operation's:\n got: %s\nwant suffix: %s", msg, row.want)
			}
		})
	}
}

// TestCountPinsReportsEveryClassEvenWhenEmpty holds the per-class count to the
// class table, so a reader of a pin outcome sees every class the lock can hold.
func TestCountPinsReportsEveryClassEvenWhenEmpty(t *testing.T) {
	got := CountPins(Pins{ArtifactDigests: []types.ArtifactDigest{{Name: "a"}, {Name: "b"}}})
	want := []ClassCount{{Class: "remote source", Pins: 0}, {Class: "imported file", Pins: 2}, {Class: "local source file", Pins: 0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CountPins = %+v; want %+v", got, want)
	}
}
