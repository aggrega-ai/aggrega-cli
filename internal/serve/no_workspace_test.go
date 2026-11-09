// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestARequestsWorkspaceRootThatIsNotAFolderIsRefusedInThisSurfacesWords is the
// regression row for the language server's own refusal of a root a request names:
// whether the root can be one is the shared resolution's judgement, and the
// sentence stays this surface's, naming the parameter the request sent.
//
// 🔴 PLANTED DEFECT: have workspaceconfig.NamedRootDir accept every root, and both
// rows fail — the request is answered over a folder that does not exist, or over
// a file.
func TestARequestsWorkspaceRootThatIsNotAFolderIsRefusedInThisSurfacesWords(t *testing.T) {
	t.Parallel()
	sess := newTestSession(t, t.TempDir(), bytes.NewReader(nil), &bytes.Buffer{})

	missing := filepath.Join(t.TempDir(), "no-such-folder")
	_, err := sess.workspaceFor(missing)
	if err == nil {
		t.Fatalf("a request naming %s as its workspace root was answered", missing)
	}
	if want := "the `workspaceRoot` parameter names " + missing + ", which cannot be read: "; !strings.HasPrefix(err.Error(), want) {
		t.Errorf("the refusal reads %q; want it to open %q", err, want)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the refusal %q does not carry the reason the folder could not be read", err)
	}

	file := filepath.Join(t.TempDir(), "a-file")
	if werr := os.WriteFile(file, []byte("x"), 0o644); werr != nil {
		t.Fatalf("write %s: %v", file, werr)
	}
	_, err = sess.workspaceFor(file)
	if want := "the `workspaceRoot` parameter names " + file + ", which is not a directory: a workspace root is a tree"; err == nil || err.Error() != want {
		t.Errorf("the refusal reads %v; want %q", err, want)
	}
}

// TestTheDependencyResolutionsRefusalCarriesTheLocalCatalogNote is the language
// server's row of the note's parity: a dependency resolution that fails on a
// reference that resolved to nothing answers with the workspace's own account of
// where local entities are read from, the note the command line's and the
// assistant's refusals carry, composed by the one resolution they all run.
//
// 🔴 PLANTED DEFECT: restore AnnotateResolutionFailure as a hook only the command
// line supplies, and this row fails.
//
// Not parallel: the resolution is run from the workspace it resolves, as a
// request's is.
func TestTheDependencyResolutionsRefusalCarriesTheLocalCatalogNote(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{
		workspaceconfig.ConfigFilename: "workspace: {}\noffline: true\n",
		"aggregate.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: app
  version: "1.0.0"
spec:
  components:
    web:
      type: example/compute/missing:1.0.0
`,
		"manifest.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: app-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        example/compute/missing:1.0.0: compose/missing:1.0.0
`,
		"release.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: app-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: app
  manifest: app-manifest
  boundaries:
    app:
      directory: app
      components:
        web: {}
`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	t.Chdir(root)
	srv := newFillCapableServer(t, root)
	note := strings.TrimSpace(srv.resolvedWorkspace.LocalCatalogAdviceNote(filepath.Join(root, "release.yaml")))
	if note == "" {
		t.Fatal("precondition: the workspace composes no note for this Release, so the row would compare nothing")
	}

	_, errEnvelope := srv.handleResolveDependencies(context.Background(),
		resolveDependenciesParams{Release: "release.yaml"}, srv.progressFor(context.Background()))
	if errEnvelope == nil {
		t.Fatal("the dependency resolution of a Release binding a Recipe no tier holds succeeded")
	}
	if !strings.Contains(errEnvelope.Message, note) {
		t.Errorf("the refusal does not carry the workspace's note\nrefusal: %s\nnote:    %s", errEnvelope.Message, note)
	}
}
