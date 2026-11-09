// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// catalogTransformAttachment names the governance plugin the fixtures below attach
// at a transform role: its artifact reference, and the entity reference the
// workspace configuration attaches.
const (
	catalogTransformArtifact = "governance/tag-injector"
	catalogTransformEntity   = catalogTransformArtifact + ":1.0.0"
)

// catalogTransformProofLabel is the node label the mutator-proof guest injects. The
// fixture's workload echoes every node label into a container variable, so the label
// in the rendered output shows the attached guest ran and its change was applied.
const catalogTransformProofLabel = "mutator-proof"

// catalogGovernanceAttachment names a governance plugin a fixture workspace attaches:
// its artifact reference (the entity's identity coordinate), the one export its
// entity declares, and the role it is attached at.
type catalogGovernanceAttachment struct {
	artifact string
	export   string
	role     string
}

// catalogTransformAttachmentWorkspace writes a workspace whose configuration attaches,
// at the label-transform role, a governance Plugin entity authored in the workspace's
// own catalog directory with its .wasm beside it under the file name the plugin
// cascade reads, over a one-component compose Release. configHead is written at the
// top of the configuration, so a caller states the workspace's registry posture.
// Returns the workspace root, the Release path, and the path of the module beside the
// entity.
func catalogTransformAttachmentWorkspace(t *testing.T, guest []byte, configHead string) (root, releasePath, modulePath string) {
	t.Helper()
	return catalogGovernanceAttachmentWorkspace(t, catalogGovernanceAttachment{
		artifact: catalogTransformArtifact, export: "mutate", role: "label-transform",
	}, guest, configHead)
}

