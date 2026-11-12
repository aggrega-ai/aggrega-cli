// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compilerops

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// pinnedRetractedLibraryLockfile builds the case version selection structurally
// cannot reach: a workspace holding a library at an EXPLICITLY PINNED version that
// was retracted after the pin was taken.
//
// The pin is what makes it the interesting case. Selection excludes a withdrawn
// version only when it is choosing one; a reference that names the version exactly
// bypasses the choice, so nothing on the selection axis ever looks at it. If the
// build says nothing either, the workspace keeps building against a withdrawn
// library indefinitely — and with exclusion visibly working elsewhere, that silence
// reads as "nothing is withdrawn", which is a stronger failure than no coverage.
//
// One library per library section, each at a pinned version carrying a retracted
// marker.
func pinnedRetractedLibraryLockfile(generatedAt string) *lockfile.Lockfile {
	const pinned = "1.4.0"
	retracted := func(reason string) *lockfile.DeprecationRecord {
		return &lockfile.DeprecationRecord{
			Severity: string(deprecation.SeverityRetracted),
			Message:  reason,
		}
	}
	// The URI carries the pinned version, which is how a pinned reference reaches
	// the closure: the version is part of the identity rather than something the
	// resolver chose.
	const (
		schemaURI    = "PortSpec:" + pinned
		mappingURI   = "HelmValues:" + pinned
		workloadURI  = "Deployments:" + pinned
		artifactsURI = "Bundles:" + pinned
	)
	lf := &lockfile.Lockfile{
		GeneratedAt: generatedAt,
		SchemaLibraries: []lockfile.SchemaLibrary{{
			URI: schemaURI, ResolvedVersion: pinned, Deprecation: retracted("withdrawn: unsound port range"),
		}},
		MappingLibraries: []lockfile.MappingLibrary{{
			URI: mappingURI, ResolvedVersion: pinned, Deprecation: retracted("withdrawn: wrong values path"),
		}},
		WorkloadLibraries: []lockfile.WorkloadLibrary{{
			URI: workloadURI, ResolvedVersion: pinned, Deprecation: retracted("withdrawn: bad probe defaults"),
		}},
		ArtifactLibraries: []lockfile.ArtifactLibrary{{
			URI: artifactsURI, ResolvedVersion: pinned, Deprecation: retracted("withdrawn: stale bundle"),
		}},
	}
	return lf
}

// TestBuildReportsAPinnedRetractedLibrary is the demonstration the build axis
// exists for: every library section carrying a recorded withdrawal at an
// explicitly pinned version is reported at build time, with the moment the record
// was observed and the command that refreshes it.
//
// The expected set is derived from the lockfile's own enumeration of its
// marker-bearing rows rather than from a hand-written list of library kinds, so a
// library section added later is covered by this test without anyone remembering
// to extend it — the same enumeration the gate itself reads, which is what makes
// an added section a build-time question rather than a silent omission.
func TestBuildReportsAPinnedRetractedLibrary(t *testing.T) {
	const stamp = "2026-08-01T09:30:00Z"
	lf := pinnedRetractedLibraryLockfile(stamp)

	var wantURIs []string
	for _, row := range lf.VerifiedFactRows() {
		if row.Deprecation() != nil {
			wantURIs = append(wantURIs, row.URI)
		}
	}
	if len(wantURIs) == 0 {
		t.Fatal("the fixture lockfile yielded no marker-bearing rows; the assertion would pass vacuously")
	}

	var out bytes.Buffer
	// The default workspace configures no halt threshold, so this arm proves the
	// REPORT is unconditional: a withdrawal is announced even where nothing stops.
	if err := enforceLockfileDeprecation(lf, workspaceconfig.BuildConfig{}, &out); err != nil {
		t.Fatalf("no configured threshold must halt on nothing; got: %v", err)
	}

	got := out.String()
	for _, uri := range wantURIs {
		if !strings.Contains(got, uri+" is retracted") {
			t.Errorf("the build said nothing about the withdrawn library %q — a pinned reference "+
				"bypasses selection, so this notice is the only place it is announced:\n%s", uri, got)
		}
	}
	if n := strings.Count(got, "as of "+stamp); n != len(wantURIs) {
		t.Errorf("every notice must state when the record was observed; got %d of %d:\n%s", n, len(wantURIs), got)
	}
	if !strings.Contains(got, "aggrega pull") {
		t.Errorf("the notice omits the command that refreshes the record:\n%s", got)
	}
}

