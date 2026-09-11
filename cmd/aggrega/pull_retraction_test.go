// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/publishops"
	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const retractionScreenHost = "hub.aggrega.ai"
const retractionScreenRepo = "component/core/api-server"
const retractionScreenReference = retractionScreenHost + "/" + retractionScreenRepo

// buildScreenIndex builds a real local index for a two-version repository through
// the production `registry update`, attaching a marker of the given severity to each
// named version. signMarker chooses whether the marker carries a verifying signature
// — the axis that decides whether the record may be acted on at all.
//
// The index is written and read back through the production writer and loader, so a
// screen built over it reads the same bytes a real workspace would. What it does not
// exercise is the selection step itself, which needs a registry to list tags; that is
// proven end-to-end against a real in-process registry in the integration test.
func buildScreenIndex(t *testing.T, markers map[string]deprecation.Severity, signMarker bool) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	byVersion := make(map[string]deprecation.Marker, len(markers))
	for version, severity := range markers {
		byVersion[version] = deprecation.Marker{
			Severity: severity,
			Reason:   "withdrawn in favour of a successor",
			IssuedOn: "2026-05-01T00:00:00Z",
		}
	}
	return buildMarkedScreenIndex(t, byVersion, signMarker)
}

// buildMarkedScreenIndex is buildScreenIndex over whole markers rather than bare
// severities, for a case that needs two versions marked differently.
//
// A marker payload is content-addressed, so two versions carrying byte-identical
// markers push the same blob twice — which an in-memory store rejects as already
// present. Supplying the markers lets such a case give each version its own reason,
// which is what a publisher retracting two versions would write anyway.
func buildMarkedScreenIndex(t *testing.T, markers map[string]deprecation.Marker, signMarker bool) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	ctx := context.Background()
	meta := oci.SearchableMetadata{Description: "d", Category: "compute", Kind: "Component"}
	target := seedComponentRepo(t, []string{"1.0.0", "2.0.0"}, meta)

	signer, content := keyedMarkerSigner(t)
	for version, marker := range markers {
		if signMarker {
			attachSignedMarkerToTag(t, target.Store, version, marker, signer)
			continue
		}
		// Unsigned: attached as a bare referrer, exactly as an unauthenticated party
		// could. It must not reach the index at all.
		payload, err := deprecation.Marshal(marker)
		if err != nil {
			t.Fatalf("marshal marker: %v", err)
		}
		if _, err := oci.AttachReferrer(ctx, target.Store, version, deprecation.ArtifactType, payload); err != nil {
			t.Fatalf("attach unsigned referrer: %v", err)
		}
	}

	withIndexTarget(t, fakeIndexSource{
		repos:   []string{retractionScreenRepo},
		targets: map[string]oras.ReadOnlyTarget{retractionScreenRepo: target},
	})

	rw := resolveWorkspaceWithConfig(t, t.TempDir(), content)
	var out bytes.Buffer
	if err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Out: &out, now: frozenClock()}); err != nil {
		t.Fatalf("registryUpdateRun: %v\n%s", err, out.String())
	}
	return rw
}

