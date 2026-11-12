// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package lockfile

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

const (
	commitOne = "1111111111111111111111111111111111111111"
	commitTwo = "2222222222222222222222222222222222222222"
)

// priorRow builds one recorded source-artifact row: an identity, the version it
// pins, and the reference (if any) that version was resolved FROM.
func priorRow(pinned, requestedRef string) SourceArtifact {
	return SourceArtifact{
		Kind:          "git-repository",
		URL:           "git://example.invalid/repo",
		ModuleSubdir:  "modules/alpha",
		PinnedVersion: pinned,
		ReferencedBy: []SourceArtifactReference{{
			Entity:       "recipe",
			URI:          "terraform/alpha-mod:1.0.0",
			Field:        "spec.source",
			RequestedRef: requestedRef,
		}},
	}
}

// freshRow is priorRow's counterpart on the resolution side — the same identity
// terms, carried on the closure-side type the descent produces.
func freshRow(pinned, requestedRef string) closure.SourceArtifact {
	return closure.SourceArtifact{
		Kind:          "git-repository",
		URL:           "git://example.invalid/repo",
		ModuleSubdir:  "modules/alpha",
		PinnedVersion: pinned,
		ReferencedBy: []closure.SourceArtifactReference{{
			Entity:       "recipe",
			URI:          "terraform/alpha-mod:1.0.0",
			Field:        "spec.source",
			RequestedRef: requestedRef,
		}},
	}
}

// TestVerifySourceReferences walks the grid the comparison has to separate. The
// rows are chosen so that the two halves of the join key are each load-bearing in
// at least one of them: drop the pinned version from the key and the moved row
// stops being found; drop the reference and the author-edit rows start reporting
// drift they must not.
func TestVerifySourceReferences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		prior     *Lockfile
		resolved  []closure.SourceArtifact
		wantDrift bool
		wantIn    []string
	}{
		{
			name:      "the same reference now resolves to a different commit",
			prior:     &Lockfile{SourceArtifacts: []SourceArtifact{priorRow(commitOne, "v1.0.0")}},
			resolved:  []closure.SourceArtifact{freshRow(commitTwo, "v1.0.0")},
			wantDrift: true,
			// The diagnostic owes the reader all three facts and the way out.
			wantIn: []string{
				"git://example.invalid/repo", "modules/alpha", `"v1.0.0"`,
				commitOne, commitTwo, "--upgrade",
			},
		},
		{
			name:     "the same reference still resolves to the pinned commit",
			prior:    &Lockfile{SourceArtifacts: []SourceArtifact{priorRow(commitOne, "v1.0.0")}},
			resolved: []closure.SourceArtifact{freshRow(commitOne, "v1.0.0")},
		},
		{
			// The author edited spec.source.ref. That is a change to what was asked
			// for, not a change in the answer, and refusing it would refuse an
			// ordinary edit. This row is the reason the reference is in the key.
			name:     "the author asked for a different reference",
			prior:    &Lockfile{SourceArtifacts: []SourceArtifact{priorRow(commitOne, "v1.0.0")}},
			resolved: []closure.SourceArtifact{freshRow(commitTwo, "v2.0.0")},
		},
		{
			// helm-* and compose-* pin what the author wrote, so nothing was
			// transformed and no reference is recorded. A version change there is an
			// edit to the Recipe.
			name:     "neither side records a reference",
			prior:    &Lockfile{SourceArtifacts: []SourceArtifact{priorRow("1.0.0", "")}},
			resolved: []closure.SourceArtifact{freshRow("2.0.0", "")},
		},
		{
			name:     "the reference is newly added, so nothing pins it yet",
			prior:    &Lockfile{SourceArtifacts: nil},
			resolved: []closure.SourceArtifact{freshRow(commitOne, "v1.0.0")},
		},
		{
			// What a full refresh hands over. It reads no prior lockfile at all, so
			// `aggrega pull --upgrade` adopts a moved reference without this
			// comparison testing any flag.
			name:     "there is no prior lockfile",
			prior:    nil,
			resolved: []closure.SourceArtifact{freshRow(commitTwo, "v1.0.0")},
		},
		{
			// Two directories of one repository are two artifacts. The one whose
			// reference did not move must not be swept in by the one whose did.
			name: "one of two module directories moved",
			prior: &Lockfile{SourceArtifacts: []SourceArtifact{
				priorRow(commitOne, "v1.0.0"),
				{
					Kind: "git-repository", URL: "git://example.invalid/repo",
					ModuleSubdir: "modules/beta", PinnedVersion: commitOne,
					ReferencedBy: []SourceArtifactReference{{
						Entity: "recipe", URI: "terraform/beta-mod:1.0.0",
						Field: "spec.source", RequestedRef: "v1.0.0",
					}},
				},
			}},
			resolved: []closure.SourceArtifact{
				freshRow(commitTwo, "v1.0.0"),
				{
					Kind: "git-repository", URL: "git://example.invalid/repo",
					ModuleSubdir: "modules/beta", PinnedVersion: commitOne,
					ReferencedBy: []closure.SourceArtifactReference{{
						Entity: "recipe", URI: "terraform/beta-mod:1.0.0",
						Field: "spec.source", RequestedRef: "v1.0.0",
					}},
				},
			},
			wantDrift: true,
			wantIn:    []string{"modules/alpha"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := VerifySourceReferences(tc.prior, tc.resolved)
			if !tc.wantDrift {
				if err != nil {
					t.Fatalf("this case is not drift and must report none; got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("a moved reference must be reported; nothing was")
			}
			if !errors.Is(err, ErrLockfileDrift()) {
				t.Fatalf("the report must carry the lockfile-drift condition so a caller can match it; got: %v", err)
			}
			for _, want := range tc.wantIn {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the diagnostic does not name %q, so a reader cannot act on it:\n%v", want, err)
				}
			}
		})
	}
}

