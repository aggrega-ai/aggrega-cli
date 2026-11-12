// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// A withdrawal marker on a plugin attached at a verdict role is recorded in the
// plugin trust record beside the Release, not in the lockfile, because such a
// plugin shapes no rendered byte and is not pinned there. So a closure listing that
// reads the lockfile alone says nothing about a withdrawn gate plugin while it names
// a withdrawn pinned reference — and its silence reads as "nothing is withdrawn".
// These cases hold the listing to the one report over both stores.

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
)

// TestClosureListingReportsAWithdrawnTrustedPlugin drives the real command over a
// workspace whose lockfile records no marker and whose trust record carries a
// withdrawal on a gate plugin. The listing must name the withdrawn plugin in its
// deprecations section, in the record's own words.
func TestClosureListingReportsAWithdrawnTrustedPlugin(t *testing.T) {
	t.Parallel()
	root, releasePath := closureFindingsWorkspace(t, false)
	record := &pluginstrust.Trust{
		Version: pluginstrust.CurrentVersion,
		Plugins: []pluginstrust.TrustedPlugin{{
			Plugin:          "linter/house-style",
			ResolvedVersion: "0.3.0",
			Digest:          "sha256:1111111111111111111111111111111111111111111111111111111111111111",
			Deprecation:     &pluginstrust.Deprecation{Severity: "retracted", Message: "withdrawn: unsound gate"},
		}},
	}
	if err := pluginstrust.Write(pluginstrust.PathFor(releasePath), record); err != nil {
		t.Fatalf("write trust record: %v", err)
	}
	// Read from the record's own producer rather than spelled here, so the assertion
	// is about the listing carrying the report, not about one sentence.
	findings := record.DeprecationFindings()
	if len(findings) != 1 {
		t.Fatalf("the record's own producer reports %d withdrawals, so the assertion below would be vacuous", len(findings))
	}
	notice := findings[0].Notice()

	listing := closureListingFor(t, root, releasePath)
	if !strings.Contains(listing, "\nDeprecations:\n") {
		t.Fatalf("the listing carries no deprecations section although the trust record records a withdrawal:\n%s", listing)
	}
	if !strings.Contains(listing, "  "+notice+"\n") {
		t.Fatalf("the listing does not name the withdrawn gate plugin %q:\n%s", notice, listing)
	}
}

// TestClosureListingRefusesAnUnreadableTrustRecord is the other half of reading the
// record: one that is there and cannot be read refuses the listing rather than
// answering as though no plugin were withdrawn, and writes nothing.
func TestClosureListingRefusesAnUnreadableTrustRecord(t *testing.T) {
	t.Parallel()
	root, releasePath := closureFindingsWorkspace(t, false)
	if err := os.WriteFile(pluginstrust.PathFor(releasePath), []byte("plugins: [\n"), 0o644); err != nil {
		t.Fatalf("write damaged record: %v", err)
	}

	var buf bytes.Buffer
	opts := testInspectOptions(&buf)
	opts.closure = true
	opts.level = levelDefault
	err := dispatchInspect(context.Background(),
		resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		releasePath,
		nil,
		opts,
	)
	if err == nil {
		t.Fatalf("a record that cannot be read must refuse rather than pass as empty; got the listing:\n%s", buf.String())
	}
	if buf.Len() != 0 {
		t.Errorf("a refused listing must write nothing; got:\n%s", buf.String())
	}
}
