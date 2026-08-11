// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

// Package main's retraction-at-selection integration test. Build-tagged
// `integration` because it drives a real in-process OCI registry, whose
// test-only dependency must not enter the production binary's import graph —
// the same reason the registry round-trip test carries the tag.
package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// retractionFixture is one prepared registry for the selection arms below: a real
// in-process registry holding two signed versions of one publishable entity, a
// workspace whose registry policy pins the publisher's key, and the local index
// built from that registry through the production `registry update`.
type retractionFixture struct {
	host      string
	rw        *workspaceconfig.ResolvedWorkspace
	reference string
}

// retractionSubject describes the publishable entity a fixture holds: the
// repository path its publish binds to under the registry host, the entity file's
// name, and the per-version entity body. Parameterising it is what lets the same
// six production steps — push, marker attach, marker signature, index build, index
// verification, screened selection — cover each discovery step that selects a
// version, rather than one of them with the others taken on trust.
type retractionSubject struct {
	repoPath string
	filename string
	body     func(version string) string
}

// componentSubject is the core catalog entity the entity discovery step resolves.
func componentSubject() retractionSubject {
	return retractionSubject{
		repoPath: "component/test/svc",
		filename: "component.yaml",
		body: func(version string) string {
			return `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/svc
  version: "` + version + `"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      port: { type: integer, default: 8080 }
`
		},
	}
}

// schemaLibrarySubject is the library the library discovery step resolves by its
// host-less identity.
func schemaLibrarySubject() retractionSubject {
	return retractionSubject{
		repoPath: "schemalibrary/dbsizing",
		filename: "schemalibrary.yaml",
		body: func(version string) string {
			return `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: DbSizing
  version: "` + version + `"
spec:
  schemas:
    Storage:
      size: { type: string, default: "10Gi" }
`
		},
	}
}

// aggregateSubject is the published Aggregate the sub-aggregate discovery step
// resolves by its bare identity.
func aggregateSubject() retractionSubject {
	return retractionSubject{
		repoPath: "aggregate/test/app-agg",
		filename: "aggregate.yaml",
		body: func(version string) string {
			return `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test/app-agg
  version: "` + version + `"
spec:
  components:
    app:
      type: test/svc
`
		},
	}
}

// newRetractionFixture publishes the Component subject at 1.0.0 AND 2.0.0, the
// shape every entity-route arm below reads.
func newRetractionFixture(t *testing.T, markers map[string]string) retractionFixture {
	t.Helper()
	return newRetractionFixtureFor(t, componentSubject(), markers)
}

// newRetractionFixtureFor publishes the subject at 1.0.0 AND 2.0.0 to a fresh
// registry through the production signed push, optionally attaches a signed
// deprecation marker of the given severity to each named version through the
// production `aggrega deprecate`, then builds the local index through the
// production `registry update`.
//
// Two published versions is the whole point: a single-version repository cannot
// show a selection consequence at all, because there is nothing else to select.
// Every link is the production path — the push, the marker attach, the marker's
// own signature, the index build and its verification — so a passing arm cannot
// be an artefact of a hand-stubbed index.
func newRetractionFixtureFor(t *testing.T, subject retractionSubject, markers map[string]string) retractionFixture {
	t.Helper()

	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}

	host := startRegistry(t, nil)
	cat := t.TempDir()
	for _, version := range []string{"1.0.0", "2.0.0"} {
		path := filepath.Join(cat, subject.repoPath, version, subject.filename)
		writeFixture(t, path, subject.body(version))
		pushCatalogEntitySigned(t, host, path, signer)
	}

	// The marker is attached by the same command an operator would run, and its own
	// signature is produced by the publisher's key — so the index build has a real
	// signature to verify rather than a fabricated verdict to trust. The workspace
	// is declared here, once for the whole loop, and is deliberately not the
	// fixture's own consumer workspace built below: the attach needs a reference
	// and a signer, and the fixture's registry trust policy is the consumer's
	// concern rather than the publisher's.
	markerWorkspace := resolveTempWorkspace(t, tempWorkspaceOptions{})
	for version, severity := range markers {
		if derr := deprecateRun(deprecateConfig{
			ws:        markerWorkspace,
			Reference: host + "/" + subject.repoPath + ":" + version,
			Severity:  severity,
			Reason:    "withdrawn in favour of a successor",
			Signer:    signer,
			EnvLookup: func(string) string { return "" },
		}); derr != nil {
			t.Fatalf("deprecateRun(%s, %s): %v", version, severity, derr)
		}
	}

	var b strings.Builder
	b.WriteString("workspace: {}\n")
	b.WriteString("default_registry: \"" + host + "\"\n")
	b.WriteString("offline: false\n")
	b.WriteString("registries:\n")
	b.WriteString("  - host: \"" + host + "\"\n")
	b.WriteString("    name: fixture\n")
	b.WriteString("    public_key: |\n")
	for _, line := range strings.Split(strings.TrimRight(string(pubPEM), "\n"), "\n") {
		b.WriteString("      " + line + "\n")
	}
	rw := resolveWorkspaceWithConfig(t, t.TempDir(), b.String())

	var updateOut bytes.Buffer
	if uerr := registryUpdateRun(registryUpdateConfig{ws: rw, Out: &updateOut}); uerr != nil {
		t.Fatalf("registryUpdateRun: %v\n%s", uerr, updateOut.String())
	}

	return retractionFixture{host: host, rw: rw, reference: host + "/" + subject.repoPath}
}

