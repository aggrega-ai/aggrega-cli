// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package recipepin_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// TestReadLockRefusesAnIgnorePluginRecordingAVersionOrDigest holds the ignore
// section's plugin block to its one shape: the plugin's name and nothing that
// names a release of it. A lock recording the plugin's reference or the digest of
// its entity — the block an earlier release wrote — is refused when it is read,
// naming the key, rather than decoded with the key dropped; so is a block with no
// name, or an empty one. The first row is the positive control.
func TestReadLockRefusesAnIgnorePluginRecordingAVersionOrDigest(t *testing.T) {
	t.Parallel()
	const head = "version: 1\nlocalSourceDigests:\n  - path: module/main.tf\n" +
		"    digest: sha256:bf8259b1a977876fea2a11901d8cef3b5a16c84ba0694d5f72c1a940cd355b4a\nignore:\n  plugin:\n"
	const tail = "    patterns:\n      - .terraform/\n  rules:\n    - pattern: .terraform/\n      within: module\n      from: plugin\n"
	const digest = "sha256:fc474a1ec156253e0303895a5c9139ca9bc2a6b947338e4a392078a3671255ef"
	cases := []struct {
		name  string
		block string
		keys  []string // empty: the row is the positive control
	}{
		{name: "the plugin named alone", block: "    name: provider/terraform\n"},
		{
			name:  "the name and the plugin's reference",
			block: "    name: provider/terraform\n    ref: provider/terraform:1.0.0\n",
			keys:  []string{"ignore.plugin.ref"},
		},
		{
			name:  "the name and the digest of the plugin's entity",
			block: "    name: provider/terraform\n    digest: " + digest + "\n",
			keys:  []string{"ignore.plugin.digest"},
		},
		{
			name:  "the reference and the digest an earlier release wrote, and no name",
			block: "    ref: provider/terraform:0.8.7-2-g2c6204be\n    digest: " + digest + "\n",
			keys:  []string{"ignore.plugin.ref", "ignore.plugin.digest", "ignore.plugin.name"},
		},
		{
			name:  "an empty name",
			block: "    name: \"\"\n",
			keys:  []string{"ignore.plugin.name"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			const path = "catalog/recipe/terraform/demo/1.0.0/entity.lock"
			lock, err := recipepin.DecodeLock([]byte(head+tc.block+tail), path)
			if len(tc.keys) == 0 {
				if err != nil {
					t.Fatalf("the reader refused a plugin block naming the plugin alone: %v", err)
				}
				if lock.Ignore == nil || lock.Ignore.Plugin == nil || lock.Ignore.Plugin.Name != "provider/terraform" {
					t.Fatalf("the plugin block read back as %+v; want the name provider/terraform", lock.Ignore)
				}
				return
			}
			if err == nil {
				t.Fatalf("the reader admitted a plugin block with %s: %+v", tc.name, lock.Ignore.Plugin)
			}
			if !errors.Is(err, envelope.ErrEntityEnvelope()) {
				t.Errorf("refused, but not as a schema violation: %v", err)
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("the refusal does not name the lock file %s: %v", path, err)
			}
			for _, key := range tc.keys {
				if !strings.Contains(err.Error(), key) {
					t.Errorf("the refusal does not name the key %s: %v", key, err)
				}
			}
		})
	}
}
