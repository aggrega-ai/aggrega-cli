// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// The daemon's build reaches the halt on an omits entry that left nothing out
// exactly as the command line's does: the Recipe's entry, made active by the
// Release, names a controller its chart does not render, and the build refuses,
// naming the node, the document, the entry's position and what it resolved to.

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

// TestTheDaemonBuildRefusesAnOmitsEntryThatLeftNothingOut drives a copy of the
// end-to-end omits fixture, whose configuration names its catalog directory and
// records the pins of its local chart sources at pull, through the daemon's build.
func TestTheDaemonBuildRefusesAnOmitsEntryThatLeftNothingOut(t *testing.T) {
	src, err := filepath.Abs(filepath.Join("..", "compiler", "testdata", "helm-omits"))
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

	sess := newTestSession(t, root, bytes.NewReader(nil), &bytes.Buffer{})
	srv, err := sess.workspaceFor(root)
	if err != nil {
		t.Fatalf("open the workspace at %s: %v", root, err)
	}
	resp, pushed := driveBuildWithRawParams(t, srv, `{"release":"release-stale.yaml","autoPull":true}`)
	if resp.Error == nil {
		t.Fatalf("the daemon built a Recipe whose active omits entry names nothing its chart renders\nwire:\n%s", pushed)
	}
	code, _ := cerrors.CodeOf(renderer.ErrOmitMatchedNothing())
	shortCode, _ := cerrors.ShortCodeOf(renderer.ErrOmitMatchedNothing())
	for _, want := range []string{`"code":"` + code + `"`, `"shortCode":"` + shortCode + `"`} {
		if !strings.Contains(pushed, want) {
			t.Errorf("the refusal does not carry the condition (%s):\n%s", want, pushed)
		}
	}
	for _, want := range []string{
		renderer.ErrOmitMatchedNothing().Error(),
		`node "app"`, `"omits-app" omits[0]`, `kind "Deployment"`, `name "app-missing"`,
	} {
		if !strings.Contains(resp.Error.Message, want) {
			t.Errorf("the refusal does not carry %q:\n%s", want, resp.Error.Message)
		}
	}
}
