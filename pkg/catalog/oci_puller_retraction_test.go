// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// fixedScreen is a RetractionScreen over a fixed withdrawn-version set. It stands in
// for the production screen, whose input is a signature-verified local index — the
// index reading and its verification are proven where they live; what is under test
// here is the SELECTION consequence of a screen's answer, and specifically the two
// failure modes, which must not be collapsed.
type fixedScreen struct {
	withdrawn map[string]string
	selected  string
	excluded  []VersionExclusion
	pinned    []string
}

func (s *fixedScreen) ScreenVersions(_ string, tags []string) ([]string, []VersionExclusion) {
	eligible := make([]string, 0, len(tags))
	var excluded []VersionExclusion
	for _, tag := range tags {
		if reason, hit := s.withdrawn[tag]; hit {
			excluded = append(excluded, VersionExclusion{Version: tag, Reason: reason})
			continue
		}
		eligible = append(eligible, tag)
	}
	return eligible, excluded
}

func (s *fixedScreen) ReportSelection(_, selected string, excluded []VersionExclusion) {
	s.selected, s.excluded = selected, excluded
}

func (s *fixedScreen) ReportPinned(_, version string) {
	s.pinned = append(s.pinned, version)
}

// TestSelectScreenedPullVersionScreening pins the discovery step's behaviour under a
// screen, and above all keeps its two failure modes apart. Collapsing them would
// report a fully-withdrawn repository as a missing entity — the path of least
// resistance, and the one thing this must not do.
func TestSelectScreenedPullVersionScreening(t *testing.T) {
	const ref = "reg.test/component/core/svc"

	t.Run("unscreened_puller_selects_the_highest", func(t *testing.T) {
		// The behaviour that existed before any screen, preserved by construction: a
		// nil screen is the unscreened default, not a degraded mode.
		p := DefaultOCIPuller{}
		got, err := p.selectScreenedPullVersion(ref, []string{"1.0.0", "2.0.0"})
		if err != nil || got != "2.0.0" {
			t.Fatalf("unscreened selection = (%q, %v); want (2.0.0, nil)", got, err)
		}
	})

	t.Run("withdrawn_highest_falls_to_the_next_and_announces", func(t *testing.T) {
		screen := &fixedScreen{withdrawn: map[string]string{"2.0.0": "withdrawn"}}
		p := DefaultOCIPuller{retraction: screen}
		got, err := p.selectScreenedPullVersion(ref, []string{"1.0.0", "2.0.0"})
		if err != nil || got != "1.0.0" {
			t.Fatalf("screened selection = (%q, %v); want (1.0.0, nil)", got, err)
		}
		if screen.selected != "1.0.0" || len(screen.excluded) != 1 || screen.excluded[0].Version != "2.0.0" {
			t.Fatalf("the substitution must be announced; got selected=%q excluded=%+v", screen.selected, screen.excluded)
		}
	})

	t.Run("no_exclusion_stays_silent", func(t *testing.T) {
		screen := &fixedScreen{withdrawn: map[string]string{"9.9.9": "withdrawn"}}
		p := DefaultOCIPuller{retraction: screen}
		got, err := p.selectScreenedPullVersion(ref, []string{"1.0.0", "2.0.0"})
		if err != nil || got != "2.0.0" {
			t.Fatalf("selection = (%q, %v); want (2.0.0, nil)", got, err)
		}
		if screen.selected != "" {
			t.Errorf("an ordinary selection must announce nothing; got %q", screen.selected)
		}
	})

	t.Run("every_candidate_withdrawn_halts_loudly_not_as_not_found", func(t *testing.T) {
		screen := &fixedScreen{withdrawn: map[string]string{"1.0.0": "gone", "2.0.0": "also gone"}}
		p := DefaultOCIPuller{retraction: screen}
		_, err := p.selectScreenedPullVersion(ref, []string{"1.0.0", "2.0.0"})
		if !errors.Is(err, ErrAllVersionsRetracted) {
			t.Fatalf("error = %v; want ErrAllVersionsRetracted", err)
		}
		// Reusing the no-candidate path would have surfaced this as a missing entity.
		if errors.Is(err, oci.ErrFetchNotFound) {
			t.Fatalf("a fully-withdrawn repository must NOT read as not-found; got %v", err)
		}
		for _, want := range []string{"1.0.0", "2.0.0", "gone", "also gone"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("halt message must name %q; got %v", want, err)
			}
		}
	})

	t.Run("no_conformant_version_and_no_exclusion_stays_not_found", func(t *testing.T) {
		// The screen excluded nothing, so the failure has nothing to do with
		// withdrawal and must keep the reading it has always had.
		screen := &fixedScreen{withdrawn: map[string]string{"9.9.9": "gone"}}
		p := DefaultOCIPuller{retraction: screen}
		_, err := p.selectScreenedPullVersion(ref, []string{"v1", "1.2.3.4"})
		if !errors.Is(err, oci.ErrFetchNotFound) {
			t.Fatalf("error = %v; want the not-found reading when nothing was withdrawn", err)
		}
		if errors.Is(err, ErrAllVersionsRetracted) {
			t.Fatalf("a repository with no conformant version was not withdrawn; got %v", err)
		}
	})

	t.Run("exclusion_order_is_deterministic_in_the_halt_message", func(t *testing.T) {
		// The screen reports ascending published version; the message must reproduce
		// that order verbatim so it is byte-stable for the same registry state.
		excluded := []VersionExclusion{{Version: "1.0.0", Reason: "a"}, {Version: "2.0.0"}}
		got := formatVersionExclusions(excluded)
		if got != "1.0.0 (a), 2.0.0" {
			t.Fatalf("formatVersionExclusions = %q; want %q", got, "1.0.0 (a), 2.0.0")
		}
	})
}
