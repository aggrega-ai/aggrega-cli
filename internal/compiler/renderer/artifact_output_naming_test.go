// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestArtifactOutputNames_TwoImportsSharingOneBaseName is the load-bearing proof
// for the output-naming contract: two artifacts entries importing two DIFFERENT
// files that happen to share a base name are two distinct outputs, named by the
// keys they were authored under.
//
// The input is the one that produces the fault: `extras/shared.yaml` and
// `overrides/shared.yaml` are different files, and nothing an author can write
// distinguishes them once only their base name survives. A corpus that never
// authors two same-base-name imports cannot demonstrate this either way, and the
// shipped catalog never reaches the form, so the input is built here.
func TestArtifactOutputNames_TwoImportsSharingOneBaseName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	extras := writeArtifactFile(t, dir, "extras/shared.yaml", `apiVersion: v1
kind: ConfigMap
metadata:
  name: extras-shared
`)
	overrides := writeArtifactFile(t, dir, "overrides/shared.yaml", `apiVersion: v1
kind: ConfigMap
metadata:
  name: overrides-shared
`)

	tmplCtx, state, nodeCtx := newArtifactsCtx()
	resetArtifacts(state)
	state.Recipe.Spec.Artifacts.Set("extras-shared.yaml", types.ArtifactEntry{Import: extras})
	state.Recipe.Spec.Artifacts.Set("overrides-shared.yaml", types.ArtifactEntry{Import: overrides})

	proj, err := buildArtifactsProjection(nil, tmplCtx, state, nodeCtx, "{{", "}}")
	if err != nil {
		t.Fatalf("two imports sharing one base name did not project: %v", err)
	}
	if len(proj.Entries) != 2 {
		t.Fatalf("projected %d entr(ies); want 2 — one per authored key", len(proj.Entries))
	}

	// Author order is the projection order, so each entry is checked against the
	// file its own key imported: a name pair that matched while the bodies were
	// swapped would be the same information loss wearing two names.
	wantByName := map[string]string{
		"extras-shared.yaml":    "name: extras-shared",
		"overrides-shared.yaml": "name: overrides-shared",
	}
	for _, ent := range proj.Entries {
		want, ok := wantByName[ent.Name]
		if !ok {
			t.Errorf("entry named %q; want one of the two authored keys", ent.Name)
			continue
		}
		if !strings.Contains(string(ent.Content), want) {
			t.Errorf("entry %q carries the wrong file's body; want %q in:\n%s", ent.Name, want, ent.Content)
		}
		delete(wantByName, ent.Name)
	}
	if len(wantByName) != 0 {
		t.Errorf("%d authored key(s) never emitted: %v", len(wantByName), wantByName)
	}
}

// TestArtifactOutputNames_BundleNestsUnderTheConsumptionKey proves the bundle
// form's output names: one entry expands into one output per bundle file, each
// nested under the key the bundle was consumed at.
//
// Nesting rather than joining is the point. A separator-joined name is not
// recoverable when either part contains the separator, whereas the key is a
// single mapping key in one document and the filename is a single key in one
// bundle, so the nested pair identifies the file it came from.
func TestArtifactOutputNames_BundleNestsUnderTheConsumptionKey(t *testing.T) {
	t.Parallel()

	tmplCtx, state, nodeCtx := newArtifactsCtx()
	resetArtifacts(state)
	state.Recipe.Spec.Artifacts.Set("widgets", types.ArtifactEntry{Import: "SharedCRDs#/artifacts/WidgetCRD"})
	state.ArtifactsLibraries = map[string]types.ArtifactsLibrary{
		"SharedCRDs": artifactsLibraryFixtureOrdered("SharedCRDs", "WidgetCRD",
			[]string{"widget-crd.yaml", "widget-rbac.yaml"},
			[]string{albCRDBody, albRBACBody}),
	}

	proj, err := buildArtifactsProjection(nil, tmplCtx, state, nodeCtx, "{{", "}}")
	if err != nil {
		t.Fatalf("bundle projection: %v", err)
	}
	want := []string{"widgets/widget-crd.yaml", "widgets/widget-rbac.yaml"}
	if len(proj.Entries) != len(want) {
		t.Fatalf("projected %d entr(ies); want %d", len(proj.Entries), len(want))
	}
	for i, w := range want {
		if got := proj.Entries[i].Name; got != w {
			t.Errorf("entry[%d] name = %q; want %q", i, got, w)
		}
	}

	// One bundle consumed at two keys is two output sets, which is the property the
	// flat form could not express: the same bundle at a second key collided with
	// itself when the key contributed nothing to the name.
	state.Recipe.Spec.Artifacts.Set("widgets-again", types.ArtifactEntry{Import: "SharedCRDs#/artifacts/WidgetCRD"})
	proj2, err := buildArtifactsProjection(nil, tmplCtx, state, nodeCtx, "{{", "}}")
	if err != nil {
		t.Fatalf("the same bundle consumed at two keys halted: %v", err)
	}
	if len(proj2.Entries) != 4 {
		t.Fatalf("projected %d entr(ies) for two consumptions; want 4", len(proj2.Entries))
	}
	if proj2.Entries[2].Name != "widgets-again/widget-crd.yaml" {
		t.Errorf("second consumption entry[0] = %q; want widgets-again/widget-crd.yaml", proj2.Entries[2].Name)
	}
}

