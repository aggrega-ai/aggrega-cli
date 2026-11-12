// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

// The pure conversions the two build methods share: the advisory-class mapping,
// the advisory walk and the artifact-name walk. They are covered here rather
// than under either method because neither owns them — a test left beside one
// method would go on passing while the other stopped using the conversion at
// all.

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
)

// TestNoticeClassNamesEveryAdvisorySurface walks every class the shared
// computation raises and pins the wire name each one carries, plus the answer for
// a class this daemon does not name.
//
// The unknown row is the one worth having: a nearest match there would tell a
// client the advisory came from a surface it did not come from, which is worse
// than telling it nothing.
func TestNoticeClassNamesEveryAdvisorySurface(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		class compilerops.WarningClass
		want  string
	}{
		{"deprecation", compilerops.WarningDeprecation, noticeClassDeprecation},
		{"catalog", compilerops.WarningCatalog, noticeClassCatalog},
		{"catalog lint", compilerops.WarningCatalogLint, noticeClassCatalogLint},
		{"extends", compilerops.WarningExtends, noticeClassExtends},
		{"a class this daemon does not name", compilerops.WarningClass(-1), ""},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := noticeClass(tc.class); got != tc.want {
				t.Errorf("noticeClass(%v) = %q, want %q", tc.class, got, tc.want)
			}
		})
	}
}

// TestBuildNoticesCarryEveryWarningInSequence asserts the conversion keeps
// every advisory and keeps them in the order the computation raised them. That
// order is what the artifact posture's own gate sequence defines, so a reader
// meets a finding where the run found it rather than in an order this layer
// chose.
func TestBuildNoticesCarryEveryWarningInSequence(t *testing.T) {
	t.Parallel()
	preview := &compilerops.PreviewResult{Warnings: []compilerops.Warning{
		{Class: compilerops.WarningExtends, Text: "a broken parent"},
		{Class: compilerops.WarningDeprecation, Text: "a withdrawn pin"},
		{Class: compilerops.WarningCatalogLint, Text: "an overlapping pillar"},
	}}
	got := buildNotices(preview)
	want := []buildNotice{
		{Class: noticeClassExtends, Text: "a broken parent"},
		{Class: noticeClassDeprecation, Text: "a withdrawn pin"},
		{Class: noticeClassCatalogLint, Text: "an overlapping pillar"},
	}
	if len(got) != len(want) {
		t.Fatalf("the conversion carried %d of %d advisories: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("advisory %d is %+v, want %+v", i, got[i], want[i])
		}
	}
	if buildNotices(nil) != nil {
		t.Error("a run that raised nothing must carry no advisories")
	}
	if buildNotices(&compilerops.PreviewResult{}) != nil {
		t.Error("a run with an empty advisory set must carry no advisories")
	}
}

// TestRenderedArtifactNamesAreSorted pins the two answers the artifact-name walk
// owes: one sorted sequence for a result that rendered, and nothing for a run
// that halted before it could.
//
// The input is deliberately not in sorted order. Go randomises map iteration, so
// a walk that reported range order would name the same build differently on every
// call and a reader could not diff two previews of one workspace.
func TestRenderedArtifactNamesAreSorted(t *testing.T) {
	t.Parallel()
	if got := renderedArtifactNames(nil); got != nil {
		t.Errorf("a halted run has no artifacts to name; got %v", got)
	}
	result := &pipeline.Result{Artifacts: map[string][]byte{
		"boundaries/web/service.yaml": []byte("b"),
		"boundaries/api/deploy.yaml":  []byte("a"),
		"boundaries/web/deploy.yaml":  []byte("c"),
	}}
	want := []string{
		"boundaries/api/deploy.yaml",
		"boundaries/web/deploy.yaml",
		"boundaries/web/service.yaml",
	}
	got := renderedArtifactNames(result)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the artifact names are not sorted:\n got %v\nwant %v", got, want)
	}
}
