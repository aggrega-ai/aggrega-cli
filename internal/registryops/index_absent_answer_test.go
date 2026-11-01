// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryops

import (
	"net/http"
	"slices"
	"strings"
	"testing"
)

// TestAMarkerTheRegistryAnswersForIsJudgedAndTheVersionStaysListed walks a registry
// whose deprecation markers cannot be honored for an ANSWER the registry gives,
// and requires the walk to report the marker not honored, record nothing of it,
// and index the repository with every version still listed. A marker with no
// verified signer is never acted on, and the version it names stays selectable;
// leaving the repository out of the index instead would hide every version of it.
//
// Two answers are walked: a marker carrying no signature at all, and a marker
// whose own manifest the registry answers it does not hold (404) when its
// signatures are looked up. The last row is the control: the same read failing
// with a 503 on every attempt is no answer, and the repository is left out and
// named, as a read that keeps failing always is. Reading the 404 as a failure to
// read turns the second row into the third: the repository is left out.
func TestAMarkerTheRegistryAnswersForIsJudgedAndTheVersionStaysListed(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name string
		// setup changes the registry after the fixture is built, and returns the
		// fault it set, if any.
		setup func(t *testing.T, f *failureFixture) *readFault
		// repo is the repository whose marker is not honored, and version the
		// version that marker names.
		repo, version string
		// readable is false for the control row: the repository is not read.
		readable bool
	}{
		{
			name: "a marker with no signature",
			setup: func(t *testing.T, f *failureFixture) *readFault {
				attachUnsignedMarker(t, f.reg.repository(t, cleanRepo), "0.1.0")
				return nil
			},
			repo: cleanRepo, version: "0.1.0", readable: true,
		},
		{
			name: "a marker whose manifest the registry answers it does not hold",
			setup: func(t *testing.T, f *failureFixture) *readFault {
				fault := &readFault{repo: failingRepo, read: readReferrerResolve, status: http.StatusNotFound}
				f.reg.addFault(fault)
				return fault
			},
			repo: failingRepo, version: "1.0.0", readable: true,
		},
		{
			name: "control: a marker whose manifest the registry keeps failing to resolve",
			setup: func(t *testing.T, f *failureFixture) *readFault {
				fault := &readFault{repo: failingRepo, read: readReferrerResolve, status: http.StatusServiceUnavailable}
				f.reg.addFault(fault)
				return fault
			},
			repo: failingRepo, version: "1.0.0", readable: false,
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			f := newFailureFixture(t)
			fault := row.setup(t, f)
			ws := f.workspace(t, f.pub)
			res, out := updateResult(t, ws, UpdateOptions{})

			if fault != nil {
				if fired, _ := f.reg.faultState(fault); fired == 0 {
					t.Fatal("the fault never met a read, so the walk below did not meet the answer it is about")
				}
			}
			if !row.readable {
				if got := skippedRepos(res.Hosts[0]); !slices.Equal(got, []string{row.repo}) {
					t.Errorf("repositories reported unreadable = %v; want %s\n%s", got, row.repo, out)
				}
				if rec, found := indexedRecord(t, ws, row.repo); found {
					t.Errorf("the repository was recorded from a walk whose read failed: %+v\n%s", rec, out)
				}
				return
			}

			if got := skippedRepos(res.Hosts[0]); len(got) != 0 {
				t.Errorf("repositories reported unreadable = %v; want none, since the registry answered every read\n%s", got, out)
			}
			rec, found := indexedRecord(t, ws, row.repo)
			if !found {
				t.Fatalf("the repository is missing from the index; a marker that is not honored must leave it listed\n%s", out)
			}
			if !slices.Contains(rec.Versions, row.version) {
				t.Errorf("versions = %v; want %s still listed", rec.Versions, row.version)
			}
			for _, dep := range rec.Deprecations {
				if dep.Version == row.version {
					t.Errorf("a marker that is not honored was recorded: %+v", dep)
				}
			}
			for _, pv := range rec.Published {
				if pv.Signer == nil {
					t.Errorf("version %s recorded with no signer; every version here is signed\n%s", pv.Version, out)
				}
			}
			if want := row.repo + ":" + row.version + ": marker present but not honored"; !strings.Contains(out, want) {
				t.Errorf("the commentary does not report the marker not honored (%q):\n%s", want, out)
			}
			var noted bool
			for _, n := range res.Hosts[0].Deprecations {
				if n.Repo == row.repo && n.Version == row.version {
					noted = true
					if n.Recorded {
						t.Errorf("the marker is noted as recorded: %+v", n)
					}
				}
			}
			if !noted {
				t.Errorf("the walk's outcome carries no note for the marker on %s:%s: %+v", row.repo, row.version, res.Hosts[0].Deprecations)
			}
		})
	}
}

// TestARepositoryTheRegistryNoLongerHoldsIsDroppedNotKept follows an earlier index
// with a refresh whose tag listing for one repository the registry answers 404 —
// it holds no such repository — and requires the walk to drop the repository from
// the index and name it, rather than keep its earlier record: that answer is the
// same on every later walk, so keeping the record would keep a repository that is
// gone. The control row answers the same listing 503 on every attempt, which is
// no answer, and the earlier record is kept. Reading the 404 as a failure to read
// turns the first row into the second.
func TestARepositoryTheRegistryNoLongerHoldsIsDroppedNotKept(t *testing.T) {
	t.Parallel()

	t.Run("the tag listing answers 404", func(t *testing.T) {
		t.Parallel()
		f := newFailureFixture(t)
		ws := f.workspace(t, f.pub)
		updateResult(t, ws, UpdateOptions{})
		if _, found := indexedRecord(t, ws, failingRepo); !found {
			t.Fatal("the earlier walk did not index the repository, so the walk below cannot drop it")
		}

		fault := &readFault{repo: failingRepo, read: readTagListing, status: http.StatusNotFound}
		f.reg.addFault(fault)
		res, out := updateResult(t, ws, UpdateOptions{})

		if fired, _ := f.reg.faultState(fault); fired == 0 {
			t.Fatal("the fault never met a read, so the walk below did not meet the answer it is about")
		}
		if rec, found := indexedRecord(t, ws, failingRepo); found {
			t.Errorf("the index keeps a repository the registry answers it no longer holds: %+v\n%s", rec, out)
		}
		if got := skippedRepos(res.Hosts[0]); !slices.Equal(got, []string{failingRepo}) {
			t.Errorf("repositories reported = %v; want %s named\n%s", got, failingRepo, out)
		}
		if !strings.Contains(out, "skipped "+failingRepo+": ") || strings.Contains(out, "its earlier record is kept") {
			t.Errorf("the commentary does not say the repository was dropped:\n%s", out)
		}
		if _, found := indexedRecord(t, ws, cleanRepo); !found {
			t.Errorf("the other repository was not indexed\n%s", out)
		}
	})

	t.Run("control: the tag listing answers 503 on every attempt", func(t *testing.T) {
		t.Parallel()
		f := newFailureFixture(t)
		ws := f.workspace(t, f.pub)
		updateResult(t, ws, UpdateOptions{})
		before := ws.files(t)

		fault := &readFault{repo: failingRepo, read: readTagListing, status: http.StatusServiceUnavailable}
		f.reg.addFault(fault)
		res, out := updateResult(t, ws, UpdateOptions{})

		if fired, _ := f.reg.faultState(fault); fired == 0 {
			t.Fatal("the fault never met a read, so the walk below is not a walk through a failure")
		}
		assertKept(t, ws, res, out, before)
	})
}