// screenedPuller builds the production entity puller with the production
// withdrawal screen attached, reading the fixture's real on-disk index.
func (f retractionFixture) screenedPuller(t *testing.T, out *bytes.Buffer) catalog.OCIPuller {
	t.Helper()
	wsCfg := f.rw.Config()
	screen := newRetractionScreen(f.rw, wsCfg.DefaultRegistry, out)
	puller := catalog.NewOCIPullerWithRetractionScreen(wsCfg, screen)
	if puller == nil {
		t.Fatal("puller is nil; the fixture workspace must not be offline")
	}
	return puller
}

// pullEntity resolves the Component through the production entity pull. floor is
// the reference's explicit version, empty for the unversioned reference whose
// resolution the withdrawal screen governs.
func pullEntity(t *testing.T, puller catalog.OCIPuller, floor string) (string, error) {
	t.Helper()
	ep, ok := puller.(catalog.EntityPuller)
	if !ok {
		t.Fatal("production puller must implement EntityPuller")
	}
	_, version, err := ep.PullEntity(context.Background(), "test/svc", "Component", floor)
	return version, err
}

// TestIntegration_RetractionExcludesAtSelection is the discriminating pair the
// selection consequence needs, plus the arms that fence it.
//
// The pair is load-bearing and neither half proves anything alone: "the lower
// version resolves" passes identically whether the exclusion fired or whether the
// higher version was never a candidate, so the same two-version fixture is
// resolved twice — once with the higher version withdrawn and once without — and
// only the DIFFERENCE between the two outcomes is evidence.
func TestIntegration_RetractionExcludesAtSelection(t *testing.T) {
	t.Run("retracted_higher_version_is_excluded_and_announced", func(t *testing.T) {
		f := newRetractionFixture(t, map[string]string{"2.0.0": "retracted"})
		var diag bytes.Buffer
		version, err := pullEntity(t, f.screenedPuller(t, &diag), "")
		if err != nil {
			t.Fatalf("an unversioned reference with one unretracted version must resolve; got: %v", err)
		}
		if version != "1.0.0" {
			t.Fatalf("resolved %q; want 1.0.0 — the withdrawn 2.0.0 must not be selected", version)
		}
		notice := diag.String()
		// The announcement is not decoration: a version the reference did not name
		// was substituted on evidence absent from the author's own files.
		if !strings.Contains(notice, "2.0.0 is withdrawn and was not selected") {
			t.Errorf("the exclusion must be announced; diagnostics were:\n%s", notice)
		}
		if !strings.Contains(notice, "withdrawn in favour of a successor") {
			t.Errorf("the announcement must carry the recorded reason; diagnostics were:\n%s", notice)
		}
		if !strings.Contains(notice, "resolved to 1.0.0 instead") {
			t.Errorf("the announcement must name the version taken instead; diagnostics were:\n%s", notice)
		}
		// No message may imply the record is current: it is a snapshot.
		if !strings.Contains(notice, "aggrega registry update") {
			t.Errorf("the announcement must name the refresh path; diagnostics were:\n%s", notice)
		}
	})

	t.Run("soft_severity_on_the_same_fixture_still_selects_the_higher", func(t *testing.T) {
		// The other half of the pair, and the sharpest form of it: the marker is
		// present, signed and verified exactly as above, so the screen is live and
		// holds a record for this very artifact. Only the SEVERITY differs. A screen
		// that excluded on any record rather than on the withdrawal severity would
		// pass the arm above and fail here.
		f := newRetractionFixture(t, map[string]string{"2.0.0": "warning"})
		var diag bytes.Buffer
		version, err := pullEntity(t, f.screenedPuller(t, &diag), "")
		if err != nil {
			t.Fatalf("a soft marker must not affect resolution; got: %v", err)
		}
		if version != "2.0.0" {
			t.Fatalf("resolved %q; want 2.0.0 — the soft tier warns and does not change selection", version)
		}
		if strings.Contains(diag.String(), "retraction:") {
			t.Errorf("a soft marker must announce no exclusion; diagnostics were:\n%s", diag.String())
		}
	})

	t.Run("no_marker_at_all_selects_the_higher", func(t *testing.T) {
		f := newRetractionFixture(t, nil)
		var diag bytes.Buffer
		version, err := pullEntity(t, f.screenedPuller(t, &diag), "")
		if err != nil {
			t.Fatalf("an unmarked fixture must resolve: %v", err)
		}
		if version != "2.0.0" {
			t.Fatalf("resolved %q; want 2.0.0 for an unmarked fixture", version)
		}
	})

	t.Run("before_picture_unscreened_selection_takes_the_retracted_version", func(t *testing.T) {
		// The shipped posture this item changes, preserved by construction rather
		// than reconstructed: an unscreened puller is exactly the selection behaviour
		// that existed before the screen, and it SELECTS the withdrawn version. The
		// build-time gate is what then halts on it, which the retracted-severity arm
		// of the consume path already proves.
		f := newRetractionFixture(t, map[string]string{"2.0.0": "retracted"})
		version, err := pullEntity(t, catalog.NewDefaultOCIPuller(f.rw.Config()), "")
		if err != nil {
			t.Fatalf("unscreened resolution must succeed: %v", err)
		}
		if version != "2.0.0" {
			t.Fatalf("unscreened resolution took %q; want 2.0.0 — selection is marker-blind without a screen", version)
		}
	})

	t.Run("explicit_pin_for_a_retracted_version_still_resolves_loudly", func(t *testing.T) {
		// Naming the version IS the override. This is what makes the exclusion safe
		// to ship: it stops new accidental selection without breaking a build that
		// already committed to the version.
		f := newRetractionFixture(t, map[string]string{"2.0.0": "retracted"})
		var diag bytes.Buffer
		version, err := pullEntity(t, f.screenedPuller(t, &diag), "2.0.0")
		if err != nil {
			t.Fatalf("an explicit pin for a withdrawn version must still resolve; got: %v", err)
		}
		if version != "2.0.0" {
			t.Fatalf("resolved %q; want the pinned 2.0.0", version)
		}
		if !strings.Contains(diag.String(), "names it explicitly") {
			t.Errorf("a pinned withdrawn version must resolve LOUDLY; diagnostics were:\n%s", diag.String())
		}
	})

	t.Run("every_version_retracted_halts_loudly_and_not_as_not_found", func(t *testing.T) {
		f := newRetractionFixture(t, map[string]string{"1.0.0": "retracted", "2.0.0": "retracted"})
		var diag bytes.Buffer
		_, err := pullEntity(t, f.screenedPuller(t, &diag), "")
		if !errors.Is(err, catalog.ErrAllVersionsRetracted()) {
			t.Fatalf("a fully-withdrawn repository must halt with ErrAllVersionsRetracted; got: %v", err)
		}
		// The path of least resistance would have surfaced this as a missing entity,
		// which names the wrong problem and reads as a quiet fallback.
		if errors.Is(err, oci.ErrFetchNotFound()) {
			t.Fatalf("a fully-withdrawn repository must NOT report as not-found; got: %v", err)
		}
		for _, want := range []string{"1.0.0", "2.0.0", "withdrawn in favour of a successor"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("halt message must name %q; got: %v", want, err)
			}
		}
	})
}