// TestVerifySourceReferencesDoesNotReportOneReferenceTwice holds the report to
// one entry per moved reference. Two Recipes reaching one reference of one
// repository dedupe into a single artifact with two back-pointers, and a reader
// told the same thing twice cannot tell that from two distinct references having
// moved.
func TestVerifySourceReferencesDoesNotReportOneReferenceTwice(t *testing.T) {
	t.Parallel()

	twoReferrers := func(pinned string) []SourceArtifactReference {
		return []SourceArtifactReference{
			{Entity: "recipe", URI: "terraform/alpha-mod:1.0.0", Field: "spec.source", RequestedRef: "v1.0.0"},
			{Entity: "recipe", URI: "terraform/other-mod:1.0.0", Field: "spec.source", RequestedRef: "v1.0.0"},
		}
	}
	prior := &Lockfile{SourceArtifacts: []SourceArtifact{{
		Kind: "git-repository", URL: "git://example.invalid/repo",
		ModuleSubdir: "modules/alpha", PinnedVersion: commitOne,
		ReferencedBy: twoReferrers(commitOne),
	}}}
	resolved := []closure.SourceArtifact{{
		Kind: "git-repository", URL: "git://example.invalid/repo",
		ModuleSubdir: "modules/alpha", PinnedVersion: commitTwo,
		ReferencedBy: []closure.SourceArtifactReference{
			{Entity: "recipe", URI: "terraform/alpha-mod:1.0.0", Field: "spec.source", RequestedRef: "v1.0.0"},
			{Entity: "recipe", URI: "terraform/other-mod:1.0.0", Field: "spec.source", RequestedRef: "v1.0.0"},
		},
	}}

	err := VerifySourceReferences(prior, resolved)
	if err == nil {
		t.Fatal("the moved reference must be reported")
	}
	if got := strings.Count(err.Error(), "reference \"v1.0.0\" moved"); got != 1 {
		t.Fatalf("one moved reference must be reported once; it was reported %d times:\n%v", got, err)
	}
}
