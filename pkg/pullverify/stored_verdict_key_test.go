// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pullverify_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestTheStoredVerdictKeyMovesWithWhatTheVerdictReads pins the key a remembered
// verdict is held under to each input the verdict is decided by. The same question
// built twice keys alike, so a verdict can be remembered at all; and a change to
// any one input — the check, the offline posture, the bytes of a pinned trust root,
// the manifest, the kept signatures — keys differently, so a verdict reached for
// one question is never the answer to another. A pinned trust root that cannot be
// read yields no key, so nothing decided over it is remembered.
//
// The trust root here is never parsed: the key reads its bytes and nothing more.
func TestTheStoredVerdictKeyMovesWithWhatTheVerdictReads(t *testing.T) {
	t.Parallel()
	const host = "registry.example.com"
	rootPath := filepath.Join(t.TempDir(), "trusted_root.json")
	writeRoot := func(content string) {
		t.Helper()
		if err := os.WriteFile(rootPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write the trust root: %v", err)
		}
	}
	writeRoot("the first trust root")

	config := func() workspaceconfig.Config {
		return workspaceconfig.Config{Registries: []workspaceconfig.RegistryConfig{{
			Host:        host,
			Signing:     workspaceconfig.SigningConfig{Required: true},
			TrustedRoot: rootPath,
			TrustedIdentities: []workspaceconfig.TrustedIdentity{
				{Identity: "publisher@example.com", Issuer: "https://issuer.example.com"},
			},
		}}}
	}
	entity := pullverify.Entity{URI: host + "/docs/aggrega-docs", ManifestDigest: "sha256:0001", Host: host}
	kept := [][]byte{[]byte("a kept signature bundle")}

	key := func(cfg workspaceconfig.Config, e pullverify.Entity, bundles [][]byte) string {
		t.Helper()
		k, ok := pullverify.StoredVerdictKey(cfg, e, bundles, nil)
		if !ok {
			t.Fatal("no key was taken over a trust root that can be read")
		}
		return k
	}
	original := key(config(), entity, kept)

	if again := key(config(), entity, kept); again != original {
		t.Errorf("the same question built twice keys differently, so no verdict could be remembered:\n%s\n%s",
			original, again)
	}

	for _, c := range []struct {
		name    string
		cfg     workspaceconfig.Config
		entity  pullverify.Entity
		bundles [][]byte
	}{
		{"another trusted identity", func() workspaceconfig.Config {
			cfg := config()
			cfg.Registries[0].TrustedIdentities[0].Identity = "someone-else@example.com"
			return cfg
		}(), entity, kept},
		{"the registry made advisory", func() workspaceconfig.Config {
			cfg := config()
			cfg.Registries[0].Advisory = true
			return cfg
		}(), entity, kept},
		{"verification switched off everywhere", func() workspaceconfig.Config {
			cfg := config()
			cfg.SkipSignatureVerification = true
			return cfg
		}(), entity, kept},
		{"the same check read offline", func() workspaceconfig.Config {
			cfg := config()
			cfg.Offline = true
			return cfg
		}(), entity, kept},
		{"another manifest", config(), pullverify.Entity{URI: entity.URI, ManifestDigest: "sha256:0002", Host: host}, kept},
		{"one more kept signature", config(), entity, append([][]byte{[]byte("another kept signature bundle")}, kept...)},
	} {
		if key(c.cfg, c.entity, c.bundles) == original {
			t.Errorf("%s: keyed like the original question, so the original's verdict would answer it", c.name)
		}
	}

	// The trust root is keyed by what it holds, not by where it is: an edit at the
	// same path is a different anchor.
	writeRoot("the second trust root")
	if key(config(), entity, kept) == original {
		t.Error("a trust root edited in place keyed like the original, so a verdict reached under the old " +
			"anchor would answer under the new one")
	}

	if err := os.Remove(rootPath); err != nil {
		t.Fatalf("remove the trust root: %v", err)
	}
	if k, ok := pullverify.StoredVerdictKey(config(), entity, kept, nil); ok {
		t.Errorf("a trust root that cannot be read yielded the key %s, so a verdict over it could be remembered", k)
	}
}
