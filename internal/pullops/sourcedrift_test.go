// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// countingResolver answers every resolution with one commit and records how many
// times it was asked. The count is the point: the two things this file separates
// — a reference re-asked, and a reference not re-asked — are indistinguishable
// from the returned error alone, because both can be nil.
type countingResolver struct {
	commit string
	calls  int
}

func (r *countingResolver) Resolve(spec closure.SourceSpec) (*closure.ResolvedSource, error) {
	r.calls++
	return &closure.ResolvedSource{
		Kind:          spec.Kind,
		URL:           spec.URL,
		Chart:         spec.Chart,
		ModuleSubdir:  spec.ModuleSubdir,
		PinnedVersion: r.commit,
	}, nil
}

// gitRefLockfile is one recorded row: a git source pinned at pinned, resolved
// from the authored reference requestedRef. An empty requestedRef is the shape a
// kind that pins what its author wrote produces.
func gitRefLockfile(pinned, requestedRef string) *lockfile.Lockfile {
	return &lockfile.Lockfile{
		Version: 1,
		SourceArtifacts: []lockfile.SourceArtifact{{
			Kind:          "terraform-module-git",
			URL:           "git://example.invalid/repo",
			PinnedVersion: pinned,
			ReferencedBy: []lockfile.SourceArtifactReference{{
				Entity:       "recipe",
				URI:          "terraform/alpha-mod:1.0.0",
				Field:        "spec.source",
				RequestedRef: requestedRef,
			}},
		}},
	}
}

// TestVerifyRecordedSourceReferencesNarrowsWhenOffline holds the offline case to
// a NARROWING that says so, rather than to either of the two wrong answers.
//
// Refusing outright would take away an air-gapped verify of the pinned closure,
// which the ahead-of-time boundary is built to support. Passing in silence would
// reproduce, on this very axis, the silence the check exists to end. So the
// assertion is three-part: nothing was asked, nothing was reported, and the
// reader was told.
func TestVerifyRecordedSourceReferencesNarrowsWhenOffline(t *testing.T) {
	t.Parallel()

	res := &countingResolver{commit: "2222222222222222222222222222222222222222"}
	var diag bytes.Buffer

	err := verifyRecordedSourceReferences(
		gitRefLockfile("1111111111111111111111111111111111111111", "v1.0.0"),
		res,
		workspaceconfig.Config{Offline: true},
		t.TempDir(), ".aggrega/cache",
		&diag,
	)
	if err != nil {
		t.Fatalf("an offline verify must narrow rather than fail: %v", err)
	}
	if res.calls != 0 {
		t.Fatalf("an offline verify reached the remote %d time(s); the egress posture must be "+
			"read before anything is asked", res.calls)
	}
	if !strings.Contains(diag.String(), "offline") {
		t.Fatalf("an offline verify covered less than usual and said nothing about it; "+
			"the diagnostic stream carried: %q", diag.String())
	}
}

// TestVerifyRecordedSourceReferencesReAsksAndReports is the online counterpart,
// and it is what makes the offline assertion above mean something: the same
// lockfile and the same moved answer produce a report here.
func TestVerifyRecordedSourceReferencesReAsksAndReports(t *testing.T) {
	t.Parallel()

	res := &countingResolver{commit: "2222222222222222222222222222222222222222"}
	var diag bytes.Buffer

	err := verifyRecordedSourceReferences(
		gitRefLockfile("1111111111111111111111111111111111111111", "v1.0.0"),
		res,
		workspaceconfig.Config{},
		t.TempDir(), ".aggrega/cache",
		&diag,
	)
	if err == nil {
		t.Fatal("the recorded reference now resolves elsewhere and must be reported")
	}
	if res.calls != 1 {
		t.Fatalf("the recorded reference must be re-asked exactly once; it was asked %d time(s)", res.calls)
	}
}

// TestResolveRecordedSourceReferencesSkipsARowWithNoRecordedRef holds the re-ask
// to the rows that record one. A row without a recorded reference pins what its
// author wrote, so asking a remote about it would spend a fetch to compare a
// value against itself.
func TestResolveRecordedSourceReferencesSkipsARowWithNoRecordedRef(t *testing.T) {
	t.Parallel()

	res := &countingResolver{commit: "2222222222222222222222222222222222222222"}
	out, err := resolveRecordedSourceReferences(gitRefLockfile("1.0.0", ""), res, t.TempDir(), ".aggrega/cache")
	if err != nil {
		t.Fatalf("a row with no recorded reference must be skipped, not failed: %v", err)
	}
	if res.calls != 0 {
		t.Fatalf("a row with no recorded reference was asked %d time(s)", res.calls)
	}
	if len(out) != 0 {
		t.Fatalf("a skipped row must produce no fresh artifact; got %d", len(out))
	}
}

// TestResolveRecordedSourceReferencesAsksOneReferenceOnce covers the case two
// Recipes reach one reference of one repository: that is one question, and asking
// it twice would double the fetches a verify performs on a workspace where
// sharing a module is the ordinary shape.
func TestResolveRecordedSourceReferencesAsksOneReferenceOnce(t *testing.T) {
	t.Parallel()

	lf := gitRefLockfile("1111111111111111111111111111111111111111", "v1.0.0")
	lf.SourceArtifacts[0].ReferencedBy = append(lf.SourceArtifacts[0].ReferencedBy,
		lockfile.SourceArtifactReference{
			Entity: "recipe", URI: "terraform/other-mod:1.0.0",
			Field: "spec.source", RequestedRef: "v1.0.0",
		})

	res := &countingResolver{commit: "2222222222222222222222222222222222222222"}
	out, err := resolveRecordedSourceReferences(lf, res, t.TempDir(), ".aggrega/cache")
	if err != nil {
		t.Fatalf("re-resolve: %v", err)
	}
	if res.calls != 1 {
		t.Fatalf("one reference of one repository must be asked once; it was asked %d time(s)", res.calls)
	}
	if len(out) != 1 {
		t.Fatalf("one asked reference must produce one fresh artifact; got %d", len(out))
	}
}
