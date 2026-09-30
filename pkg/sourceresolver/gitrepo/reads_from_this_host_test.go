// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package gitrepo

import (
	"context"
	"testing"

	"github.com/go-git/go-git/v5"
)

// TestReadsFromThisHostFollowsTheClonesOwnReadingOfTheURL pins which repository
// URLs the adapter reads from this host's filesystem rather than over the
// network, one row per spelling a source may be written in.
//
// The two local spellings are also cloned for real from a repository on disk, so
// the rows answering true are held to what the clone does with them rather than
// to what this test expects of it.
func TestReadsFromThisHostFollowsTheClonesOwnReadingOfTheURL(t *testing.T) {
	t.Parallel()
	fixtureDir, _ := buildFixtureRepo(t, map[string]string{"Chart.yaml": "name: fixture\n"})

	for _, tc := range []struct {
		name   string
		url    string
		onHost bool
		// clone says the row's URL is also cloned for real from the fixture
		// repository, which only a URL naming a place on this host can be here.
		clone bool
	}{
		{"a file URL", "file://" + fixtureDir, true, true},
		{"a path written with no scheme", fixtureDir, true, true},
		{"a relative path", "repos/chart.git", true, false},
		{"a secure web URL", "https://git.example.test/team/chart.git", false, false},
		{"a plain web URL", "http://git.example.test/team/chart.git", false, false},
		{"a secure shell URL", "ssh://git@git.example.test/team/chart.git", false, false},
		{"a secure shell host and path", "git@git.example.test:team/chart.git", false, false},
		{"the repository protocol's own URL", "git://git.example.test/team/chart.git", false, false},
		{"a registry reference", "oci://hub.example.test/charts/web", false, false},
		{"a URL the clone cannot read at all", "https://[::1", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ReadsFromThisHost(tc.url); got != tc.onHost {
				t.Errorf("ReadsFromThisHost(%q) = %v, want %v", tc.url, got, tc.onHost)
			}
			if !tc.clone {
				return
			}
			if _, err := git.PlainCloneContext(context.Background(), t.TempDir(), false,
				&git.CloneOptions{URL: tc.url, NoCheckout: true}); err != nil {
				t.Errorf("the clone did not read %q from this host: %v", tc.url, err)
			}
		})
	}
}