// TestRetractionScreenHonorsOnlyVerifiedWithdrawals is the "never act on an
// unverified record" proof. The two arms differ ONLY in whether the marker carries a
// verifying signature; the repository, the versions, the severity and the index build
// are identical. A screen that read the index without regard to verification would
// pass the signed arm and fail the unsigned one.
func TestRetractionScreenHonorsOnlyVerifiedWithdrawals(t *testing.T) {
	// Not parallel: mutates the process-global registryIndexTarget seam.
	t.Run("signed_retracted_marker_excludes_the_version", func(t *testing.T) {
		rw := buildScreenIndex(t, map[string]deprecation.Severity{"2.0.0": deprecation.SeverityRetracted}, true)
		var diag bytes.Buffer
		screen := registryindex.NewScreen(rw, retractionScreenHost, &diag)
		if screen == nil {
			t.Fatal("a verified withdrawal must produce a screen")
		}
		eligible, excluded := screen.ScreenVersions(retractionScreenReference, []string{"1.0.0", "2.0.0"})
		if len(excluded) != 1 || excluded[0].Version != "2.0.0" {
			t.Fatalf("excluded = %+v; want exactly 2.0.0", excluded)
		}
		if excluded[0].Reason != "withdrawn in favour of a successor" {
			t.Errorf("exclusion reason = %q; want the recorded reason", excluded[0].Reason)
		}
		if len(eligible) != 1 || eligible[0] != "1.0.0" {
			t.Fatalf("eligible = %v; want only 1.0.0", eligible)
		}
	})

	t.Run("unsigned_retracted_marker_screens_nothing", func(t *testing.T) {
		rw := buildScreenIndex(t, map[string]deprecation.Severity{"2.0.0": deprecation.SeverityRetracted}, false)
		var diag bytes.Buffer
		screen := registryindex.NewScreen(rw, retractionScreenHost, &diag)
		if screen == nil {
			// The honest outcome: nothing verified was recorded, so no screen forms and
			// the discovery step stays unscreened.
			return
		}
		eligible, excluded := screen.ScreenVersions(retractionScreenReference, []string{"1.0.0", "2.0.0"})
		t.Fatalf("an unsigned marker must screen nothing; eligible=%v excluded=%+v", eligible, excluded)
	})
}

// TestVerifiedArtifactSignerRecoversThePublishersIdentity covers the branch the
// index build needs and the closure discovery pass gets for free: recovering an
// artifact's OWN verified signer, which a keyless honored set folds in so a
// publisher can withdraw their own artifact.
//
// Production reaches it only on a keyless policy, but the function's own logic —
// resolve the subject, list its signature referrers, fetch and verify one against
// the manifest digest — is policy-independent, so a keyed policy exercises the whole
// body while keeping the crypto offline. The keyless verification primitive it
// delegates to is proven separately against a virtual trust root.
func TestVerifiedArtifactSignerRecoversThePublishersIdentity(t *testing.T) {
	ctx := context.Background()
	meta := oci.SearchableMetadata{Description: "d", Category: "compute", Kind: "Component"}
	target := seedComponentRepo(t, []string{"1.0.0"}, meta)
	signer, content := keyedMarkerSigner(t)
	rw := resolveWorkspaceWithConfig(t, t.TempDir(), content)
	wsCfg := rw.Config()
	policy, ok := wsCfg.RegistryTrustPolicy(retractionScreenHost)
	if !ok {
		t.Fatalf("test config must carry a policy for %s", retractionScreenHost)
	}

	t.Run("unsigned_artifact_yields_nil_rather_than_an_error", func(t *testing.T) {
		// nil narrows the honored signer set rather than widening it, so this can only
		// under-record a withdrawal — never invent one.
		if got := registryindex.VerifiedArtifactSigner(ctx, wsCfg, target.Store, "1.0.0", policy); got != nil {
			t.Fatalf("an unsigned artifact must yield no signer; got %+v", got)
		}
	})

	t.Run("signed_artifact_yields_its_verified_signer", func(t *testing.T) {
		digest, exists, err := oci.Resolve(ctx, target.Store, "1.0.0")
		if err != nil || !exists {
			t.Fatalf("resolve 1.0.0: %v (exists=%v)", err, exists)
		}
		bundle, artifactType, serr := publishops.SignDigest(ctx, signer, digest)
		if serr != nil {
			t.Fatalf("signDigest: %v", serr)
		}
		if aerr := publishops.AttachBundle(ctx, target.Store, "1.0.0", artifactType, bundle); aerr != nil {
			t.Fatalf("attachBundle: %v", aerr)
		}
		got := registryindex.VerifiedArtifactSigner(ctx, wsCfg, target.Store, "1.0.0", policy)
		if got == nil {
			t.Fatal("a signed artifact must yield its verified signer")
		}
		if got.Mode != "keyed" {
			t.Errorf("signer mode = %q; want keyed", got.Mode)
		}
	})
}

