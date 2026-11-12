// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pullops

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// TestLockfileIgnoresResolutionTier is the cross-machine determinism proof: two
// closures that pinned the SAME entity through DIFFERENT resolution tiers
// (tier-1-catalog on one machine, tier-3-pull on another) must write a
// byte-identical lockfile. The resolution tier is machine state and is not
// persisted; only the deterministic triple (identity, digest, provenance) is.
func TestLockfileIgnoresResolutionTier(t *testing.T) {
	mk := func(tier string) closure.Closure {
		return closure.Closure{Entities: []closure.ResolvedRef{{
			URI:             "hub.aggrega.ai/component/core/compute/api:1.0.0",
			ResolvedVersion: "1.0.0",
			Digest:          "sha256:abc",
			ResolutionPath:  tier,
			ReferencedFrom:  []string{"aggregate.spec.components.api.type"},
		}}}
	}

	write := func(cl closure.Closure) []byte {
		lf := buildLockfile("release-prod.yaml", cl, nil, nil, nil, nil, nil)
		dir := t.TempDir()
		path := filepath.Join(dir, "release-prod.lock")
		if err := lockfile.WriteLockfile(path, lf); err != nil {
			t.Fatalf("WriteLockfile: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read lockfile: %v", err)
		}
		// The sections that describe the pull rather than the closure are removed
		// through the one home for that tolerance — generated_at, which is
		// wall-clock, and the toolchain stamp — so the proof isolates the
		// resolution-tier field. Routing through the shared waiver rather than
		// zeroing fields here keeps this comparison and the offline/online parity
		// comparison waiving the same set.
		return lockfile.ParityWaived(data)
	}

	local := write(mk("tier-1-catalog"))
	pulled := write(mk("tier-3-pull"))
	if !bytes.Equal(local, pulled) {
		t.Fatalf("lockfile differs by resolution tier — not cross-machine deterministic:\n--- tier-1 ---\n%s\n--- tier-3 ---\n%s", local, pulled)
	}
	if strings.Contains(string(local), "resolution_path") {
		t.Fatalf("lockfile still carries resolution_path:\n%s", local)
	}
}
