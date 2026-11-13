// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package docsbundle

// Which version of the documentation bundle a build made some commits after a
// pre-release reads: that pre-release where it is published, else the newest
// version published below it, pre-release or release — while every other bound
// keeps choosing releases only.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
)

// TestScaffoldExampleVersionABuildAfterAPreReleaseReadsThatPreRelease pins the
// online choice for a build described after a pre-release, beside the bounds
// that keep choosing releases only.
func TestScaffoldExampleVersionABuildAfterAPreReleaseReadsThatPreRelease(t *testing.T) {
	t.Parallel()
	candidates := []string{"0.9.0-rc.3", "0.9.0-rc.4", "0.9.0-rc.5", "0.9.0-rc.6"}
	cases := []struct {
		name      string
		published []string
		asked     string
		// want is the version read; empty means the read is refused as not found
		// with a message ending in refusedAt.
		want      string
		refusedAt string
		// notAcquired are published versions the read must leave uncached.
		notAcquired []string
	}{
		{
			name:        "a build after a candidate reads that candidate",
			published:   append([]string{"0.8.0"}, candidates...),
			asked:       "0.9.0-rc.6-4-gabc",
			want:        "0.9.0-rc.6",
			notAcquired: []string{"0.8.0", "0.9.0-rc.5"},
		},
		{
			name:      "a build after a candidate from a tree holding changes reads that candidate",
			published: append([]string{"0.8.0"}, candidates...),
			asked:     "0.9.0-rc.6-4-gabc-dirty",
			want:      "0.9.0-rc.6",
		},
		{
			name:        "a build after an unpublished candidate reads the newest one below it",
			published:   []string{"0.8.0", "0.9.0-rc.4", "0.9.0-rc.5", "0.9.0-rc.7", "0.9.0"},
			asked:       "0.9.0-rc.6-4-gabc",
			want:        "0.9.0-rc.5",
			notAcquired: []string{"0.9.0-rc.7", "0.9.0"},
		},
		{
			name:        "a build after a candidate with only an earlier release below reads that release",
			published:   []string{"0.8.0", "0.9.0"},
			asked:       "0.9.0-rc.6-4-gabc",
			want:        "0.8.0",
			notAcquired: []string{"0.9.0"},
		},
		{
			name:        "a build after a candidate with nothing below it is refused naming the candidate",
			published:   []string{"0.9.0-rc.4", "0.9.0-rc.5", "0.9.0-rc.6"},
			asked:       "0.9.0-rc.3-4-gabc",
			refusedAt:   "at or below 0.9.0-rc.3",
			notAcquired: []string{"0.9.0-rc.4"},
		},
		{
			name:        "a build after a release reads no candidate",
			published:   candidates,
			asked:       "0.9.0-4-gabc",
			refusedAt:   "at or below 0.9.0",
			notAcquired: []string{"0.9.0-rc.6"},
		},
		{
			name:      "a candidate asked for exactly reads itself",
			published: append([]string{"0.8.0"}, candidates...),
			asked:     "0.9.0-rc.6",
			want:      "0.9.0-rc.6",
		},
		{
			name:        "a candidate asked for exactly and unpublished reads a release only",
			published:   append([]string{"0.8.0"}, candidates...),
			asked:       "0.9.0-rc.7",
			want:        "0.8.0",
			notAcquired: []string{"0.9.0-rc.6"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			_, target, _, key := publishedAt(t, tc.published...)

			held, _, err := EnsureCachedAtOrBelow(context.Background(), requiring(key), root, testRef, tc.asked, target, nil)
			if tc.want == "" {
				if !errors.Is(err, docscache.ErrFetchNotFound()) {
					t.Fatalf("answered (%q, %v), want the cache miss", held, err)
				}
				if msg := err.Error(); !strings.HasSuffix(msg, tc.refusedAt) {
					t.Errorf("the refusal reads\n%s\nwant it to end with\n%s", msg, tc.refusedAt)
				}
			} else if err != nil || held != tc.want {
				t.Fatalf("answered (%q, %v), want %s", held, err, tc.want)
			}
			for _, v := range tc.notAcquired {
				if cached, cerr := docscache.Cached(root, testRef, v); cerr != nil || cached {
					t.Errorf("%s was acquired (%v, %v)", v, cached, cerr)
				}
			}
		})
	}
}

// TestScaffoldExampleVersionOfflineABuildAfterAPreReleaseReadsThatPreRelease pins
// the offline half of the same choice: from the copies the cache holds, the
// pre-release the build was made after, else the newest one cached below it.
func TestScaffoldExampleVersionOfflineABuildAfterAPreReleaseReadsThatPreRelease(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		cached []string
		want   string
	}{
		{
			name:   "the candidate is cached",
			cached: []string{"0.8.0", "0.9.0-rc.5", "0.9.0-rc.6", "0.9.0"},
			want:   "0.9.0-rc.6",
		},
		{
			name:   "the candidate is not cached",
			cached: []string{"0.8.0", "0.9.0-rc.5", "0.9.0"},
			want:   "0.9.0-rc.5",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			_, target, _, key := publishedAt(t, tc.cached...)
			online := requiring(key)
			for _, v := range tc.cached {
				if _, err := ensureCached(context.Background(), online, root, testRef, v, target, nil); err != nil {
					t.Fatalf("seed %s: %v", v, err)
				}
			}
			offline := requiring(key)
			offline.Offline = true

			held, acquired, err := EnsureCachedAtOrBelow(context.Background(), offline, root, testRef,
				"0.9.0-rc.6-4-gabc", nil, nil)
			if err != nil || held != tc.want || acquired {
				t.Fatalf("offline answered (%q, %v, %v), want %s from the cache", held, acquired, err, tc.want)
			}
		})
	}
}