// TestRetractionScreenSoftSeverityDoesNotScreen proves the two-tier split at the
// screen, and does so without a vacuous pass. Both versions carry a verified marker
// and only their SEVERITY differs, so the screen is necessarily live and holds a
// record for both — a screen that keyed on the presence of a record rather than on
// the withdrawal severity would exclude both and fail here.
func TestRetractionScreenSoftSeverityDoesNotScreen(t *testing.T) {
	rw := buildScreenIndex(t, map[string]deprecation.Severity{
		"1.0.0": deprecation.SeverityRetracted,
		"2.0.0": deprecation.SeverityWarning,
	}, true)
	var diag bytes.Buffer
	screen := registryindex.NewScreen(rw, retractionScreenHost, &diag)
	if screen == nil {
		t.Fatal("a verified withdrawal on 1.0.0 must produce a screen")
	}
	eligible, excluded := screen.ScreenVersions(retractionScreenReference, []string{"1.0.0", "2.0.0"})
	if len(excluded) != 1 || excluded[0].Version != "1.0.0" {
		t.Fatalf("only the retracted version may be screened; excluded = %+v", excluded)
	}
	if len(eligible) != 1 || eligible[0] != "2.0.0" {
		t.Fatalf("the warning-severity version must stay eligible; eligible = %v", eligible)
	}
}

// TestRetractionScreenNoticesCarryTheObservationMoment proves the honest-limit
// requirement on the surfaces a user reads: every notice about a recorded withdrawal
// states when the record was observed and how to refresh it, so the absence of a
// marker is never read as evidence of non-withdrawal.
func TestRetractionScreenNoticesCarryTheObservationMoment(t *testing.T) {
	rw := buildScreenIndex(t, map[string]deprecation.Severity{"2.0.0": deprecation.SeverityRetracted}, true)

	t.Run("selection_notice", func(t *testing.T) {
		var diag bytes.Buffer
		screen := registryindex.NewScreen(rw, retractionScreenHost, &diag)
		_, excluded := screen.ScreenVersions(retractionScreenReference, []string{"1.0.0", "2.0.0"})
		screen.ReportSelection(retractionScreenReference, "1.0.0", excluded)
		notice := diag.String()
		for _, want := range []string{
			"2.0.0 is retracted and was not selected",
			"withdrawn in favour of a successor",
			"resolved to 1.0.0 instead",
			"as of 2026-01-02T03:04:05Z",
			"aggrega registry update",
		} {
			if !strings.Contains(notice, want) {
				t.Errorf("selection notice must contain %q; got:\n%s", want, notice)
			}
		}
	})

	t.Run("pinned_notice_is_loud", func(t *testing.T) {
		var diag bytes.Buffer
		screen := registryindex.NewScreen(rw, retractionScreenHost, &diag)
		screen.ReportPinned(retractionScreenReference, "2.0.0")
		notice := diag.String()
		for _, want := range []string{"2.0.0 is retracted", "names it explicitly", "as of 2026-01-02T03:04:05Z"} {
			if !strings.Contains(notice, want) {
				t.Errorf("pinned notice must contain %q; got:\n%s", want, notice)
			}
		}
	})

	t.Run("pinned_notice_is_silent_for_an_unwithdrawn_version", func(t *testing.T) {
		var diag bytes.Buffer
		screen := registryindex.NewScreen(rw, retractionScreenHost, &diag)
		screen.ReportPinned(retractionScreenReference, "1.0.0")
		if diag.String() != "" {
			t.Errorf("an unwithdrawn pinned version must produce no notice; got:\n%s", diag.String())
		}
	})
}
