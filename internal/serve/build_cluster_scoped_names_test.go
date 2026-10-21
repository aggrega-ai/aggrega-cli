// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// The daemon's build reaches the halt over a cluster-scoped name exactly as the
// command line's does: one boundary, two components of one chart placed in two
// namespaces, each writing one ClusterRole and the one CustomResourceDefinition
// under one name. The cluster holds each as one object whatever namespace it is
// placed in, so the build refuses, naming each name in the key space of its kind
// alone.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// clusterScopedNamesWorkspace copies the end-to-end fixture of cluster-scoped names
// into a fresh workspace and writes a configuration naming its catalog directory
// and recording the pins of its local chart sources at pull, so a build that fills
// its own lockfile renders it. The copy is what every request reads and writes, so
// the tracked fixture is never driven in place.
func clusterScopedNamesWorkspace(t *testing.T) string {
	t.Helper()
	src, err := filepath.Abs(filepath.Join("..", "compiler", "testdata", "helm-cluster-scoped-names"))
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

// TestTheDaemonBuildRefusesOneClusterScopedNameFromTwoNamespaces is the refusal on
// this surface, asserted against the condition the halt carries.
func TestTheDaemonBuildRefusesOneClusterScopedNameFromTwoNamespaces(t *testing.T) {
	root := clusterScopedNamesWorkspace(t)
	sess := newTestSession(t, root, bytes.NewReader(nil), &bytes.Buffer{})
	srv, err := sess.workspaceFor(root)
	if err != nil {
		t.Fatalf("open the workspace at %s: %v", root, err)
	}

	resp, pushed := driveBuildWithRawParams(t, srv, `{"release":"release-two-namespaces.yaml","autoPull":true}`)
	if resp.Error == nil {
		t.Fatalf("the daemon built two components writing one ClusterRole and one CustomResourceDefinition into "+
			"one output\nwire:\n%s", pushed)
	}
	code, _ := cerrors.CodeOf(renderer.ErrDerivedKeyCollision())
	shortCode, _ := cerrors.ShortCodeOf(renderer.ErrDerivedKeyCollision())
	for _, want := range []string{`"code":"` + code + `"`, `"shortCode":"` + shortCode + `"`} {
		if !strings.Contains(pushed, want) {
			t.Errorf("the refusal does not carry the derived-name condition (%s):\n%s", want, pushed)
		}
	}
	for _, want := range []string{
		`"shared-reader"`, `key space "resource-names:ClusterRole"`,
		`"widgets.example.com"`, `key space "resource-names:CustomResourceDefinition"`,
		`derived by component "store"`, `derived by component "edge"`,
	} {
		if !strings.Contains(resp.Error.Message, want) {
			t.Errorf("the refusal does not carry %q:\n%s", want, resp.Error.Message)
		}
	}
	if strings.Contains(resp.Error.Message, `"resource-names:ClusterRole--`) {
		t.Errorf("the refusal names a ClusterRole space carrying a namespace:\n%s", resp.Error.Message)
	}
}
