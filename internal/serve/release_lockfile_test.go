// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// The Releases the answer tests lay, each chosen for what it separates:
//
//   - app-release.yaml            a .yaml Release the candidate walk lists
//   - staging.yml                 a .yml Release the candidate walk lists
//   - vendor/other-release.yaml   a Release the walk skips, which the marker can still select
//   - release.rel                 a Release named with no YAML extension, which the walk skips.
//     It is the one name here for which the rule and a derivation that strips whatever
//     extension it finds disagree, so it is what makes a second derivation visible.
var lockfileProbeReleases = []string{"app-release.yaml", "staging.yml", "vendor/other-release.yaml", "release.rel"}

// newLockfileProbeWorkspace lays lockfileProbeReleases under a fresh root, with no
// lockfile anywhere, and returns the root.
func newLockfileProbeWorkspace(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	for _, rel := range lockfileProbeReleases {
		writeRelease(t, ws, rel, "release/"+strings.NewReplacer("/", "-", ".", "-").Replace(rel))
	}
	return ws
}

// gateLockfile reads the lockfile path the graph request's lockfile-absent refusal names
// for release. That refusal is where the server applies the rule to decide which file it
// requires, so an answer naming a Release's lockfile is held against the file the server
// itself goes looking for, rather than against a copy of the rule written here.
func gateLockfile(t *testing.T, srv *Server, release string) string {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/irGraph", irGraphParams{Release: release}))
	resp, _ := runOneRequestPastPushes(t, srv, &in, &out)
	if resp.Error == nil || resp.Error.Code != errLockfileAbsent || resp.Error.Data == nil {
		t.Fatalf("the graph request for %s did not refuse for the absent lockfile, so there is no gate "+
			"reading to hold the answers against: %+v (result %s)", release, resp.Error, resp.Result)
	}
	return resp.Error.Data.Lockfile
}

// TestLockfilesForAppliesTheCompilersRule pins the one map builder both the language
// server's candidate answer and the assistant-facing Release listing call.
//
// Every row is held against the rule itself rather than against a literal, because the
// property is that there is one rule and this applies it: a literal here would be a
// second statement of where a lockfile lives. What stops that from being circular is the
// table-level check below — at least one row must be a name for which the rule and a
// derivation that strips whatever extension it finds disagree, or a second derivation
// would pass every row.
func TestLockfilesForAppliesTheCompilersRule(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name    string
		release string
	}{
		{name: "a .yaml Release given absolutely", release: "/w/app-release.yaml"},
		{name: "a .yml Release given absolutely", release: "/w/staging.yml"},
		{name: "a Release in a subdirectory given relatively", release: "envs/prod.yaml"},
		{name: "a Release named with no YAML extension", release: "release.rel"},
		{name: "a Release named with no extension at all", release: "release"},
		{name: "a Release whose stem carries dots", release: "app.v2.yaml"},
	}

	discriminates := false
	all := make([]string, 0, len(rows))
	for _, row := range rows {
		all = append(all, row.release)
		stripped := strings.TrimSuffix(row.release, filepath.Ext(row.release)) + ".lock"
		if lockfile.PathFor(row.release) != stripped {
			discriminates = true
		}
		t.Run(row.name, func(t *testing.T) {
			got := LockfilesFor([]string{row.release})
			if len(got) != 1 {
				t.Fatalf("one Release in gave %d entries out: %v", len(got), got)
			}
			if want := lockfile.PathFor(row.release); got[row.release] != want {
				t.Errorf("lockfile for %q = %q; the rule places it at %q", row.release, got[row.release], want)
			}
		})
	}
	if !discriminates {
		t.Fatal("no row names a Release for which the rule and a derivation stripping any extension " +
			"disagree, so a second derivation would pass this whole table")
	}

	t.Run("every Release given is a key, and nothing else is", func(t *testing.T) {
		got := LockfilesFor(all)
		var keys []string
		for key := range got {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		want := append([]string(nil), all...)
		sort.Strings(want)
		if strings.Join(keys, "\n") != strings.Join(want, "\n") {
			t.Errorf("keys %v; want exactly the Releases given %v", keys, want)
		}
	})

	t.Run("no Release gives an empty object, never null", func(t *testing.T) {
		got := LockfilesFor(nil)
		if got == nil {
			t.Fatal("LockfilesFor(nil) returned a nil map, which reaches the wire as null")
		}
		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(encoded) != "{}" {
			t.Errorf("an empty listing encoded as %s; want {}", encoded)
		}
	})
}

