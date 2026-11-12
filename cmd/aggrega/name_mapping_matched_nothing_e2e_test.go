// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// The test in this file drives what an author meets whose Manifest writes a
// name-tier key that names nothing in the build, through the real pull and build.
// The commonest slip is a features.name key written as the name of the component
// an attachment sits on rather than as the attachment's own name; a
// components.name key with a typo is the same fault on the other section. Each
// halts naming the key, the Manifest and the name the author probably meant, and
// the same workspace keyed correctly builds and renders through the realization
// the entry names. It reuses the name-tier fixture and rewrites only its Manifest.

// nameMappingManifestYAML is the fixture's Manifest with the two name-tier keys
// given.
func nameMappingManifestYAML(componentKey, featureKey string) string {
	return `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: greeters-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        example/compute/greeter:1.0.0: compose/greeter-blue:1.0.0
      name:
        ` + componentKey + `: compose/greeter-green:1.0.0
    features:
      type:
        example/banner:1.0.0: compose/banner-plain:1.0.0
      name:
        ` + featureKey + `: compose/banner-loud:1.0.0
`
}

// TestANameMappingKeyThatNamesNothingHaltsTheBuild drives one row per section of
// the mapping, each against a control keyed correctly in the same workspace.
func TestANameMappingKeyThatNamesNothingHaltsTheBuild(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		componentKey string
		featureKey   string
		want         []string
	}{
		{
			name:         "a features.name key written as the component the attachment sits on",
			componentKey: "greeter-special",
			featureKey:   "greeter-special",
			want: []string{
				`.spec.mappings.features.name.greeter-special`,
				`Manifest greeters-manifest`,
				`"greeter-special"`,
				`attachments of that type in this build "banner, loud-banner"`,
				`did you mean "loud-banner"`,
			},
		},
		{
			name:         "a components.name key one edit from the instance",
			componentKey: "greeter-specail",
			featureKey:   "loud-banner",
			want: []string{
				`.spec.mappings.components.name.greeter-specail`,
				`Manifest greeters-manifest`,
				`"greeter-specail"`,
				`did you mean "greeter-special"`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			catalogDir, rel := nameTierFixture(t, root)
			pull := func() error {
				t.Helper()
				return pullRun(context.Background(), pullConfig{
					ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
					ReleasePath: rel,
					CatalogDirs: []string{catalogDir},
					DiagOut:     io.Discard,
					Out:         io.Discard,
					diag:        buildDiagOptions{quiet: true},
				})
			}
			build := func() error {
				t.Helper()
				return dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
					rel, []string{catalogDir}, false, io.Discard, io.Discard)
			}

			writeFixture(t, filepath.Join(root, "manifest.yaml"), nameMappingManifestYAML(tc.componentKey, tc.featureKey))
			if err := pull(); err != nil {
				t.Fatalf("pull: %v", err)
			}
			err := build()
			if err == nil {
				t.Fatal("a build whose Manifest writes a name-tier key naming nothing built")
			}
			// The details sit in the causes beneath the halt's head, so they are read
			// from the diagnostic as the command prints it rather than from the
			// error's own text, which is the head alone.
			printed := formatCompilerError(err)
			if !errors.Is(err, catalog.ErrNameMappingMatchedNothing()) {
				t.Fatalf("a build whose Manifest writes a name-tier key naming nothing must halt with that condition:\n%s", printed)
			}
			for _, w := range append([]string{"E0357"}, tc.want...) {
				if !strings.Contains(printed, w) {
					t.Errorf("the printed diagnostic does not carry %q:\n%s", w, printed)
				}
			}

			writeFixture(t, filepath.Join(root, "manifest.yaml"), nameMappingManifestYAML("greeter-special", "loud-banner"))
			if err := pull(); err != nil {
				t.Fatalf("pull over the corrected Manifest: %v", err)
			}
			if err := build(); err != nil {
				t.Fatalf("the same workspace keyed by the instance and the attachment must build:\n%s", formatCompilerError(err))
			}
			compose := string(mustReadFile(t, filepath.Join(root, "dist", "app", "compose", "docker-compose.yaml")))
			if !strings.Contains(compose, "aggrega.ai/recipe-uri: greeter-green:1.0.0") {
				t.Errorf("the corrected components.name entry did not reach the render:\n%s", compose)
			}
		})
	}
}
