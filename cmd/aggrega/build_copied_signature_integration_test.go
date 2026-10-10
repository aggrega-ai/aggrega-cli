// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

// A recorded signature fact copied from one lockfile row onto another, over facts a
// real pull recorded: a Component and its Recipe are published signed to a real
// in-process registry, so their signatures are made for two different manifests,
// and a consumer pulls them under a signing requirement, so each row records its
// own verified fact and both bundles are cached. Copying one row's `signature`
// mapping onto the other leaves every bundle intact in the cache — the edit needs
// the lockfile alone — so the copy is told apart only by which artifact its bundle
// was made for.
//
// The Recipe row is read by a build through the pinned catalog view and the
// Component row through the content cache's own tier, so the two directions cover
// both ways a build reads a cache-served row.

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// signedServiceRows returns the Component's row and the one other row the pull
// recorded a signature for — the Recipe's — failing unless there is exactly one.
func signedServiceRows(t *testing.T, lf *lockfile.Lockfile) (component, recipe lockfile.Entity) {
	t.Helper()
	var others []lockfile.Entity
	for _, e := range lf.Entities {
		switch {
		case e.URI == recordedSignatureComponent:
			component = e
		case e.Signature != nil:
			others = append(others, e)
		}
	}
	if component.Signature == nil || component.Signature.BundleDigest == "" {
		t.Fatalf("the pull recorded no signature for %q: %+v", recordedSignatureComponent, lf.Entities)
	}
	if len(others) != 1 {
		t.Fatalf("want exactly one other signed row — the Recipe's — got %d: %+v", len(others), lf.Entities)
	}
	recipe = others[0]
	if recipe.Signature.BundleDigest == component.Signature.BundleDigest {
		t.Fatalf("the two rows record one bundle, so copying one onto the other changes nothing: %+v", lf.Entities)
	}
	return component, recipe
}

// TestIntegration_ABuildRefusesASignatureCopiedFromAnotherRow holds the build to
// which artifact a recorded signature was made for, on both directions of the
// copy and with no requirement at all, and holds a plain pull to repairing the
// copied fact.
func TestIntegration_ABuildRefusesASignatureCopiedFromAnotherRow(t *testing.T) {
	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	host := startRegistry(t, nil)
	publishSignedService(t, host, signer)

	ws, releasePath := writeSignedServiceConsumer(t)
	lockPath := lockfile.PathFor(releasePath)
	requiringStance := recordedSignatureStance{topRequired: true}
	writeWorkspaceConfig(t, ws, requiringStance.config(host, pubPEM))
	var pullDiag bytes.Buffer
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}),
		ReleasePath: releasePath, Out: io.Discard, DiagOut: &pullDiag}); err != nil {
		t.Fatalf("pull the signed service under a requirement: %v\n%s", err, pullDiag.String())
	}
	pristine, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read the lockfile the pull wrote: %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(lockPath)
	if err != nil {
		t.Fatalf("load the lockfile the pull wrote: %v", err)
	}
	component, recipe := signedServiceRows(t, lf)

	// copyFact rewrites the pristine lockfile with the row to carrying the row
	// from's signature, and nothing else changed.
	copyFact := func(t *testing.T, from, to lockfile.Entity) {
		t.Helper()
		edited, _, err := lockfile.LoadLockfile(lockPath)
		if err != nil {
			t.Fatalf("load the lockfile: %v", err)
		}
		fact := *from.Signature
		for i := range edited.Entities {
			if edited.Entities[i].URI == to.URI {
				edited.Entities[i].Signature = &fact
			}
		}
		if err := lockfile.WriteLockfile(lockPath, edited); err != nil {
			t.Fatalf("write the edited lockfile: %v", err)
		}
	}
	restore := func(t *testing.T, stance recordedSignatureStance) {
		t.Helper()
		if err := os.WriteFile(lockPath, pristine, 0o644); err != nil {
			t.Fatalf("restore the pristine lockfile: %v", err)
		}
		writeWorkspaceConfig(t, ws, stance.config(host, pubPEM))
	}
	build := func(t *testing.T) error {
		t.Helper()
		var out, diag bytes.Buffer
		return dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), releasePath, nil, false, &diag, &out)
	}
	refused := func(t *testing.T, err error, row, owner lockfile.Entity) {
		t.Helper()
		if err == nil {
			t.Fatalf("the build accepted the signature of %q copied onto %q: the bundle it names is intact, and was made for %q",
				owner.URI, row.URI, owner.URI)
		}
		if !strings.Contains(err.Error(), `"`+row.URI+`"`) {
			t.Errorf("the refusal does not name the row carrying the copied signature %q:\n%v", row.URI, err)
		}
	}

	t.Run("the lockfile as the pull wrote it builds", func(t *testing.T) {
		restore(t, requiringStance)
		if err := build(t); err != nil {
			t.Fatalf("the build over the pull's own lockfile must go ahead: %v", err)
		}
	})
	t.Run("the Component's signature copied onto the Recipe's row is refused", func(t *testing.T) {
		restore(t, requiringStance)
		copyFact(t, component, recipe)
		refused(t, build(t), recipe, component)
	})
	t.Run("the Recipe's signature copied onto the Component's row is refused", func(t *testing.T) {
		restore(t, requiringStance)
		copyFact(t, recipe, component)
		refused(t, build(t), component, recipe)
	})
	t.Run("with no signing requirement the copied signature is still refused", func(t *testing.T) {
		restore(t, recordedSignatureStance{})
		copyFact(t, component, recipe)
		refused(t, build(t), recipe, component)
	})
	t.Run("a plain pull replaces the copied signature with the row's own", func(t *testing.T) {
		restore(t, requiringStance)
		copyFact(t, component, recipe)
		var diag bytes.Buffer
		if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}),
			ReleasePath: releasePath, Out: io.Discard, DiagOut: &diag}); err != nil {
			t.Fatalf("the plain pull over the edited lockfile: %v\n%s", err, diag.String())
		}
		after, _, err := lockfile.LoadLockfile(lockPath)
		if err != nil {
			t.Fatalf("load the lockfile after the pull: %v", err)
		}
		found := false
		for _, e := range after.Entities {
			if e.URI != recipe.URI {
				continue
			}
			found = true
			if e.Signature == nil || *e.Signature != *recipe.Signature {
				t.Fatalf("the plain pull did not ask again about the copied signature on %q: want its own %+v, got %+v",
					recipe.URI, *recipe.Signature, e.Signature)
			}
		}
		if !found {
			t.Fatalf("the lockfile the pull wrote has no row for %q", recipe.URI)
		}
		if err := build(t); err != nil {
			t.Fatalf("the build after the pull repaired the row must go ahead: %v", err)
		}
	})
}
