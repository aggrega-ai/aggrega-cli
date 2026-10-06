// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// setActiveRelease drives aggrega/setActiveRelease against srv through the
// production dispatch and returns the response, read past any diagnostics the
// answer publishes before it is written.
func setActiveRelease(t *testing.T, srv *Server, params map[string]any) jsonRPCResponse {
	t.Helper()
	return callPastPushes(t, srv, "aggrega/setActiveRelease", params)
}

// decodeActiveReleaseAnswer decodes a successful answer, failing the test on a
// refusal.
func decodeActiveReleaseAnswer(t *testing.T, resp jsonRPCResponse) activeReleaseResult {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("setActiveRelease was refused: %+v", resp.Error)
	}
	var result activeReleaseResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v (raw: %s)", err, resp.Result)
	}
	return result
}

// readMarker returns the bytes at path, or "(absent)" where nothing is there.
func readMarker(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "(absent)"
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// TestSetActiveReleaseWritesTheMarkerTheReaderReads is the plain write, over every
// layout in which the marker lands inside the workspace: nothing linked, a marker
// already naming another Release, a `.aggrega` directory that is a link to a
// directory inside the workspace, and a marker that is a link leading nowhere
// whose far end lies inside it. Each row names the file the marker's bytes must be
// found in afterwards, and the answer must be the one aggrega/activeRelease gives
// over the same workspace — asked separately, so the writer's answer is held
// against the reader's rather than against itself.
func TestSetActiveReleaseWritesTheMarkerTheReaderReads(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name string
		// lay builds the layout beneath root and returns the file the marker's
		// bytes must be found in once written.
		lay func(t *testing.T, root string) string
	}{
		{
			name: "nothing linked",
			lay: func(t *testing.T, root string) string {
				return filepath.Join(root, ".aggrega", "active-release")
			},
		},
		{
			name: "a marker already naming another Release is replaced",
			lay: func(t *testing.T, root string) string {
				plantFile(t, filepath.Join(root, ".aggrega", "active-release"), "other.yaml\n")
				return filepath.Join(root, ".aggrega", "active-release")
			},
		},
		{
			name: "a .aggrega directory linked to a directory inside the workspace",
			lay: func(t *testing.T, root string) string {
				plantDir(t, filepath.Join(root, "state"))
				plantLink(t, filepath.Join(root, "state"), filepath.Join(root, ".aggrega"))
				return filepath.Join(root, "state", "active-release")
			},
		},
		{
			name: "a marker linked nowhere, its far end inside the workspace",
			lay: func(t *testing.T, root string) string {
				plantDir(t, filepath.Join(root, ".aggrega"))
				plantDir(t, filepath.Join(root, "state"))
				plantLink(t, filepath.Join(root, "state", "chosen"), filepath.Join(root, ".aggrega", "active-release"))
				return filepath.Join(root, "state", "chosen")
			},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeRelease(t, root, "other.yaml", "release/other")
			want := writeRelease(t, root, filepath.Join("releases", "chosen.yaml"), "release/chosen")
			landing := row.lay(t, root)
			srv := newLazyCascadeServer(t, root, nil, nil)

			got := decodeActiveReleaseAnswer(t, setActiveRelease(t, srv, map[string]any{"release": "releases/chosen.yaml"}))

			if marker := readMarker(t, landing); marker != "releases/chosen.yaml\n" {
				t.Errorf("the marker at %s reads %q; want %q", landing, marker, "releases/chosen.yaml\n")
			}
			if got.Release != want {
				t.Errorf("the answer names %q; want %q, the Release recorded", got.Release, want)
			}
			if reader := activeReleaseAnswer(t, srv); reader != got {
				t.Errorf("the writer answered %+v, and the reader asked afterwards answers %+v; the two must agree", got, reader)
			}
		})
	}
}

