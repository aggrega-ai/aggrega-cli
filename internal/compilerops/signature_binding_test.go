// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compilerops

// The build's check of recorded signature facts, held to which artifact each fact's
// bundle was made for. The check has four inputs that decide the outcome — which
// store records the fact, which of the two forms a pull accepts its bundle is in,
// whether the fact is the row's own or one copied from another, and whether a
// signing requirement covers the reference — so the cells are enumerated rather
// than left to margins: two stores, two forms, two facts, a requirement on or off,
// every cell asserted. The requirement axis is in the grid because the refusal
// must not be wired behind it, and the form axis because the reading of which
// artifact a signature was made for is a different decoding for each form.
//
// Beside the grid stand the bounds the check states, each as a row of its own: a
// reference read from a catalog directory, and a bundle in a form the reading does
// not recognise, are held to the re-hash alone.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/verdicttest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// bindingCell is one cell of the grid.
type bindingCell struct {
	envelope bool
	copied   bool
	required bool
}

func (c bindingCell) String() string {
	form := "a message signature"
	if c.envelope {
		form = "a signed envelope"
	}
	fact := "the row's own"
	if c.copied {
		fact = "copied from another row"
	}
	req := "no requirement"
	if c.required {
		req = "a requirement"
	}
	return form + ", " + fact + ", under " + req
}

func bindingCells() []bindingCell {
	var cells []bindingCell
	for _, envelope := range []bool{false, true} {
		for _, copied := range []bool{false, true} {
			for _, required := range []bool{false, true} {
				cells = append(cells, bindingCell{envelope: envelope, copied: copied, required: required})
			}
		}
	}
	return cells
}

// factFor makes, in the cell's form, a signature over madeFor — a manifest digest —
// caches its bundle where a pull caches a verified one, and returns the fact a pull
// records for it.
func (c bindingCell) factFor(t *testing.T, root, cacheDir string, signer *signing.KeyedSigner, madeFor string) *lockfile.VerifiedSignature {
	t.Helper()
	if !c.envelope {
		return signedFactFor(t, root, cacheDir, signer, madeFor)
	}
	bundle, _, err := signingtest.KeyedEnvelope(signingtest.Statement(madeFor))
	if err != nil {
		t.Fatalf("sign an envelope over %s: %v", madeFor, err)
	}
	return gateFact(t, root, cacheDir, bundle)
}

// bindingConfig is the configuration a cell runs under.
func bindingConfig(c bindingCell) workspaceconfig.Config {
	if c.required {
		return requiring()
	}
	return workspaceconfig.Config{DefaultRegistry: gateRegistry}
}

// assertBindingOutcome holds one cell's outcome: its own signature builds whatever
// the requirement, and a copied one is refused as made for a different artifact
// whatever the requirement, naming the subject, the file recording the fact and the
// manifest the build read.
func assertBindingOutcome(t *testing.T, c bindingCell, err error, subject, other, file, readManifest string) {
	t.Helper()
	if !c.copied {
		if err != nil {
			t.Fatalf("%s: the build must go ahead: %v", c, err)
		}
		return
	}
	if !errors.Is(err, oci.ErrSignatureForAnotherArtifact()) {
		t.Fatalf("%s: want the signature-for-another-artifact refusal, got %v", c, err)
	}
	if errors.Is(err, oci.ErrSignatureBundleDigestMismatch()) || errors.Is(err, lockfile.ErrSignatureNotRecorded()) {
		t.Errorf("%s: the refusal also reports a damaged bundle or a missing signature, and neither is true: %v", c, err)
	}
	text := err.Error()
	for _, want := range []string{`artifact "` + subject + `"`, file + " records it", "the artifact read has manifest " + readManifest} {
		if !strings.Contains(text, want) {
			t.Errorf("%s: the refusal does not carry %q:\n%s", c, want, text)
		}
	}
	if strings.Contains(text, `"`+other+`"`) {
		t.Errorf("%s: the refusal names %q, whose own signature it is:\n%s", c, other, text)
	}
}

