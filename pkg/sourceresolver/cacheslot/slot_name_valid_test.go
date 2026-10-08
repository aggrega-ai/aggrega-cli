// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cacheslot_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegmenttest"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
)

// TestEveryNameASourceSlotWriteProducesIsOneEveryPlatformAccepts composes the
// slot of each source kind over values carrying what a file name cannot — a host
// with a port, a URL path with capitals, a colon and a space, a chart and a
// module subdirectory with a colon and a device name, a version with capitals and
// a colon — writes the slot's archive and metadata into it as an adapter does,
// and holds every directory and file name beneath the source-artifact subtree to
// the one validity predicate.
func TestEveryNameASourceSlotWriteProducesIsOneEveryPlatformAccepts(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	dg := "sha256:" + strings.Repeat("ab12", 16)
	for _, src := range []struct {
		kind, url, chart, moduleSubdir, version string
	}{
		{"helm-oci-chart", "oci://localhost:5000/Charts/MariaDB", "", "", "11.4.2"},
		{"helm-https-chart", "https://Charts.Example.com:8443/stable", "My:Chart", "", "1.0.0+Meta:x"},
		{"git-repository", "git://git.example.com/Infra/Modules.git", "", "Mods/A:b/con", "b149cd9719865f0afdb19df745c5458c2cd45cc8"},
		{"compose-https-fragment", "https://example.com/some path/frag:Ment.yaml", "", "", "1.0.0"},
		{"compose-https-fragment", "https://example.com/aux/nul.yaml", "", "", "v2."},
	} {
		slot, err := cacheslot.Slot(ws, ".aggrega/cache", src.kind, src.url, src.chart, src.moduleSubdir, src.version, dg)
		if err != nil {
			t.Fatalf("Slot(%s %q): %v", src.kind, src.url, err)
		}
		if err := cacheslot.WriteBlobAndMetadata(ws, ".aggrega/cache", slot, []byte("archive"),
			src.kind, src.url, src.version, dg, "application/gzip", "", []byte("{}")); err != nil {
			t.Fatalf("WriteBlobAndMetadata(%s %q): %v", src.kind, src.url, err)
		}
	}
	judged := pathsegmenttest.RequireTreeValid(t, cacheslot.SourceArtifactsRoot(ws, ".aggrega/cache"))
	t.Logf("judged %d names under the source-artifact cache", judged)
}
