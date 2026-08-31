// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// End-to-end proof that a component name a target cannot use is refused where it
// was written, and that the same name is untouched under a target that accepts
// it.
//
// The asymmetry is the whole subject. A component name of several capitals is
// legal, ordinary, and matches the authoring pattern exactly. Under a target
// whose alphabet admits no uppercase, each capital is escaped to nine characters
// and the derived name leaves that target's grammar entirely; under a target
// whose service names admit uppercase, the same name passes through as authored.
// Nothing about the name says which will happen, which is why the refusal has to
// carry the target's own reason and why the guidance names the cost.
//
// Both cases drive the real pull-then-build pipeline through the embedded guests,
// so what is asserted is the shipped behaviour rather than a projection of it.

// capitalsComponentName is six capitals — the shortest all-capital name whose
// escaped form leaves a lowercase-only target's bound, and therefore the smallest
// input that shows the whole effect.
const capitalsComponentName = "ABCDEF"

// helmCapitalsFixture authors a buildable workspace whose single component is
// named compName and whose recipe sources a local chart naming its ConfigMap
// after the release, so the derived name reaches the emitted bytes where a reader
// can see it.
func helmCapitalsFixture(t *testing.T, root, compName string) string {
	t.Helper()
	catalogRoot := filepath.Join(root, "catalog")
	writeCapitalsWorkspace(t, root, compName)
	writeFixture(t, filepath.Join(catalogRoot, "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values: {}
`)
	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "providers"))

	chartDir := filepath.Join(root, "charts", "echo")
	writeFixture(t, filepath.Join(chartDir, "Chart.yaml"), "apiVersion: v2\nname: echo\nversion: 0.1.0\n")
	writeFixture(t, filepath.Join(chartDir, "templates", "cm.yaml"), `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}
data:
  releaseName: {{ .Release.Name | quote }}
`)
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "server.yaml"), fmt.Sprintf(`apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server
  version: "1.0.0"
spec:
  capability: helm
  implements: server
  source: %q
`, chartDir))
	return filepath.Join(root, "release.yaml")
}

// composeCapitalsFixture authors the same workspace against the compose target,
// whose service names admit uppercase.
func composeCapitalsFixture(t *testing.T, root, compName string) string {
	t.Helper()
	catalogRoot := filepath.Join(root, "catalog")
	writeCapitalsWorkspace(t, root, compName)
	writeFixture(t, filepath.Join(catalogRoot, "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values: {}
`)
	seedEmbeddedPlugins(t, filepath.Join(catalogRoot, "providers"), "provider/compose")
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server
  version: "1.0.0"
spec:
  capability: compose
  implements: server
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: busybox:1
`)
	return filepath.Join(root, "release.yaml")
}

// writeCapitalsWorkspace writes the Aggregate, Release and Manifest shared by
// both fixtures, with the component authored under compName.
func writeCapitalsWorkspace(t *testing.T, root, compName string) {
	t.Helper()
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), fmt.Sprintf(`apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    %s:
      type: server
`, compName))
	writeFixture(t, filepath.Join(root, "release.yaml"), fmt.Sprintf(`apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: caps-rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        %s:
          infra:
            replicas: 1
`, compName))
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        server: server
`)
}

// buildCapitalsWorkspace primes the lockfile and runs the build, returning
// whatever the build reported.
func buildCapitalsWorkspace(t *testing.T, root, releasePath string) error {
	t.Helper()
	catalogDir := filepath.Join(root, "catalog")
	if err := pullRun(pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("prime lockfile via pull: %v", err)
	}
	return dispatchBuild(resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout)
}

// TestBuild_CapitalsComponentNameIsRefusedWhereItWasWritten asserts the build
// halts, and asserts WHERE the halt points.
//
// That the build fails is the weaker half. The half that matters is that the
// message names the Aggregate field the developer wrote and carries the target's
// own reason: an authored name is refused rather than shortened precisely because
// the person able to change it is the person who wrote it, and a halt naming a
// derived value would send them looking for a coordinate no document contains.
func TestBuild_CapitalsComponentNameIsRefusedWhereItWasWritten(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	err := buildCapitalsWorkspace(t, root, helmCapitalsFixture(t, root, capitalsComponentName))
	if err == nil {
		t.Fatal("a component name whose derived release name leaves the target's grammar must halt the build")
	}
	if !errors.Is(err, renderer.ErrComponentIdentityTargetRefuses()) {
		t.Fatalf("the halt must carry the target-refuses condition; got %v", err)
	}
	for _, want := range []string{
		"spec.components." + capitalsComponentName, // the field the developer wrote
		"release name",         // the target's own name for what refused
		"rename the component", // the remedy
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the halt does not carry %q; got %v", want, err)
		}
	}

	// Nothing was written. A halt that emitted output first would have put a name
	// the target refuses on disk, which is what the refusal exists to prevent.
	if entries, statErr := os.ReadDir(filepath.Join(root, "dist")); statErr == nil && len(entries) > 0 {
		t.Errorf("the refused build emitted %d entries under dist/; it must emit none", len(entries))
	}
}

// TestBuild_TheSameCapitalsNameIsLegalUnderAnotherTarget is the other half of the
// asymmetry, and it is what makes the refusal above a property of the target
// rather than of the name.
//
// The same six capitals reach the compose target's service map unchanged. Without
// this case a reader could reasonably conclude that capitals are refused, which is
// both wrong and the opposite of what the guidance says.
func TestBuild_TheSameCapitalsNameIsLegalUnderAnotherTarget(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	if err := buildCapitalsWorkspace(t, root, composeCapitalsFixture(t, root, capitalsComponentName)); err != nil {
		t.Fatalf("the same name must build unchanged under a target that accepts it: %v", err)
	}

	rendered := readTree(t, filepath.Join(root, "dist"))
	joined := strings.Join(mapValues(rendered), "\n")
	if !strings.Contains(joined, "  "+capitalsComponentName+":") {
		t.Errorf("the compose output does not carry the authored name as its service key; got:\n%s", joined)
	}
	if !strings.Contains(joined, "aggrega.ai/component-id: "+capitalsComponentName) {
		t.Errorf("the compose output does not stamp the authored name as the component identity; got:\n%s", joined)
	}
}

// TestBuild_AnOrdinaryNameStillBuildsThroughTheSameTarget is the positive control
// the two cases above need.
//
// Without it a refusal that fired on every name would satisfy the first case and
// look like correct behaviour, and the second case — which runs against a
// different target — would not notice.
func TestBuild_AnOrdinaryNameStillBuildsThroughTheSameTarget(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	if err := buildCapitalsWorkspace(t, root, helmCapitalsFixture(t, root, "webapp")); err != nil {
		t.Fatalf("an ordinary lowercase component name must still build through this target: %v", err)
	}

	rendered := readTree(t, filepath.Join(root, "dist"))
	joined := strings.Join(mapValues(rendered), "\n")
	if !strings.Contains(joined, `releaseName: "webapp"`) {
		t.Errorf("the emitted release name is not the authored identity; got:\n%s", joined)
	}
}