// TestActiveReleaseNamesTheLockfileTheGateChecks drives aggrega/activeRelease with the
// marker at each probe Release in turn, and holds the answer's lockfile against the file
// the graph request's gate requires for that same Release.
//
// Two of the four are Releases the candidate walk never lists — one under vendor/, one
// named with no YAML extension — which is why this answer carries the path at all: a
// client that selected one of them by default has no candidate entry to read it from.
// The last row selects nothing, and the lockfile must then be absent from the wire rather
// than present and empty: there is no Release for it to belong to.
func TestActiveReleaseNamesTheLockfileTheGateChecks(t *testing.T) {
	t.Parallel()
	ws := newLockfileProbeWorkspace(t)
	srv := newLazyCascadeServer(t, ws, nil, nil)
	markerDir := filepath.Join(ws, ".aggrega")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatalf("mkdir marker dir: %v", err)
	}
	marker := filepath.Join(markerDir, "active-release")

	for _, target := range lockfileProbeReleases {
		t.Run("marker at "+target, func(t *testing.T) {
			if err := os.WriteFile(marker, []byte(target+"\n"), 0o644); err != nil {
				t.Fatalf("write marker: %v", err)
			}
			got := activeReleaseAnswer(t, srv)
			wantRelease := filepath.Join(ws, target)
			if got.Release != wantRelease {
				t.Fatalf("release = %q; want %q — the marker did not select the Release this row is about",
					got.Release, wantRelease)
			}
			gate := gateLockfile(t, srv, target)
			if got.Lockfile != gate {
				t.Errorf("lockfile = %q; the graph request's gate requires %q for the same Release",
					got.Lockfile, gate)
			}
			if want := lockfile.PathFor(wantRelease); got.Lockfile != want {
				t.Errorf("lockfile = %q; the rule places it at %q", got.Lockfile, want)
			}
		})
	}

	t.Run("nothing selected", func(t *testing.T) {
		if err := os.Remove(marker); err != nil {
			t.Fatalf("remove marker: %v", err)
		}
		// Two walk-listed Releases and no marker: the cascade selects none.
		var in, out bytes.Buffer
		in.Write(framedRequest(1, "aggrega/activeRelease", map[string]any{}))
		resp := runOneRequest(t, srv, &in, &out)
		if resp.Error != nil {
			t.Fatalf("an unresolved workspace was answered with an error: %+v", resp.Error)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(resp.Result, &raw); err != nil {
			t.Fatalf("unmarshal raw result: %v (raw: %s)", err, resp.Result)
		}
		if release := string(raw["release"]); release != `""` {
			t.Fatalf("release = %s; want the empty string, or this row is not the no-selection case", release)
		}
		if lockfileKey, present := raw["lockfile"]; present {
			t.Errorf("the answer names a lockfile (%s) while selecting no Release", lockfileKey)
		}
	})
}

// TestReleaseCandidatesNameEachListedReleasesLockfile drives aggrega/releaseCandidates and
// holds its lockfiles map to two things: its keys are exactly the Releases the answer
// lists, spelled as they are listed, and each value is the file the graph request's gate
// requires for that Release. The empty workspace row pins that the map is present as an
// empty object, so a client reads it without a null check.
func TestReleaseCandidatesNameEachListedReleasesLockfile(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name       string
		lay        func(t *testing.T) string
		wantListed []string
	}{
		{
			name:       "the probe workspace",
			lay:        newLockfileProbeWorkspace,
			wantListed: []string{"app-release.yaml", "staging.yml"},
		},
		{
			name:       "a workspace holding no Release",
			lay:        func(t *testing.T) string { return t.TempDir() },
			wantListed: nil,
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws := row.lay(t)
			srv := newTestServer(t, ws, nil, nil)
			var in, out bytes.Buffer
			in.Write(framedRequest(1, "aggrega/releaseCandidates", map[string]any{}))
			resp := runOneRequest(t, srv, &in, &out)
			if resp.Error != nil {
				t.Fatalf("releaseCandidates refused: %+v", resp.Error)
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(resp.Result, &raw); err != nil {
				t.Fatalf("unmarshal raw result: %v (raw: %s)", err, resp.Result)
			}
			if encoded := strings.TrimSpace(string(raw["lockfiles"])); !strings.HasPrefix(encoded, "{") {
				t.Fatalf("lockfiles reached the wire as %q; want an object, empty when nothing is listed", encoded)
			}
			var got releaseCandidatesResult
			if err := json.Unmarshal(resp.Result, &got); err != nil {
				t.Fatalf("unmarshal result: %v", err)
			}

			var listed []string
			for _, abs := range got.Releases {
				listed = append(listed, strings.TrimPrefix(abs, ws+string(filepath.Separator)))
			}
			sort.Strings(listed)
			if strings.Join(listed, "\n") != strings.Join(row.wantListed, "\n") {
				t.Fatalf("listed %v; want %v — the fixture is not the one this row is about", listed, row.wantListed)
			}

			var missing, extra []string
			for _, abs := range got.Releases {
				if _, ok := got.Lockfiles[abs]; !ok {
					missing = append(missing, abs)
				}
			}
			listedSet := map[string]bool{}
			for _, abs := range got.Releases {
				listedSet[abs] = true
			}
			for key := range got.Lockfiles {
				if !listedSet[key] {
					extra = append(extra, key)
				}
			}
			sort.Strings(missing)
			sort.Strings(extra)
			if len(missing) > 0 || len(extra) > 0 {
				t.Errorf("lockfiles keys disagree with the listed Releases: missing %v, not listed %v", missing, extra)
			}

			for _, abs := range got.Releases {
				rel := strings.TrimPrefix(abs, ws+string(filepath.Separator))
				if gate := gateLockfile(t, srv, rel); got.Lockfiles[abs] != gate {
					t.Errorf("lockfiles[%s] = %q; the graph request's gate requires %q", rel, got.Lockfiles[abs], gate)
				}
			}
		})
	}
}
