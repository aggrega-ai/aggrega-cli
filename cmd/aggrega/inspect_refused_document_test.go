// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// versionlessFeatureDocument is a Feature its kind's schema refuses for one
// fault: it declares no metadata.version.
const versionlessFeatureDocument = `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: local/nightly-backup
spec:
  appliesTo: "component.aggrega.ai/category=datastore"
`

// TestInspectByNameStatesARefusedDocumentOnce drives `aggrega inspect <name>`
// through the real command tree over a catalog holding a document its kind's
// schema refuses, and reads the one diagnostic the process error stream carries.
//
// The load that reads the catalog refuses the document, and the reader is owed
// that refusal once: the document as the head's source, the fault beneath it,
// the head carrying the category and the condition's short code — no frame for
// the directory the load was walking, none for the step that read the file, and
// no advice to move a file that sits where the catalog reads. It is asserted by
// equality, so any nested frame or appended note fails it.
func TestInspectByNameStatesARefusedDocumentOnce(t *testing.T) {
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment seams below are process-global too.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	code, ok := cerrors.ShortCodeOf(envelope.ErrEntityEnvelope())
	if !ok {
		t.Fatal("the envelope-validation condition must be registered with a short code")
	}
	root := adviceWorkspaceRoot(t, "workspace: {}\n", nil, map[string]string{"nightly-backup.yaml": versionlessFeatureDocument})
	doc := filepath.Join(root, workspaceconfig.ConventionalCatalogDir, "nightly-backup.yaml")

	cmd := newRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs([]string{"--workspace-root", root, "inspect", "local/nightly-backup"})
	var exit int
	process := captureStderr(t, func() {
		exit = executeRoot(context.Background(), cmd)
	})
	if exit != 1 {
		t.Fatalf("`aggrega inspect local/nightly-backup` exited %d; want 1\n%s", exit, process)
	}

	want := "[" + string(cerrors.CatalogError) + " " + code + "] " + doc + "\n" +
		"  document envelope validation failed (Feature); 3:1: metadata.version: missing required property: version. " +
		"correct the document to match its kind's schema"
	if got := strings.TrimRight(process, "\n"); got != want {
		t.Errorf("the refusal reads\n%s\nwant\n%s", got, want)
	}
	if n := strings.Count(process, "caused by:"); n != 0 {
		t.Errorf("the refusal carries %d nested frames; want none:\n%s", n, process)
	}
	if strings.Contains(process, adviceOpening) {
		t.Errorf("the refusal advises moving a document the catalog read:\n%s", process)
	}
	if out.Len() != 0 {
		t.Errorf("a refused inspection wrote to stdout:\n%s", out.String())
	}
}
