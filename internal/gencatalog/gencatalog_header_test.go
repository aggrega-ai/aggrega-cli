// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package gencatalog

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
)

// TestGenCatalogHeaderNamesTheOneEditAnAuthorMayMake reads the first line of every
// entity a --from-embedded generation writes. It says the entity is generated from
// the plugin, names the command, and names the one edit an author may make — narrow
// the capability lists — and it no longer forbids every edit, since that would
// contradict the narrowing an author is allowed.
func TestGenCatalogHeaderNamesTheOneEditAnAuthorMayMake(t *testing.T) {
	t.Parallel()
	overlayDir := t.TempDir()
	outDir := t.TempDir()
	writeEmbeddedOverlays(t, overlayDir)
	if err := Run(context.Background(), Options{FromEmbedded: true, OutputDir: outDir, OverlayPath: overlayDir},
		&bytes.Buffer{}); err != nil {
		t.Fatalf("gen-catalog --from-embedded: %v", err)
	}

	uris := embedded.URIs()
	if len(uris) == 0 {
		t.Fatal("the binary embeds no guest, so the generation wrote nothing to read")
	}
	for _, uri := range uris {
		path := filepath.Join(outDir, filepath.FromSlash(uri), embeddedGuestVersion(t, uri), genCatalogPluginFilename)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		first, _, _ := strings.Cut(string(raw), "\n")
		for _, want := range []string{"Generated from the plugin", "aggrega plugin gen-catalog", "narrow its capability lists"} {
			if !strings.Contains(first, want) {
				t.Errorf("%s: the first line must say %q: %q", uri, want, first)
			}
		}
		if strings.Contains(string(raw), "DO NOT EDIT") {
			t.Errorf("%s: the entity still forbids every edit:\n%s", uri, first)
		}
	}
}