// pullLibrary resolves the SchemaLibrary through the production library discovery
// step. floor is the reference's explicit version, empty for the bare-name
// reference whose resolution the withdrawal screen governs.
func pullLibrary(t *testing.T, puller catalog.OCIPuller, floor string) (string, error) {
	t.Helper()
	lp, ok := puller.(catalog.LibraryPuller)
	if !ok {
		t.Fatal("production puller must implement LibraryPuller")
	}
	_, version, err := lp.PullLibrary(context.Background(), "DbSizing", "SchemaLibrary", floor)
	return version, err
}

// pullAggregate resolves the published Aggregate through the production
// sub-aggregate discovery step.
func pullAggregate(t *testing.T, puller catalog.OCIPuller, floor string) (string, error) {
	t.Helper()
	ap, ok := puller.(catalog.AggregatePuller)
	if !ok {
		t.Fatal("production puller must implement AggregatePuller")
	}
	_, version, err := ap.PullAggregate(context.Background(), "test/app-agg", floor)
	return version, err
}

// TestIntegration_RetractionExcludesAtLibrarySelection is the library discovery
// step's discriminating pair, plus its explicit-pin arm.
//
// It is the same load-bearing pair the entity route carries and for the same
// reason: "the lower version resolves" passes identically whether the exclusion
// fired or whether the higher version was never published, so the same two-version
// library is resolved twice — once with the higher version withdrawn and once
// without — and only the DIFFERENCE is evidence. A screen keyed on the entity
// route alone would pass the unmarked arm and fail the marked one.
func TestIntegration_RetractionExcludesAtLibrarySelection(t *testing.T) {
	t.Run("retracted_higher_version_is_excluded_and_announced", func(t *testing.T) {
		f := newRetractionFixtureFor(t, schemaLibrarySubject(), map[string]string{"2.0.0": "retracted"})
		var diag bytes.Buffer
		version, err := pullLibrary(t, f.screenedPuller(t, &diag), "")
		if err != nil {
			t.Fatalf("a bare-name library with one unretracted version must resolve; got: %v", err)
		}
		if version != "1.0.0" {
			t.Fatalf("resolved %q; want 1.0.0 — the withdrawn 2.0.0 must not be selected", version)
		}
		notice := diag.String()
		for _, want := range []string{
			"2.0.0 is withdrawn and was not selected",
			"withdrawn in favour of a successor",
			"resolved to 1.0.0 instead",
			"aggrega registry update",
		} {
			if !strings.Contains(notice, want) {
				t.Errorf("the announcement must contain %q; diagnostics were:\n%s", want, notice)
			}
		}
	})

	t.Run("no_marker_at_all_selects_the_higher", func(t *testing.T) {
		f := newRetractionFixtureFor(t, schemaLibrarySubject(), nil)
		var diag bytes.Buffer
		version, err := pullLibrary(t, f.screenedPuller(t, &diag), "")
		if err != nil {
			t.Fatalf("an unmarked library must resolve: %v", err)
		}
		if version != "2.0.0" {
			t.Fatalf("resolved %q; want 2.0.0 for an unmarked library", version)
		}
	})

	t.Run("explicit_pin_for_a_retracted_version_still_resolves_loudly", func(t *testing.T) {
		// Naming the version IS the override, on this route as on the entity one.
		f := newRetractionFixtureFor(t, schemaLibrarySubject(), map[string]string{"2.0.0": "retracted"})
		var diag bytes.Buffer
		version, err := pullLibrary(t, f.screenedPuller(t, &diag), "2.0.0")
		if err != nil {
			t.Fatalf("an explicit pin for a withdrawn library version must still resolve; got: %v", err)
		}
		if version != "2.0.0" {
			t.Fatalf("resolved %q; want the pinned 2.0.0", version)
		}
		if !strings.Contains(diag.String(), "names it explicitly") {
			t.Errorf("a pinned withdrawn version must resolve LOUDLY; diagnostics were:\n%s", diag.String())
		}
	})

	t.Run("unscreened_selection_takes_the_retracted_version", func(t *testing.T) {
		// The screen is what changes the outcome, not the fixture: an unscreened
		// puller over the identical registry state selects the withdrawn version.
		f := newRetractionFixtureFor(t, schemaLibrarySubject(), map[string]string{"2.0.0": "retracted"})
		version, err := pullLibrary(t, catalog.NewDefaultOCIPuller(f.rw.Config()), "")
		if err != nil {
			t.Fatalf("unscreened library resolution must succeed: %v", err)
		}
		if version != "2.0.0" {
			t.Fatalf("unscreened resolution took %q; want 2.0.0 — selection is marker-blind without a screen", version)
		}
	})

	t.Run("every_version_retracted_halts_loudly_and_not_as_not_found", func(t *testing.T) {
		f := newRetractionFixtureFor(t, schemaLibrarySubject(), map[string]string{"1.0.0": "retracted", "2.0.0": "retracted"})
		var diag bytes.Buffer
		_, err := pullLibrary(t, f.screenedPuller(t, &diag), "")
		if !errors.Is(err, catalog.ErrAllVersionsRetracted()) {
			t.Fatalf("a fully-withdrawn library must halt with ErrAllVersionsRetracted; got: %v", err)
		}
		if errors.Is(err, oci.ErrFetchNotFound()) {
			t.Fatalf("a fully-withdrawn library must NOT report as not-found; got: %v", err)
		}
	})
}

