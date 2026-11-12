// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package lintops

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// TestLintAdvisoryReadsTheGuestBesideTheLintedEntity drives the under-declaration
// advisory over a Plugin entity named by path, and reads which file the advisory's
// resolution looked in from what it prints.
//
// The entity names a plugin no binary embeds, so the only place its module can be
// read from is beside the entity, under the filename the resolver derives from the
// entity's plugin type and name. Each row places, or does not place, a file there
// and names the line the advisory has to print:
//
//   - with bytes beside the entity, the resolution finds them and the advisory goes
//     on to describe them. The bytes are not a module, so describing them fails, and
//     the advisory says so — which it can say only of bytes it read.
//   - with nothing beside the entity, the resolution names the derived filename and
//     the entity's own directory as the place the module belongs.
//
// A resolver that registers no directory beside the entity fails both rows: it
// never reads the planted bytes, and its refusal names neither the file nor the
// directory, only that the binary does not carry the module.
func TestLintAdvisoryReadsTheGuestBesideTheLintedEntity(t *testing.T) {
	t.Parallel()

	const reference = "pipeline-step/lint-beside-entity-probe"
	entity := []byte(`apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: lint-beside-entity-probe
  pluginType: pipeline-step
  version: 1.0.0
spec:
  abiVersion: v1beta1
`)
	leaf := pluginpath.PluginLeaf(reference)

	tests := []struct {
		name      string
		plant     bool
		mustHold  func(dir string) []string
		mustNotBe []string
	}{
		{
			name:  "a module beside the entity is the one read",
			plant: true,
			mustHold: func(string) []string {
				return []string{`skipped for "lint-beside-entity-probe" (its .wasm did not describe:`}
			},
			mustNotBe: []string{"not resolvable locally"},
		},
		{
			name:  "with nothing beside the entity, the refusal names the file and the directory",
			plant: false,
			mustHold: func(dir string) []string {
				return []string{"not resolvable locally", "named " + leaf, "beside its Plugin entity in " + dir}
			},
			mustNotBe: []string{"did not describe", "the binary does not carry its module"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			dir := filepath.Join(root, "guest")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			entityPath := filepath.Join(dir, "plugin.yaml")
			if err := os.WriteFile(entityPath, entity, 0o644); err != nil {
				t.Fatal(err)
			}
			if tc.plant {
				if err := os.WriteFile(filepath.Join(dir, leaf), []byte("not a WebAssembly module"), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			var out bytes.Buffer
			pluginUnderDeclarationAdvisory(context.Background(), entity, entityPath, root, &out)
			got := out.String()

			for _, want := range tc.mustHold(dir) {
				if !strings.Contains(got, want) {
					t.Errorf("the advisory printed %q, which does not contain %q", got, want)
				}
			}
			for _, unwanted := range tc.mustNotBe {
				if strings.Contains(got, unwanted) {
					t.Errorf("the advisory printed %q, which contains %q", got, unwanted)
				}
			}
		})
	}
}
