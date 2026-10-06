// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestAWorkspaceWhoseConfigurationLeadsOutIsRefusedOnEveryRequest is the language
// server's half of the one reading every surface gives a workspace configuration
// file that is a link leading out of the workspace: it is neither read nor used.
// The server resolves a workspace a request names through the same resolution the
// command line does, so every request naming such a workspace — a listing, the
// default Release, the effective configuration, the marker write — is refused with
// the read hold's sentence naming the link, rather than answered from a
// configuration read through it. The control is the same workspace with its
// configuration file inside, answered.
func TestAWorkspaceWhoseConfigurationLeadsOutIsRefusedOnEveryRequest(t *testing.T) {
	t.Parallel()

	methods := []struct {
		method string
		params map[string]any
	}{
		{"aggrega/catalogIndex", map[string]any{}},
		{"aggrega/releaseCandidates", map[string]any{}},
		{"aggrega/activeRelease", map[string]any{}},
		{"aggrega/effectiveConfiguration", map[string]any{}},
		{"aggrega/setActiveRelease", map[string]any{"release": "release.yaml"}},
	}
	for _, linked := range []bool{false, true} {
		root, outside := t.TempDir(), t.TempDir()
		writeRelease(t, root, "release.yaml", "release/only")
		link := filepath.Join(root, workspaceconfig.ConfigFilename)
		if linked {
			plantFile(t, filepath.Join(outside, "config.yaml"), "workspace: {}\n")
			plantLink(t, filepath.Join(outside, "config.yaml"), link)
		} else {
			plantFile(t, link, "workspace: {}\n")
		}
		sess := newTestSession(t, t.TempDir(), nil, nil)

		for _, m := range methods {
			var in, out bytes.Buffer
			in.Write(framedRequest(1, m.method, m.params))
			resp := runOneRequestIn(t, sess, root, &in, &out)

			if !linked {
				if resp.Error != nil {
					t.Errorf("control, %s: refused over a configuration file inside the workspace: %+v", m.method, resp.Error)
				}
				continue
			}
			if resp.Error == nil {
				t.Errorf("%s answered over a configuration file read through a link leading out: %s", m.method, resp.Result)
				continue
			}
			for _, fragment := range []string{
				"a file this operation reads leads through a link out of the directory it must stay inside",
				"the link " + link + " leads to " + outside,
			} {
				if !strings.Contains(resp.Error.Message, fragment) {
					t.Errorf("%s: the refusal does not carry %q: %q", m.method, fragment, resp.Error.Message)
				}
			}
		}
		if linked {
			if _, err := os.Lstat(filepath.Join(root, ".aggrega")); !os.IsNotExist(err) {
				t.Errorf("a refused workspace had its marker directory created (lstat: %v)", err)
			}
		}
	}
}
