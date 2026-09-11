// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
)

const signComponentBody = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/api-server
  version: 1.0.0
spec:
  schema:
    values:
      type: object
`

// writeSigningWorkspace writes a workspace config requiring signatures for
// the given registry into a fresh tempdir and chdirs into it, returning the
// entity path to push and the workspace root that config sits at.
//
// The root is returned because the signature requirement under test lives in
// that config, and a push reads the requirement from the workspace it is
// given. Returning the directory lets each caller declare it at the call site
// instead of leaving the command to discover a workspace from wherever the
// process happens to be standing.
func writeSigningWorkspace(t *testing.T, registry string, requiresSignatures bool) (entityPath, wsRoot string) {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	cfg := "workspace: {}\ndefault_registry: " + registry + "\n"
	if requiresSignatures {
		cfg += "registries:\n  - host: " + registry + "\n    signing:\n      required: true\n"
	}
	if err := os.WriteFile(filepath.Join(root, ".aggrega-config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	entityPath = filepath.Join(root, "1.0.0", "entity.yaml")
	writeEntityFixture(t, entityPath, signComponentBody)
	return entityPath, root
}

// TestPushSignFlagSignsAndAttachesReferrer proves an explicit --sign push
// signs the pushed digest and attaches the signature as a referrer, in
// one flow against an in-memory target.
func TestPushSignFlagSignsAndAttachesReferrer(t *testing.T) {
	ctx := context.Background()
	entityPath, wsRoot := writeSigningWorkspace(t, "example.com", false)
	store := memory.New()
	fake := &signingtest.FakeSigner{}

	sign := true
	if err := pushRun(t.Context(), pushConfig{
		Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot}),
		EntityPath: entityPath,
		Registry:   "example.com",
		PushTarget: store,
		Sign:       &sign,
		Signer:     fake,
	}); err != nil {
		t.Fatalf("pushRun: %v", err)
	}

	if len(fake.Signed) != 1 {
		t.Fatalf("signer called %d times, want 1", len(fake.Signed))
	}
	refs, err := oci.ListReferrers(ctx, store, "1.0.0", signing.BundleMediaType)
	if err != nil || len(refs) != 1 {
		t.Fatalf("ListReferrers: refs=%d err=%v", len(refs), err)
	}
}

// TestPushUnsignedToSignatureRequiringRegistryRefused is the
// security-critical guardrail: an unsigned push (--sign=false) to a
// registry that requires signatures halts with ErrUnsignedPushRefused and
// pushes NO bytes.
func TestPushUnsignedToSignatureRequiringRegistryRefused(t *testing.T) {
	ctx := context.Background()
	entityPath, wsRoot := writeSigningWorkspace(t, "secure.example.com", true)
	store := memory.New()

	noSign := false
	err := pushRun(t.Context(), pushConfig{
		Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot}),
		EntityPath: entityPath,
		Registry:   "secure.example.com",
		PushTarget: store,
		Sign:       &noSign,
	})
	if !errors.Is(err, signing.ErrUnsignedPushRefused()) {
		t.Fatalf("want ErrUnsignedPushRefused, got %v", err)
	}

	// No bytes were pushed: the tag must not exist.
	_, exists, rerr := oci.Resolve(ctx, store, "1.0.0")
	if rerr != nil {
		t.Fatalf("Resolve: %v", rerr)
	}
	if exists {
		t.Error("guardrail let bytes through: the tag exists after a refused unsigned push")
	}
}

// TestPushSignsByDefaultToSignatureRequiringRegistry proves that, with no
// explicit --sign, a push to a signature-requiring registry signs by
// default rather than refusing.
func TestPushSignsByDefaultToSignatureRequiringRegistry(t *testing.T) {
	ctx := context.Background()
	entityPath, wsRoot := writeSigningWorkspace(t, "secure.example.com", true)
	store := memory.New()
	fake := &signingtest.FakeSigner{}

	if err := pushRun(t.Context(), pushConfig{
		Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot}),
		EntityPath: entityPath,
		Registry:   "secure.example.com",
		PushTarget: store,
		Signer:     fake,
		// Sign left nil: policy decides → sign by default.
	}); err != nil {
		t.Fatalf("pushRun: %v", err)
	}
	if len(fake.Signed) != 1 {
		t.Fatalf("default-policy push signed %d times, want 1", len(fake.Signed))
	}
	refs, err := oci.ListReferrers(ctx, store, "1.0.0", signing.BundleMediaType)
	if err != nil || len(refs) != 1 {
		t.Fatalf("ListReferrers: refs=%d err=%v", len(refs), err)
	}
}

// TestPushSignRequestedWithoutSignerRefused proves a --sign push with no
// signer configured halts rather than silently publishing unsigned.
func TestPushSignRequestedWithoutSignerRefused(t *testing.T) {
	entityPath, wsRoot := writeSigningWorkspace(t, "example.com", false)
	store := memory.New()

	sign := true
	err := pushRun(t.Context(), pushConfig{
		Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot}),
		EntityPath: entityPath,
		Registry:   "example.com",
		PushTarget: store,
		Sign:       &sign,
		// Signer nil.
	})
	if !errors.Is(err, signing.ErrUnsignedPushRefused()) {
		t.Fatalf("want ErrUnsignedPushRefused, got %v", err)
	}
}

// TestNoAllowUnsignedFlagExists asserts the strict-refuse guardrail has no
// escape hatch: neither push nor sign exposes a flag to bypass the
// signature requirement.
func TestNoAllowUnsignedFlagExists(t *testing.T) {
	t.Parallel()
	forbidden := []string{"allow-unsigned", "no-sign", "skip-signing", "force-unsigned", "insecure-skip-signature"}

	push := newPushCmd()
	signCmd := newSignCmd()
	for _, name := range forbidden {
		if push.Flags().Lookup(name) != nil {
			t.Errorf("push exposes a forbidden bypass flag --%s", name)
		}
		if signCmd.Flags().Lookup(name) != nil {
			t.Errorf("sign exposes a forbidden bypass flag --%s", name)
		}
	}
}