// TestBuildHaltsOnAPinnedRetractedLibrary pins the halting arm on the library
// axis: a workspace that configured a halt threshold the recorded severity meets
// is stopped, exactly as it is for a catalog entity. Reporting and halting are
// independent axes and both must reach a library, because a workspace that opted
// into halting on withdrawal and was silently exempted for libraries got the
// weakest possible outcome — a configured policy that does not apply.
func TestBuildHaltsOnAPinnedRetractedLibrary(t *testing.T) {
	const stamp = "2026-08-01T09:30:00Z"
	lf := pinnedRetractedLibraryLockfile(stamp)

	var out bytes.Buffer
	err := enforceLockfileDeprecation(lf, workspaceconfig.BuildConfig{HaltOnDeprecatedSeverity: "retracted"}, &out)
	if !errors.Is(err, deprecation.ErrDeprecatedEntity()) {
		t.Fatalf("a withdrawn library at the configured threshold must halt the build; got: %v", err)
	}
	// The FIRST halting reference in the sorted walk owns the error, so the halt is
	// deterministic rather than dependent on closure order. "Bundles:1.4.0" sorts
	// first among the four fixture URIs.
	if !strings.Contains(err.Error(), "Bundles:1.4.0 is retracted") {
		t.Errorf("the halt must name the first halting reference in the sorted walk:\n%v", err)
	}
	// A configured halt must not cost the reader a notice for a reference sorted
	// after the halting one.
	for _, uri := range []string{"Bundles:1.4.0", "Deployments:1.4.0", "HelmValues:1.4.0", "PortSpec:1.4.0"} {
		if !strings.Contains(out.String(), uri+" is retracted") {
			t.Errorf("a configured halt must not cost a notice; missing %q:\n%s", uri, out.String())
		}
	}
}

// TestBuildReportsALibraryAlongsideAnEntity proves the two axes share one gate
// rather than one gate each: a lockfile holding both a withdrawn entity and a
// withdrawn library produces both notices from a single sorted walk, with one
// halt owned by whichever sorts first.
func TestBuildReportsALibraryAlongsideAnEntity(t *testing.T) {
	const stamp = "2026-08-01T09:30:00Z"
	const entityURI = "oci://reg.example/components/api"
	const libraryURI = "PortSpec:1.4.0"

	rec := func(msg string) *lockfile.DeprecationRecord {
		return &lockfile.DeprecationRecord{Severity: string(deprecation.SeverityRetracted), Message: msg}
	}
	lf := &lockfile.Lockfile{
		GeneratedAt:     stamp,
		Entities:        []lockfile.Entity{{URI: entityURI, Deprecation: rec("withdrawn: bad release")}},
		SchemaLibraries: []lockfile.SchemaLibrary{{URI: libraryURI, Deprecation: rec("withdrawn: unsound port range")}},
	}

	var out bytes.Buffer
	if err := enforceLockfileDeprecation(lf, workspaceconfig.BuildConfig{}, &out); err != nil {
		t.Fatalf("no configured threshold must halt on nothing; got: %v", err)
	}
	for _, uri := range []string{entityURI, libraryURI} {
		if !strings.Contains(out.String(), uri+" is retracted") {
			t.Errorf("one gate must report both axes; missing %q:\n%s", uri, out.String())
		}
	}
}