// TestArtifactOutputCollisionStillHaltsOnWhatSurvivesTheDerivation proves the
// halt is retained for what the naming rule above does not make impossible.
//
// A key that is not a single path segment can spell the nested name another key's
// bundle produces, and the authoring surface does not yet bound the key to one
// segment. That collision is the author's — two keys naming one output — rather
// than one the derivation manufactured, so it is refused rather than resolved.
func TestArtifactOutputCollisionStillHaltsOnWhatSurvivesTheDerivation(t *testing.T) {
	t.Parallel()

	tmplCtx, state, nodeCtx := newArtifactsCtx()
	resetArtifacts(state)
	state.Recipe.Spec.Artifacts.Set("widgets", types.ArtifactEntry{Import: "SharedCRDs#/artifacts/WidgetCRD"})
	state.Recipe.Spec.Artifacts.Set("widgets/widget-crd.yaml", types.ArtifactEntry{Inline: albCRDBody})
	state.ArtifactsLibraries = map[string]types.ArtifactsLibrary{
		"SharedCRDs": artifactsLibraryFixtureOrdered("SharedCRDs", "WidgetCRD",
			[]string{"widget-crd.yaml"}, []string{albCRDBody}),
	}

	_, err := buildArtifactsProjection(nil, tmplCtx, state, nodeCtx, "{{", "}}")
	if !errors.Is(err, ErrArtifactOutputCollision()) {
		t.Fatalf("expected ErrArtifactOutputCollision; got %v", err)
	}

	// Calibration: the same two entries with the inline key made a single segment
	// do NOT collide, so the halt above is a statement about the colliding name
	// rather than about the fixture shape.
	tmplCtx2, ok, nodeCtx2 := newArtifactsCtx()
	resetArtifacts(ok)
	ok.Recipe.Spec.Artifacts.Set("widgets", types.ArtifactEntry{Import: "SharedCRDs#/artifacts/WidgetCRD"})
	ok.Recipe.Spec.Artifacts.Set("widget-crd.yaml", types.ArtifactEntry{Inline: albCRDBody})
	ok.ArtifactsLibraries = state.ArtifactsLibraries
	if _, err := buildArtifactsProjection(nil, tmplCtx2, ok, nodeCtx2, "{{", "}}"); err != nil {
		t.Fatalf("single-segment keys must not collide with a nested bundle name: %v", err)
	}
}

// TestArtifactOriginIndexClassifiesByTheWholeEmittedName proves the host reads a
// placed file's input-kind origin against the whole output name the projection
// carries, at a path boundary — not against the name's last segment.
//
// The distinction is observable: a guest chooses its own output path, so the two
// readings agree on a single-segment name placed at the chart root and disagree
// wherever a name has more than one segment or a guest nests its output. The
// classification feeds the cross-origin collision report, so a file attributed to
// the wrong authoring form is attributed to the wrong author.
func TestArtifactOriginIndexClassifiesByTheWholeEmittedName(t *testing.T) {
	t.Parallel()

	proj := ArtifactsProjection{Entries: []ArtifactProjectionEntry{
		{Name: "app-config.properties", Origin: "inline"},
		{Name: "policy.yaml", Origin: "import"},
		{Name: "widgets/widget-crd.yaml", Origin: "import"},
		{Name: "nested/shared.yaml", Origin: "inline"},
		{Name: "shared.yaml", Origin: "import"},
	}}
	ix := NewArtifactOriginIndex(proj)

	cases := []struct {
		name string
		path string
		want EmitOrigin
		why  string
	}{
		{
			name: "inline entry at the path it was placed under",
			path: "app-config.properties",
			want: EmitOriginArtifactsInline,
		},
		{
			name: "imported entry under a directory the guest chose",
			path: "templates/policy.yaml",
			want: EmitOriginArtifactsImport,
		},
		{
			name: "bundle file under its consumption key",
			path: "templates/widgets/widget-crd.yaml",
			want: EmitOriginArtifactsImport,
			why:  "the whole name is widgets/widget-crd.yaml; its last segment names no entry",
		},
		{
			name: "two entries sharing a last segment resolve to the one that matches whole",
			path: "nested/shared.yaml",
			want: EmitOriginArtifactsInline,
			why:  "reading the last segment alone attributes this file to the imported entry",
		},
		{
			name: "the entry whose whole name the guest placed flat",
			path: "shared.yaml",
			want: EmitOriginArtifactsImport,
		},
		{
			name: "a companion the guest emitted for itself",
			path: "provenance.tf",
			want: EmitOriginArtifactsInline,
			why:  "no projected entry claims it, and the inline tag is what the host reports for that",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ix.For(tc.path); got != tc.want {
				msg := ""
				if tc.why != "" {
					msg = " — " + tc.why
				}
				t.Errorf("For(%q) = %v; want %v%s", tc.path, got, tc.want, msg)
			}
		})
	}
}

// TestArtifactOriginIndexNeedle proves the assertion above can fail: an index
// built over no entries classifies every path as inline, so a table that passed
// against it would be proving nothing about the matching.
func TestArtifactOriginIndexNeedle(t *testing.T) {
	t.Parallel()

	empty := NewArtifactOriginIndex(ArtifactsProjection{})
	if got := empty.For("templates/policy.yaml"); got != EmitOriginArtifactsInline {
		t.Fatalf("an empty index reported %v; want the inline tag", got)
	}
	populated := NewArtifactOriginIndex(ArtifactsProjection{Entries: []ArtifactProjectionEntry{
		{Name: "policy.yaml", Origin: "import"},
	}})
	if got := populated.For("templates/policy.yaml"); got == empty.For("templates/policy.yaml") {
		t.Fatalf("the populated and empty indexes agreed on %v, so the table above cannot discriminate", got)
	}
}
