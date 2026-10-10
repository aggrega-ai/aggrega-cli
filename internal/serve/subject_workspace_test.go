// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// TestAnAbsoluteReleaseIsAnsweredUnderTheRequestsOwnRoot is the regression row
// for the editor's server: every request it answers carries an absolute root,
// so the workspace is the one that root names, and a Release is named relative
// to it. A request naming a Release by an absolute path is refused saying so,
// whether the root it carries is a folder holding no workspace or the Release's
// own workspace — the same answer in both — and nothing is written beside the
// Release.
//
// The workspace an editor's request is answered in is never found again from a
// file it names, so neither row may be answered from the Release's workspace.
func TestAnAbsoluteReleaseIsAnsweredUnderTheRequestsOwnRoot(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		root func(t *testing.T, releaseRoot string) string
	}{
		{
			name: "the root is a folder holding no workspace",
			root: func(t *testing.T, _ string) string { return t.TempDir() },
		},
		{
			name: "the root is the Release's own workspace",
			root: func(_ *testing.T, releaseRoot string) string { return releaseRoot },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			releaseRoot := releaseCatalogWorkspace(t, "")
			release := filepath.Join(releaseRoot, "release-own.yaml")
			root := tc.root(t, releaseRoot)

			sess := newTestSession(t, root, bytes.NewReader(nil), &bytes.Buffer{})
			srv, err := sess.workspaceFor(root)
			if err != nil {
				t.Fatalf("open the workspace at %s: %v", root, err)
			}
			var in, out bytes.Buffer
			in.Write(framedRequest(1, "aggrega/resolveDependencies", resolveDependenciesParams{Release: release}))
			resp, _ := runOneRequestPastPushes(t, srv, &in, &out)
			if resp.Error == nil {
				t.Fatalf("resolving the dependencies of %s under the root %s was answered: %s", release, root, resp.Result)
			}
			if resp.Error.Code != jsonRPCInvalidParams || !strings.Contains(resp.Error.Message, "is an absolute path") {
				t.Errorf("the request was refused with %d %q, want the refusal of a Release named by an absolute path",
					resp.Error.Code, resp.Error.Message)
			}
			if _, err := os.Stat(lockfile.PathFor(release)); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("a lockfile was written beside the Release (stat: %v)", err)
			}
		})
	}
}