// TestSetActiveReleaseHoldsTheMarkerToTheWorkspace is the write the method exists
// to hold. Each row lays a link where the marker or its directory would be written
// that leads out of the workspace — to a place that exists, and to one not made
// yet — and the request must be refused with the write judgement's condition,
// naming the link, while the directory outside the workspace lists the same
// before and after: nothing created there, nothing overwritten.
func TestSetActiveReleaseHoldsTheMarkerToTheWorkspace(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name string
		// lay builds the link beneath root, leading into outside, and returns the
		// link the refusal must name.
		lay func(t *testing.T, root, outside string) string
	}{
		{
			name: "a .aggrega directory linked to a directory outside",
			lay: func(t *testing.T, root, outside string) string {
				plantDir(t, filepath.Join(outside, "aggdir"))
				plantLink(t, filepath.Join(outside, "aggdir"), filepath.Join(root, ".aggrega"))
				return filepath.Join(root, ".aggrega")
			},
		},
		{
			name: "a .aggrega directory linked to a directory outside not made yet",
			lay: func(t *testing.T, root, outside string) string {
				plantLink(t, filepath.Join(outside, "aggdir-not-yet"), filepath.Join(root, ".aggrega"))
				return filepath.Join(root, ".aggrega")
			},
		},
		{
			name: "a marker linked to an existing file outside",
			lay: func(t *testing.T, root, outside string) string {
				plantFile(t, filepath.Join(outside, "target.txt"), "kept as it is\n")
				plantDir(t, filepath.Join(root, ".aggrega"))
				plantLink(t, filepath.Join(outside, "target.txt"), filepath.Join(root, ".aggrega", "active-release"))
				return filepath.Join(root, ".aggrega", "active-release")
			},
		},
		{
			name: "a marker linked nowhere, its far end outside",
			lay: func(t *testing.T, root, outside string) string {
				plantDir(t, filepath.Join(root, ".aggrega"))
				plantLink(t, filepath.Join(outside, "not-yet.txt"), filepath.Join(root, ".aggrega", "active-release"))
				return filepath.Join(root, ".aggrega", "active-release")
			},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root, outside := t.TempDir(), t.TempDir()
			plantFile(t, filepath.Join(outside, "kept.txt"), "kept\n")
			writeRelease(t, root, "release.yaml", "release/only")
			link := row.lay(t, root, outside)
			before := treeListing(t, outside)
			srv := newLazyCascadeServer(t, root, nil, nil)

			resp := setActiveRelease(t, srv, map[string]any{"release": "release.yaml"})

			if resp.Error == nil {
				t.Fatalf("the write was admitted through %s, which leads out of the workspace: %s", link, resp.Result)
			}
			if resp.Error.Code != jsonRPCInvalidParams {
				t.Errorf("refused with code %d; want %d, the code the configuration's writes answer this refusal with",
					resp.Error.Code, jsonRPCInvalidParams)
			}
			for _, fragment := range []string{"E0320: ", "the link " + link + " leads to " + outside} {
				if !strings.Contains(resp.Error.Message, fragment) {
					t.Errorf("the refusal does not carry %q: %q", fragment, resp.Error.Message)
				}
			}
			if !strings.HasPrefix(resp.Error.Message, "E0320: ") {
				t.Errorf("the refusal does not lead with the condition's short code: %q", resp.Error.Message)
			}
			if after := treeListing(t, outside); !slices.Equal(before, after) {
				t.Errorf("the directory outside the workspace changed:\nbefore %v\nafter  %v", before, after)
			}
			if data, err := os.ReadFile(filepath.Join(outside, "target.txt")); err == nil && string(data) != "kept as it is\n" {
				t.Errorf("the file outside was overwritten: %q", data)
			}
		})
	}
}

// TestSetActiveReleaseRefusesAReleaseNamedOutsideTheWorkspace holds the value the
// request names to the contract every Release-naming parameter carries, before
// anything is written: none named, an absolute path, one climbing out of the
// workspace, and one leading out of it through a link are each refused as an
// inadmissible parameter quoting what was wrong, and no marker is written.
func TestSetActiveReleaseRefusesAReleaseNamedOutsideTheWorkspace(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name    string
		release func(outside string) any
		want    string
	}{
		{name: "none named", release: func(string) any { return nil }, want: "the request named no Release"},
		{
			name:    "an absolute path",
			release: func(outside string) any { return filepath.Join(outside, "release.yaml") },
			want:    "is an absolute path",
		},
		{name: "a path climbing out", release: func(string) any { return "../elsewhere/release.yaml" }, want: "resolves outside the workspace root"},
		{name: "a link leading out", release: func(string) any { return "linked-release.yaml" }, want: "leads outside the workspace root through a link"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root, outside := t.TempDir(), t.TempDir()
			writeRelease(t, root, "release.yaml", "release/only")
			outsideRelease := writeRelease(t, outside, "release.yaml", "release/outside")
			plantLink(t, outsideRelease, filepath.Join(root, "linked-release.yaml"))
			srv := newLazyCascadeServer(t, root, nil, nil)

			params := map[string]any{}
			if v := row.release(outside); v != nil {
				params["release"] = v
			}
			resp := setActiveRelease(t, srv, params)

			if resp.Error == nil {
				t.Fatalf("admitted: %s", resp.Result)
			}
			if resp.Error.Code != jsonRPCInvalidParams || !strings.Contains(resp.Error.Message, row.want) {
				t.Errorf("refused with %d %q; want %d carrying %q", resp.Error.Code, resp.Error.Message, jsonRPCInvalidParams, row.want)
			}
			if _, err := os.Lstat(filepath.Join(root, ".aggrega")); !os.IsNotExist(err) {
				t.Errorf("a refused request created %s (lstat: %v)", filepath.Join(root, ".aggrega"), err)
			}
		})
	}
}

// TestSetActiveReleaseAnswersWhatTheCascadeSettlesOn is the reason the answer is
// the reader's rather than an echo of the request: a configured authoring Release
// outranks the marker, so the marker is written as asked and the answer names the
// configured Release, which is what the workspace now settles on.
func TestSetActiveReleaseAnswersWhatTheCascadeSettlesOn(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configured := writeRelease(t, root, "configured.yaml", "release/configured")
	writeRelease(t, root, "marked.yaml", "release/marked")
	srv := newLazyCascadeServer(t, root, nil, nil)
	srv.wsConfig.Build.AuthoringRelease = "configured.yaml"

	got := decodeActiveReleaseAnswer(t, setActiveRelease(t, srv, map[string]any{"release": "marked.yaml"}))

	if marker := readMarker(t, filepath.Join(root, ".aggrega", "active-release")); marker != "marked.yaml\n" {
		t.Errorf("the marker reads %q; want %q", marker, "marked.yaml\n")
	}
	if got.Release != configured {
		t.Errorf("the answer names %q; want %q, the configured Release that outranks the marker", got.Release, configured)
	}
}
