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
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
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

// longLowercaseComponentName is sixty-four lowercase letters, and every one of
// them is a character the bounded target's alphabet admits.
//
// That isolation is the point: it leaves LENGTH as the only rule the derived name
// breaches, so the refusal it draws is attributable to the bound and to nothing
// else. The capitals name above breaches the alphabet as well, and a case failing
// on the alphabet would read exactly like a case failing on the length.
var longLowercaseComponentName = strings.Repeat("a", 64)

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

// TestBuild_ALongComponentNameIsRefusedWhereTheTargetDeclaresABound covers the
// condition where THE TARGET DECLARES A BOUND: an authored identity longer than
// that bound is refused, and the refusal carries the target's own budget.
//
// The other condition — a target declaring NO bound, under which an identity of
// this same length builds clean — is covered at
// internal/compiler/component_identity_refusal_build_test.go, whose corpus renders
// through such a target. Neither case is readable without the other: this one alone
// reads as a length rule the compiler holds, which is precisely what it is not, and
// that one alone reads as length never being checked at all.
//
// The bound reported is the SMALLEST across the kinds that refused, because one
// identity has to satisfy every kind at once. This target derives two names from a
// component identity and both refuse a name this long — a release name at
// fifty-three characters and the component-id label value at sixty-three — so the
// release name is the binding one and is what the message names. Asserting a
// figure the target owns is deliberate rather than a duplicated constant: the
// number lives unexported beside the derivation in the guest's own package, out of
// reach from here by construction, and an independently written expectation is what
// makes this a check on the target's answer instead of a restatement of it.
//
// The budget in the remedy is the assertion that separates a LENGTH refusal from
// the grammar refusal above. A grammar refusal can offer no budget, because no
// shortening repairs it; only a length refusal knows how short is short enough.
func TestBuild_ALongComponentNameIsRefusedWhereTheTargetDeclaresABound(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	err := buildCapitalsWorkspace(t, root, helmCapitalsFixture(t, root, longLowercaseComponentName))
	if err == nil {
		t.Fatal("an authored identity longer than the target's declared bound must halt the build")
	}
	if !errors.Is(err, renderer.ErrComponentIdentityTargetRefuses()) {
		t.Fatalf("the halt must carry the target-refuses condition; got %v", err)
	}

	// The category routes the reader to the surface at fault, so it is asserted
	// rather than left to the message text: a name a person wrote is a fault in
	// authored content, and reporting it as a translation fault would send them to
	// the plugin's maintainer for something only they can change.
	var compilerErr *cerrors.CompilerError
	if !errors.As(err, &compilerErr) {
		t.Fatalf("the halt must carry a category; got %T", err)
	}
	if compilerErr.Category != cerrors.CatalogError {
		t.Errorf("category = %v, want %v", compilerErr.Category, cerrors.CatalogError)
	}

	for _, want := range []string{
		"spec.components." + longLowercaseComponentName, // the field the developer wrote
		"release name", // the kind whose bound binds, being the smaller
		"a release name is at most 53 characters", // the target's own reason, carried through verbatim
		"choose a name of at most 53 characters",  // the budget, which only a length refusal can offer
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
