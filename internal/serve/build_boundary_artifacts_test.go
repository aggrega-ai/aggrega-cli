// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// The daemon's build reaches the refusal of a Recipe declaring files for its
// boundary's shared output on a provider that does not accept them exactly as the
// command line's does: the compose provider declares no such claim, so the build
// refuses before anything renders, naming the provider and the node.

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

// TestTheDaemonBuildRefusesBoundaryArtifactsOnAProviderThatDoesNotAcceptThem
// drives a copy of the compose fixture whose Recipe declares a boundary block
// through the daemon's build.
func TestTheDaemonBuildRefusesBoundaryArtifactsOnAProviderThatDoesNotAcceptThem(t *testing.T) {
	src, err := filepath.Abs(filepath.Join("..", "compiler", "testdata", "compose-boundary-artifacts"))
	if err != nil {
		t.Fatalf("locate the fixture workspace: %v", err)
	}
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatalf("copy the fixture workspace: %v", err)
	}
	config := "workspace:\n  catalog_dirs:\n    - catalog\n" + testOwnedRegistryConfig(t)
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(config), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}

	sess := newTestSession(t, root, bytes.NewReader(nil), &bytes.Buffer{})
	srv, err := sess.workspaceFor(root)
	if err != nil {
		t.Fatalf("open the workspace at %s: %v", root, err)
	}
	resp, pushed := driveBuildWithRawParams(t, srv, `{"release":"release.yaml","autoPull":true}`)
	if resp.Error == nil {
		t.Fatalf("the daemon built a compose Recipe declaring files for its boundary's shared output\nwire:\n%s", pushed)
	}
	code, _ := cerrors.CodeOf(renderer.ErrUnsupportedBoundaryArtifacts())
	shortCode, _ := cerrors.ShortCodeOf(renderer.ErrUnsupportedBoundaryArtifacts())
	for _, want := range []string{`"code":"` + code + `"`, `"shortCode":"` + shortCode + `"`} {
		if !strings.Contains(pushed, want) {
			t.Errorf("the refusal does not carry the condition (%s):\n%s", want, pushed)
		}
	}
	for _, want := range []string{
		renderer.ErrUnsupportedBoundaryArtifacts().Error() + ` ("compose"`, `node "app"`,
	} {
		if !strings.Contains(resp.Error.Message, want) {
			t.Errorf("the refusal does not carry %q:\n%s", want, resp.Error.Message)
		}
	}
}
