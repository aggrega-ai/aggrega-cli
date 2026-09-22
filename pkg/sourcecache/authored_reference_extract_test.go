// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package sourcecache_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
)

// What this file proves, and why the two halves have to be proved together.
//
// A source declared at a git reference is found in a record that pins the commit
// the reference resolved to. That takes two things agreeing, and each is
// invisible while the other is wrong: the row has to be REACHED from the authored
// reference, and the cache slot has to be ADDRESSED by the version the row pins.
// Reaching the row and then addressing the slot by the authored value looks like
// a working lookup and fails at the blob; addressing the slot correctly without
// reaching the row never gets that far. Driving the extract end to end is what
// pins both at once.
//
// The two halves stayed hidden for as long as every source a build could reach
// pinned the version its author wrote, because the two values were then the same
// string and neither choice could be distinguished from the other.

// gitSlotFixture seeds a cache slot the way acquisition writes one for a git
// source — addressed by the resolved commit — and returns the lockfile that pins
// it, recording the reference that resolved there.
func gitSlotFixture(t *testing.T, root, commit, ref string) *lockfile.Lockfile {
	t.Helper()
	const (
		kind = "git-repository"
		url  = "git://example.invalid/repo"
	)
	blob := buildTGZ(t, map[string]string{
		"Chart.yaml":               "apiVersion: v2\nname: websvc\nversion: 0.1.0\n",
		"templates/configmap.yaml": "kind: ConfigMap\n",
	})
	dg := digestOf(blob)
	seedSlot(t, root, ".aggrega/cache", kind, url, commit, blob, dg)
	return &lockfile.Lockfile{
		Version:     1,
		Release:     "./r.yaml",
		GeneratedAt: "2026-05-22T10:00:00Z",
		SourceArtifacts: []lockfile.SourceArtifact{{
			Kind:          kind,
			URL:           url,
			PinnedVersion: commit,
			Digest:        dg,
			Mediatype:     "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
			ReferencedBy: []lockfile.SourceArtifactReference{
				{Entity: "recipe", URI: "u", Field: "spec.source", RequestedRef: ref},
			},
		}},
	}
}

// TestLookupAndExtractFindsAGitSourceByItsAuthoredReference is the shape the
// authored document actually has: a `ref:` and no `version:` at all. Nothing in
// the recipe names the commit, so the reference is the only term that can reach
// the row, and the slot is addressed by a value the caller never passed in.
func TestLookupAndExtractFindsAGitSourceByItsAuthoredReference(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const commit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	lf := gitSlotFixture(t, root, commit, "v1.2.3")

	extracted, err := sourcecache.LookupAndExtract(lf,
		"git-repository", "git://example.invalid/repo", "", "",
		"", "v1.2.3", root, ".aggrega/cache")
	if err != nil {
		t.Fatalf("a source declared at a reference must resolve through the record that pins its commit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(extracted, "Chart.yaml")); err != nil {
		t.Errorf("the archive was not extracted: %v", err)
	}
}

// TestLookupAndExtractAddressesTheSlotByThePinnedVersion is the half a lookup
// test cannot see. The caller passes a reference and no version; the slot exists
// only under the commit. Asserting on the extracted CONTENT is what makes this
// about the slot rather than about the row: a wrong slot path fails to read a
// blob that is plainly there.
func TestLookupAndExtractAddressesTheSlotByThePinnedVersion(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const commit = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	lf := gitSlotFixture(t, root, commit, "main")

	extracted, err := sourcecache.LookupAndExtract(lf,
		"git-repository", "git://example.invalid/repo", "", "",
		"", "main", root, ".aggrega/cache")
	if err != nil {
		t.Fatalf("LookupAndExtract: %v", err)
	}
	// The slot the fixture seeded is the one addressed by the commit. Deriving it
	// here from the same public helper the seed used keeps the assertion about
	// WHICH slot was read rather than about how a path is spelled.
	wantSlot := sourcecache.SlotPath(root, ".aggrega/cache",
		"git-repository", "git://example.invalid/repo", "", "", commit, lf.SourceArtifacts[0].Digest)
	if got := filepath.Dir(extracted); got != wantSlot {
		t.Errorf("extracted from %q, want the slot addressed by the pinned commit %q", got, wantSlot)
	}
}

// TestLookupAndExtractWithoutAReferenceStillNeedsTheVersion is the negative
// control for the pair above. The same record, asked without the reference and
// without the commit, must report absence — otherwise "found by reference" would
// be satisfiable by a lookup that matched on the location terms alone and
// returned whichever row it saw first.
func TestLookupAndExtractWithoutAReferenceStillNeedsTheVersion(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lf := gitSlotFixture(t, root, "cccccccccccccccccccccccccccccccccccccccc", "v1.2.3")

	_, err := sourcecache.LookupAndExtract(lf,
		"git-repository", "git://example.invalid/repo", "", "",
		"", "", root, ".aggrega/cache")
	if !errors.Is(err, sourcecache.ErrFetchNotFound()) {
		t.Fatalf("a lookup naming neither the reference nor the commit must report absence; got %v", err)
	}
}

// TestLookupAndExtractNamesTheReferenceItLookedFor keeps the refusal actionable.
// A reader who authored a `ref:` and no `version:` is told which reference was
// looked for; a line reporting an empty version and nothing else would name none
// of the terms the lookup actually used.
func TestLookupAndExtractNamesTheReferenceItLookedFor(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lf := gitSlotFixture(t, root, "dddddddddddddddddddddddddddddddddddddddd", "v1.2.3")

	_, err := sourcecache.LookupAndExtract(lf,
		"git-repository", "git://example.invalid/repo", "", "",
		"", "v9.9.9", root, ".aggrega/cache")
	if !errors.Is(err, sourcecache.ErrFetchNotFound()) {
		t.Fatalf("want not-found, got %v", err)
	}
	if got := err.Error(); !strings.Contains(got, "ref=v9.9.9") {
		t.Errorf("the refusal does not name the reference it looked for, so a reader cannot act on it: %s", got)
	}
}