// TestIntegration_RetractionExcludesAtAggregateSelection is the sub-aggregate
// discovery step's discriminating pair, plus its explicit-pin arm. The pair is
// load-bearing for the same reason it is on the other two routes.
func TestIntegration_RetractionExcludesAtAggregateSelection(t *testing.T) {
	t.Run("retracted_higher_version_is_excluded_and_announced", func(t *testing.T) {
		f := newRetractionFixtureFor(t, aggregateSubject(), map[string]string{"2.0.0": "retracted"})
		var diag bytes.Buffer
		version, err := pullAggregate(t, f.screenedPuller(t, &diag), "")
		if err != nil {
			t.Fatalf("a bare-name Aggregate with one unretracted version must resolve; got: %v", err)
		}
		if version != "1.0.0" {
			t.Fatalf("resolved %q; want 1.0.0 — the withdrawn 2.0.0 must not be selected", version)
		}
		notice := diag.String()
		for _, want := range []string{
			"2.0.0 is withdrawn and was not selected",
			"withdrawn in favour of a successor",
			"resolved to 1.0.0 instead",
			"aggrega registry update",
		} {
			if !strings.Contains(notice, want) {
				t.Errorf("the announcement must contain %q; diagnostics were:\n%s", want, notice)
			}
		}
	})

	t.Run("no_marker_at_all_selects_the_higher", func(t *testing.T) {
		f := newRetractionFixtureFor(t, aggregateSubject(), nil)
		var diag bytes.Buffer
		version, err := pullAggregate(t, f.screenedPuller(t, &diag), "")
		if err != nil {
			t.Fatalf("an unmarked Aggregate must resolve: %v", err)
		}
		if version != "2.0.0" {
			t.Fatalf("resolved %q; want 2.0.0 for an unmarked Aggregate", version)
		}
	})

	t.Run("explicit_pin_for_a_retracted_version_still_resolves_loudly", func(t *testing.T) {
		f := newRetractionFixtureFor(t, aggregateSubject(), map[string]string{"2.0.0": "retracted"})
		var diag bytes.Buffer
		version, err := pullAggregate(t, f.screenedPuller(t, &diag), "2.0.0")
		if err != nil {
			t.Fatalf("an explicit pin for a withdrawn Aggregate version must still resolve; got: %v", err)
		}
		if version != "2.0.0" {
			t.Fatalf("resolved %q; want the pinned 2.0.0", version)
		}
		if !strings.Contains(diag.String(), "names it explicitly") {
			t.Errorf("a pinned withdrawn version must resolve LOUDLY; diagnostics were:\n%s", diag.String())
		}
	})

	t.Run("unscreened_selection_takes_the_retracted_version", func(t *testing.T) {
		f := newRetractionFixtureFor(t, aggregateSubject(), map[string]string{"2.0.0": "retracted"})
		version, err := pullAggregate(t, catalog.NewDefaultOCIPuller(f.rw.Config()), "")
		if err != nil {
			t.Fatalf("unscreened Aggregate resolution must succeed: %v", err)
		}
		if version != "2.0.0" {
			t.Fatalf("unscreened resolution took %q; want 2.0.0 — selection is marker-blind without a screen", version)
		}
	})
}