// TestTheLockfileStoreHoldsAFactToTheArtifactItWasMadeFor is the first store's
// half of the grid: two Components the content cache served, each under its own
// manifest, the second row carrying its own fact or the first row's.
func TestTheLockfileStoreHoldsAFactToTheArtifactItWasMadeFor(t *testing.T) {
	t.Parallel()
	for _, c := range bindingCells() {
		t.Run(c.String(), func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			cfg := bindingConfig(c)
			cacheDir := cfg.ResolvedArtifactCacheDir()
			manifestA := digest.ContentDigest([]byte("manifest of test/a"))
			manifestB := digest.ContentDigest([]byte("manifest of test/b"))
			signer := copiedFactSigner(t)
			factA := c.factFor(t, root, cacheDir, signer, manifestA)
			factB := c.factFor(t, root, cacheDir, signer, manifestB)
			if c.copied {
				copied := *factA
				factB = &copied
			}
			cl := closure.Closure{Entities: []closure.ResolvedRef{
				{URI: "test/a", ResolvedVersion: "1.0.0", ManifestDigest: manifestA, ResolutionPath: closure.ResolutionPathCache, KindHint: "component"},
				{URI: "test/b", ResolvedVersion: "1.0.0", ManifestDigest: manifestB, ResolutionPath: closure.ResolutionPathCache, KindHint: "component"},
			}}
			lf := &lockfile.Lockfile{Version: 1, Entities: []lockfile.Entity{
				{Kind: "component", URI: "test/a", ResolvedVersion: "1.0.0", Signature: factA},
				{Kind: "component", URI: "test/b", ResolvedVersion: "1.0.0", Signature: factB},
			}}
			err := enforceRecordedSignatures(gateInputs(root, cfg, lf), cl)
			assertBindingOutcome(t, c, err, "test/b", "test/a", lockfile.PathFor(gateRelease), manifestB)
		})
	}
}

// TestTheTrustRecordStoreHoldsAFactToTheArtifactItWasMadeFor is the second store's
// half: two plugins attached at a verdict role, each served by the content cache
// under its own manifest, the second plugin's entry carrying its own fact or the
// first plugin's.
func TestTheTrustRecordStoreHoldsAFactToTheArtifactItWasMadeFor(t *testing.T) {
	t.Parallel()
	for i, c := range bindingCells() {
		t.Run(c.String(), func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			catalogDir := filepath.Join(root, "catalog")
			suffix := string(rune('a' + i))
			a := verdicttest.SeedCached(t, root, catalogDir, "linter", "binding-a-"+suffix, "1.0.0")
			b := verdicttest.SeedCached(t, root, catalogDir, "linter", "binding-b-"+suffix, "1.0.0")
			cacheDir := workspaceconfig.Config{}.ResolvedArtifactCacheDir()
			releasePath := filepath.Join(root, "release.yaml")
			signer := copiedFactSigner(t)
			factA := c.factFor(t, root, cacheDir, signer, a.ManifestDigest)
			factB := c.factFor(t, root, cacheDir, signer, b.ManifestDigest)
			if c.copied {
				copied := *factA
				factB = &copied
			}
			trust := &pluginstrust.Trust{Version: pluginstrust.CurrentVersion}
			trust.Upsert(pluginstrust.TrustedPlugin{Plugin: a.Coordinate, ResolvedVersion: "1.0.0", Digest: a.WasmDigest, Signature: factA})
			trust.Upsert(pluginstrust.TrustedPlugin{Plugin: b.Coordinate, ResolvedVersion: "1.0.0", Digest: b.WasmDigest, Signature: factB})
			if err := pluginstrust.Write(pluginstrust.PathFor(releasePath), trust); err != nil {
				t.Fatalf("write the record: %v", err)
			}
			cfg := bindingConfig(c)
			cfg.Plugins.Linter = []workspaceconfig.PluginRef{{URI: a.URI}, {URI: b.URI}}
			view := catalog.NewLazyLookup(root, cacheDir, nil, nil)
			err := enforceRecordedSignatures(NewPreviewInputs(NoExternalInputs(), view, releasePath, root, cfg, "0.0.0-signature-gate-probe"), closure.Closure{})
			assertBindingOutcome(t, c, err, b.Coordinate, a.Coordinate, pluginstrust.PathFor(releasePath), b.ManifestDigest)
		})
	}
}