// catalogGovernanceAttachmentWorkspace writes the workspace catalogTransformAttachmentWorkspace
// describes for any governance attachment: the entity authored in the workspace's own
// catalog directory at version 1.0.0, attached at the given role. A nil guest lays no
// module beside the entity, so no local tier but the binary's own copy can hold it.
// Returns the workspace root, the Release path, and the path the module beside the
// entity is (or would be) read from.
func catalogGovernanceAttachmentWorkspace(t *testing.T, att catalogGovernanceAttachment, guest []byte, configHead string) (root, releasePath, modulePath string) {
	t.Helper()
	root = t.TempDir()
	catalogDir := filepath.Join(root, "catalog")
	pluginType, name, _ := strings.Cut(att.artifact, "/")

	writeWorkspaceConfig(t, root, "workspace:\n  catalog_dirs:\n    - ./catalog\n"+configHead+
		"auto_pull: false\nplugins:\n  governance:\n    - uri: "+att.artifact+":1.0.0\n      role: "+att.role+"\n")
	writeFixture(t, filepath.Join(catalogDir, "component", "test", "svc", "1.0.0", "component.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: test/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values: {}
`)
	writeFixture(t, filepath.Join(catalogDir, "recipe", "compose", "svc", "1.0.0", "recipe.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  capability: compose
  implements: test/svc
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      main:
        image: busybox:1
        variables:
          NODE_LABELS: "{{ range $k, $v := .context.labels }}{{ $v }};{{ end }}"
`)
	pluginDir := filepath.Join(catalogDir, "plugin", pluginType, name, "1.0.0")
	writeFixture(t, filepath.Join(pluginDir, "plugin.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: `+name+`
  pluginType: `+pluginType+`
  version: "1.0.0"
spec:
  abiVersion: v1beta1
  exports:
    - name: `+att.export+`
`)
	modulePath = filepath.Join(pluginDir, pluginpath.PluginLeaf(att.artifact))
	if guest != nil {
		writeFixtureBytes(t, modulePath, guest)
	}
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: test/svc
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/svc:1.0.0: compose/svc:1.0.0
`)
	releasePath = filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        app: {}
`)
	return root, releasePath, modulePath
}

// requireCatalogTransformPin asserts the lockfile pins the attachment's artifact from
// the module beside its entity: the row's digest and version are that module's
// content digest, the row records no manifest digest because nothing came through a
// registry, and it is referenced from the attached entity.
func requireCatalogTransformPin(t *testing.T, lf *lockfile.Lockfile, guest []byte) {
	t.Helper()
	want := digest.ContentDigest(guest)
	for _, p := range lf.Plugins {
		if p.URI != catalogTransformArtifact {
			continue
		}
		if p.Digest != want || p.ResolvedVersion != want {
			t.Errorf("the pin is (%s, %s); want the content digest of the module beside the entity, %s, as both",
				p.ResolvedVersion, p.Digest, want)
		}
		if p.ManifestDigest != "" {
			t.Errorf("the pin records manifest digest %s; bytes read beside the entity came through no registry manifest", p.ManifestDigest)
		}
		if !slices.Contains(p.ReferencedFrom, catalogTransformEntity) {
			t.Errorf("the pin is referenced from %v; want the attached entity %q", p.ReferencedFrom, catalogTransformEntity)
		}
		return
	}
	t.Fatalf("the lockfile pins no artifact for %q; a transform-role attachment's bytes shape the output and are pinned "+
		"wherever its entity is authored. plugins=%+v", catalogTransformArtifact, lf.Plugins)
}

// renderedTreeHolds reports whether any file under dir contains needle, returning the
// tree's paths for the failure message when none does.
func renderedTreeHolds(t *testing.T, dir, needle string) (bool, string) {
	t.Helper()
	var paths []string
	found := false
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		paths = append(paths, path)
		found = found || strings.Contains(string(data), needle)
		return nil
	})
	if err != nil {
		t.Fatalf("walk the rendered tree %s: %v", dir, err)
	}
	return found, strings.Join(paths, "\n")
}

// appendWasmCustomSection returns module with one custom section appended, which a
// Wasm module may carry anywhere and which changes nothing the module does. It gives a
// test a second module whose content digest differs from the first.
func appendWasmCustomSection(module []byte) []byte {
	// Section id 0, contents size 3: a one-byte name "x" and a one-byte payload "y".
	return append(append([]byte(nil), module...), 0x00, 0x03, 0x01, 'x', 'y')
}

// TestPullPinsACatalogAuthoredTransformAttachmentFromItsLocalBytes drives the real
// pull and build over a governance plugin authored in a catalog directory and attached
// at a transform role, in an offline workspace.
//
// Its bytes shape the rendered output, so the pull pins them like every
// output-shaping plugin's — from the module beside the entity, the only place they
// are read from. The build over that lockfile passes its drift check, which
// reconstructs the pin by asking the same question, and the attached guest runs.
// Then the module beside the entity is replaced by one with different bytes: the
// build refuses with the lockfile's drift condition, which is what the pin is for. A
// pin the build did not reconstruct would pass that second build too, so the refusal
// is the positive control for the agreement above it.
func TestPullPinsACatalogAuthoredTransformAttachmentFromItsLocalBytes(t *testing.T) {
	guest, err := os.ReadFile(wasmtest.EnsureFixtureBuilt(t, "mutator-proof-go.wasm"))
	if err != nil {
		t.Fatalf("read the guest fixture: %v", err)
	}
	root, releasePath, modulePath := catalogTransformAttachmentWorkspace(t, guest, "offline: true\n")
	t.Chdir(root)
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})

	if err := pullRun(context.Background(), pullConfig{ws: ws, ReleasePath: releasePath}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load the written lockfile: %v", err)
	}
	requireCatalogTransformPin(t, lf, guest)

	if err := dispatchBuild(context.Background(), ws, releasePath, nil, false, io.Discard, io.Discard); err != nil {
		t.Fatalf("the build over the lockfile the pull wrote: %v", err)
	}
	if found, tree := renderedTreeHolds(t, filepath.Join(root, "dist"), catalogTransformProofLabel); !found {
		t.Errorf("the rendered output does not carry the label %q the attached guest injects:\n%s", catalogTransformProofLabel, tree)
	}

	writeFixtureBytes(t, modulePath, appendWasmCustomSection(guest))
	err = dispatchBuild(context.Background(), ws, releasePath, nil, false, io.Discard, io.Discard)
	if !errors.Is(err, lockfile.ErrLockfileDrift()) {
		t.Fatalf("a build after the module beside the entity changed must refuse with the lockfile's drift condition; got %v", err)
	}
	if !strings.Contains(err.Error(), catalogTransformArtifact) {
		t.Errorf("the drift refusal does not name %q: %v", catalogTransformArtifact, err)
	}
}
