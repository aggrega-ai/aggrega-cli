// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// The daemon's build reaches the halt over a Kubernetes version the chart cannot be
// rendered for exactly as the command line's does: a boundary names
// aggrega.ai/kubernetes-version "one.two", which the version parser refuses, so
// the build refuses under the value-not-representable condition rather than
// rendering the chart for a version nobody named.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// kubernetesVersionWorkspace copies the end-to-end fixture of Kubernetes versions
// into a fresh workspace and writes a configuration naming its catalog directory
// and recording the pins of its local chart source at pull, so a build that fills
// its own lockfile renders it. The copy is what every request reads and writes, so
// the tracked fixture is never driven in place.
func kubernetesVersionWorkspace(t *testing.T) string {
	t.Helper()
	src, err := filepath.Abs(filepath.Join("..", "compiler", "testdata", "helm-kubernetes-version"))
	if err != nil {
		t.Fatalf("locate the fixture workspace: %v", err)
	}
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatalf("copy the fixture workspace: %v", err)
	}
	config := "workspace:\n  catalog_dirs:\n    - catalog\npin_sources_at_pull: true\n" + testOwnedRegistryConfig(t)
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(config), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
	return root
}

// TestTheDaemonBuildRefusesAKubernetesVersionTheParserRefuses is the refusal on
// this surface, asserted against the condition the halt carries.
func TestTheDaemonBuildRefusesAKubernetesVersionTheParserRefuses(t *testing.T) {
	root := kubernetesVersionWorkspace(t)
	sess := newTestSession(t, root, bytes.NewReader(nil), &bytes.Buffer{})
	srv, err := sess.workspaceFor(root)
	if err != nil {
		t.Fatalf("open the workspace at %s: %v", root, err)
	}

	resp, pushed := driveBuildWithRawParams(t, srv, `{"release":"release-malformed.yaml","autoPull":true}`)
	if resp.Error == nil {
		t.Fatalf("the daemon rendered a chart for the unparsable Kubernetes version \"one.two\"\nwire:\n%s", pushed)
	}
	code, _ := cerrors.CodeOf(plugincondition.ErrPluginValueNotRepresentable())
	shortCode, _ := cerrors.ShortCodeOf(plugincondition.ErrPluginValueNotRepresentable())
	for _, want := range []string{`"code":"` + code + `"`, `"shortCode":"` + shortCode + `"`} {
		if !strings.Contains(pushed, want) {
			t.Errorf("the refusal does not carry the value-not-representable condition (%s):\n%s", want, pushed)
		}
	}
	for _, want := range []string{
		`node "plain" in boundary "broken"`,
		`annotation aggrega.ai/kubernetes-version: "one.two" is not a Kubernetes version a chart can be rendered for`,
	} {
		if !strings.Contains(resp.Error.Message, want) {
			t.Errorf("the refusal does not carry %q:\n%s", want, resp.Error.Message)
		}
	}
}
