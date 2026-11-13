// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog/snapshottest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestThePublishersScriptWritesWhatThisPackageReads runs the catalog's own
// snapshot publisher — the script the catalog's publication runs, from the
// catalog's repository checked out beside this module — in its dry-run form over
// a git repository holding the parity fixture tree, and holds what it says and
// writes to this package: the tar stream it writes is, byte for byte, the one
// WriteSnapshotTar writes for the same tree, and the repository and media type it
// publishes under are SnapshotRepository and oci.MediaTypeCatalogSnapshot.
//
// It is the gate that re-runs the publisher on every run of this package: the
// committed tar the parity test beside it compares with was written by the script
// once, and this is what notices the script changing after that. Where the
// catalog's repository is not checked out beside this module the test fails
// naming the script, never skips.
//
// 🔴 PLANTED DEFECTS: in the script, write the mode as 0000755, drop the sort, or
// change MEDIA_TYPE: the tar row or the media-type row fails. Rename
// SnapshotRepository here and the repository row fails.
func TestThePublishersScriptWritesWhatThisPackageReads(t *testing.T) {
	tree := filepath.Join("testdata", "snapshot", "tree")
	repo := snapshottest.PublisherRepository(t, tree)
	tarOut := filepath.Join(t.TempDir(), "publisher.tar")
	out := snapshottest.RunPublisher(t, "--dry-run", "--registry", "127.0.0.1:5000", "--repository", repo,
		"--tar-out", tarOut)
	published := snapshottest.ReadPublisherOutput(t, out)

	if published.Repository != catalog.SnapshotRepository {
		t.Errorf("the publisher publishes in the repository %q; this package reads %q", published.Repository,
			catalog.SnapshotRepository)
	}
	if published.MediaType != oci.MediaTypeCatalogSnapshot {
		t.Errorf("the publisher publishes under the media type %q; this package reads %q", published.MediaType,
			oci.MediaTypeCatalogSnapshot)
	}

	written, err := os.ReadFile(tarOut)
	if err != nil {
		t.Fatalf("the publisher wrote no tar: %v", err)
	}
	var want bytes.Buffer
	if err := catalog.WriteSnapshotTar(&want, tree); err != nil {
		t.Fatalf("WriteSnapshotTar: %v", err)
	}
	if !bytes.Equal(written, want.Bytes()) {
		t.Fatalf("the publisher's tar of %s (%d bytes) is not the one WriteSnapshotTar writes (%d bytes)", tree,
			len(written), want.Len())
	}
}
