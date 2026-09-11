// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

const guardComponentBody = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/api-server
  version: 1.0.0
spec:
  schema:
    values:
      type: object
`

// TestPushAbsentTagPublishes confirms the guard's baseline: a tag that
// does not yet exist publishes normally and reports completion.
func TestPushAbsentTagPublishes(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	entityPath := filepath.Join(root, "1.0.0", "entity.yaml")
	writeEntityFixture(t, entityPath, guardComponentBody)

	store := memory.New()
	var buf bytes.Buffer
	if err := pushRun(t.Context(), pushConfig{Workspace: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), EntityPath: entityPath, Registry: "example.com", PushTarget: store, Out: &buf}); err != nil {
		t.Fatalf("push to empty registry: %v", err)
	}
	if !strings.Contains(buf.String(), "Push complete:") {
		t.Errorf("expected 'Push complete:'; got:\n%s", buf.String())
	}
}

// TestPushSameContentIsIdempotentNoOp confirms that re-pushing
// byte-identical content under an existing tag is not treated as an
// overwrite: it reports "Already published" and succeeds. This is the
// branch the manifest-digest pin exists to make reachable — without a
// reproducible digest, an identical re-push would look like a conflict.
func TestPushSameContentIsIdempotentNoOp(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	entityPath := filepath.Join(root, "1.0.0", "entity.yaml")
	writeEntityFixture(t, entityPath, guardComponentBody)

	store := memory.New()
	if err := pushRun(t.Context(), pushConfig{Workspace: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), EntityPath: entityPath, Registry: "example.com", PushTarget: store}); err != nil {
		t.Fatalf("first push: %v", err)
	}

	var buf bytes.Buffer
	if err := pushRun(t.Context(), pushConfig{Workspace: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), EntityPath: entityPath, Registry: "example.com", PushTarget: store, Out: &buf}); err != nil {
		t.Fatalf("idempotent re-push should succeed; got %v", err)
	}
	if !strings.Contains(buf.String(), "Already published:") {
		t.Errorf("expected 'Already published:' on identical re-push; got:\n%s", buf.String())
	}
}

// TestPushRefusesOverwriteWithDifferentContent confirms immutability:
// pushing different content under an already-published tag halts with
// ErrImmutableTagConflict, and the existing tag is left pointing at its
// original bytes (the conflicting content never reaches the registry).
func TestPushRefusesOverwriteWithDifferentContent(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	entityPath := filepath.Join(root, "1.0.0", "entity.yaml")
	writeEntityFixture(t, entityPath, guardComponentBody)

	store := memory.New()
	if err := pushRun(t.Context(), pushConfig{Workspace: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), EntityPath: entityPath, Registry: "example.com", PushTarget: store}); err != nil {
		t.Fatalf("first push: %v", err)
	}

	// Same tag (metadata.version unchanged), different content.
	changed := strings.Replace(guardComponentBody, "type: object", "type: string", 1)
	if changed == guardComponentBody {
		t.Fatal("fixture setup: content did not change")
	}
	writeEntityFixture(t, entityPath, changed)

	err := pushRun(t.Context(), pushConfig{Workspace: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), EntityPath: entityPath, Registry: "example.com", PushTarget: store})
	if err == nil {
		t.Fatal("expected ErrImmutableTagConflict; got nil")
	}
	if !errors.Is(err, oci.ErrImmutableTagConflict()) {
		t.Errorf("error should wrap ErrImmutableTagConflict; got %v", err)
	}

	// The published tag must still carry the original bytes.
	pulled, err := oci.Pull(context.Background(), store, "1.0.0")
	if err != nil {
		t.Fatalf("pull after refused overwrite: %v", err)
	}
	if string(pulled.Bytes) != guardComponentBody {
		t.Errorf("tag was overwritten despite refusal:\n got: %s\nwant: %s", pulled.Bytes, guardComponentBody)
	}
}
