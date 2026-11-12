// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
)

// Provider-plugin seeding for build and render fixtures.
//
// A build or pull over a fixture workspace resolves each Recipe's capability
// binding to the Plugin entity serving it, and the binary's own embedded
// provider entities win that resolution over a same-coordinate entity a fixture
// catalog declares. The lockfile walk then pins the entity the resolution
// selected, looking it up by coordinate AND version: an entity found in a local
// catalog directory is left out of the lockfile and resolves at build time from
// that directory, while one found nowhere locally is fetched from the configured
// registry.
//
// A fixture that declares a core provider under its own version therefore
// resolves to the embedded entity's version and then reaches for a registry to
// pin it, because no local directory offers that version. Seeding the embedded
// entity itself into the fixture catalog closes that gap: the version matches
// the resolution by construction, the local directory serves it, and the
// workspace builds with no registry access at all.
//
// The seeded bytes are the embedded entity verbatim rather than a hand-written
// copy. The version they carry moves whenever the guests are regenerated, so a
// literal written here would be a second home for a value that changes and would
// go stale silently; taking the bytes from the set the resolver itself reads
// means the seed cannot disagree with the resolution.

// embeddedPluginEntityYAML returns the generated Plugin entity the binary embeds
// for uri (a bare plugin identity such as "provider/helm"). It fails the test
// when the binary carries no entity for that identity, which is the case for a
// binary built with the embedded artifacts gated out.
func embeddedPluginEntityYAML(t *testing.T, uri string) []byte {
	t.Helper()
	raw, ok := embedded.LookupEntity(uri)
	if !ok {
		t.Fatalf("the binary embeds no plugin entity for %q; available: %v", uri, embedded.EntityURIs())
	}
	return raw
}

// seedEmbeddedPlugins writes the embedded Plugin entity for each supplied bare
// identity into dir, one file per identity, so a fixture catalog rooted at or
// above dir serves those entities from the local catalog tier. Returns dir, so a
// caller can inline it into a catalog search stack.
//
// Each file is named after the identity's plugin name with the socket segment
// flattened, keeping one entity per file the way a catalog directory does.
func seedEmbeddedPlugins(t *testing.T, dir string, uris ...string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	for _, uri := range uris {
		raw := embeddedPluginEntityYAML(t, uri)
		name := strings.ReplaceAll(uri, "/", "-") + ".yaml"
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return dir
}

// seedEmbeddedProviderOverlay writes the embedded Plugin entities for uris into a
// fresh temporary directory and returns it, for prepending to a catalog search
// stack whose lower-precedence directories declare the same providers under a
// different version. The overlay leaves the shared fixture catalog untouched.
func seedEmbeddedProviderOverlay(t *testing.T, uris ...string) string {
	t.Helper()
	return seedEmbeddedPlugins(t, filepath.Join(t.TempDir(), "providers"), uris...)
}