// TestIntegration_RetractionIndexRecordsVerifiedVerdictOnly proves the invariant
// the offline exclusion rests on: the index records a marker only when the
// marker's own signature verifies, so a consumer reading the index acts on a
// signed claim and never on an unauthenticated one.
func TestIntegration_RetractionIndexRecordsVerifiedVerdict(t *testing.T) {
	f := newRetractionFixture(t, map[string]string{"2.0.0": "retracted"})
	loaded, err := loadHostIndex(f.rw.Root(), f.rw.Config().ResolvedArtifactCacheDir(), f.host)
	if err != nil {
		t.Fatalf("loadHostIndex: %v", err)
	}
	var found bool
	for _, rec := range loaded.Records {
		for _, d := range rec.Deprecations {
			found = true
			if d.Signer == nil {
				t.Fatalf("recorded marker for %s:%s carries no verified signature", rec.URI, d.Version)
			}
			if d.Signer.Mode != "keyed" {
				t.Errorf("recorded signer mode = %q; want keyed for a key-pinned registry", d.Signer.Mode)
			}
		}
	}
	if !found {
		t.Fatal("no deprecation was recorded; the verified-index path did not run")
	}
	// The snapshot carries the moment it was taken, so no surface that reads it has
	// to imply a currency the design cannot provide.
	if !loaded.HasTimestamp {
		t.Error("the index must carry the moment it was built, so a stale record is distinguishable from a fresh one")
	}
}