// TestTheBoundsTheBindingStates holds each case the check holds to the re-hash
// alone, beside the re-hash still refusing a damaged bundle on the same case — so
// a pass below is the stated bound rather than a check that holds nothing.
func TestTheBoundsTheBindingStates(t *testing.T) {
	t.Parallel()
	t.Run("a row read from a catalog directory carrying another row's signature is re-hashed and otherwise admitted", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		cfg := requiring()
		cacheDir := cfg.ResolvedArtifactCacheDir()
		fact := signedFactFor(t, root, cacheDir, copiedFactSigner(t), digest.ContentDigest([]byte("manifest of another artifact")))
		cl := closure.Closure{Entities: []closure.ResolvedRef{
			{URI: "test/local", ResolvedVersion: "1.0.0", ResolutionPath: closure.ResolutionPathCatalog, KindHint: "component"},
		}}
		lf := &lockfile.Lockfile{Version: 1, Entities: []lockfile.Entity{{Kind: "component", URI: "test/local", ResolvedVersion: "1.0.0", Signature: fact}}}
		if err := enforceRecordedSignatures(gateInputs(root, cfg, lf), cl); err != nil {
			t.Fatalf("a catalog-directory row comes with no manifest, so its fact is held to the re-hash alone: %v", err)
		}
		if err := os.WriteFile(oci.SignatureBundlePath(root, cacheDir, fact.BundleDigest), []byte("rewritten"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := enforceRecordedSignatures(gateInputs(root, cfg, lf), cl); !errors.Is(err, oci.ErrSignatureBundleDigestMismatch()) {
			t.Fatalf("the re-hash must still refuse the same row's damaged bundle; got %v", err)
		}
	})
	t.Run("a bundle in a form the reading does not recognise is re-hashed and otherwise admitted", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		cfg := requiring()
		cacheDir := cfg.ResolvedArtifactCacheDir()
		fact := gateFact(t, root, cacheDir, []byte(`{"mediaType":"application/vnd.dev.sigstore.bundle.v0.3+json","dsseEnvelope":{}}`))
		manifest := digest.ContentDigest([]byte("manifest of test/env"))
		cl := closure.Closure{Entities: []closure.ResolvedRef{
			{URI: "test/env", ResolvedVersion: "1.0.0", ManifestDigest: manifest, ResolutionPath: closure.ResolutionPathCache, KindHint: "component"},
		}}
		lf := &lockfile.Lockfile{Version: 1, Entities: []lockfile.Entity{{Kind: "component", URI: "test/env", ResolvedVersion: "1.0.0", Signature: fact}}}
		if err := enforceRecordedSignatures(gateInputs(root, cfg, lf), cl); err != nil {
			t.Fatalf("a bundle the reading does not recognise is held to the re-hash alone: %v", err)
		}
		if err := os.WriteFile(oci.SignatureBundlePath(root, cacheDir, fact.BundleDigest), []byte("rewritten"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := enforceRecordedSignatures(gateInputs(root, cfg, lf), cl); !errors.Is(err, oci.ErrSignatureBundleDigestMismatch()) {
			t.Fatalf("the re-hash must still refuse the same row's damaged bundle; got %v", err)
		}
	})
	t.Run("a verdict plugin the catalog view serves carrying another plugin's signature is re-hashed and otherwise admitted", func(t *testing.T) {
		t.Parallel()
		c := newVerdictGateCase(t, "binding-catalog-served", true)
		fact := signedFactFor(t, c.root, c.cacheDir, copiedFactSigner(t), digest.ContentDigest([]byte("manifest of another plugin")))
		c.record(t, fact)
		if err := c.check(requiring(), 1); err != nil {
			t.Fatalf("a plugin the catalog view serves comes with no manifest, so its fact is held to the re-hash alone: %v", err)
		}
	})
}
