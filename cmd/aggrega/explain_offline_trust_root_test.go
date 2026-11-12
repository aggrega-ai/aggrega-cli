// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// `aggrega explain` offline, on the default configuration.
//
// The official hub's built-in signature check is keyless and pins no trust root,
// so a read of the documentation bundle verifies the kept signature against the
// public Sigstore trust root — which cannot be fetched offline. These cases drive
// the real command offline over a workspace that configures nothing, so the hub
// is its default registry and the built-in check is the one asked, and show what
// it answers with the trust root an earlier online fetch left on the machine,
// with that left trust root removed, and with a signer the check does not trust.
//
// The signatures are made by in-memory authorities, signing as an identity the
// built-in check trusts — read off the baseline rather than quoted — and the
// trust root those authorities form stands in for the one an earlier fetch left.
// Each case states its own cache root and its own trust-root cache, so nothing
// here reads or writes the developer's.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress/livetrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// baselineSigner returns an identity and issuer the official hub's built-in check
// trusts, read off the baseline: the first entry naming one identity exactly.
func baselineSigner(t *testing.T) (identity, issuer string) {
	t.Helper()
	for _, id := range workspaceconfig.DefaultBaseline().TrustedIdentities {
		if id.Identity != "" {
			return id.Identity, id.Issuer
		}
	}
	t.Fatal("the built-in baseline trusts no exact identity, so these cases have no signer to stand in for")
	return "", ""
}

// offlineDefaultWorkspace returns a workspace that configures nothing but the
// offline posture: the hub is its default registry and its built-in check the one
// a read of the documentation bundle answers to.
func offlineDefaultWorkspace(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, workspaceconfig.ConfigFilename),
		[]byte("offline: true\nworkspace: {}\n"), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
	return ws
}

// seedKeylessCopy caches the fixture pages, in the case's declared cache root, at
// the coordinates the command derives for a workspace on the default registry,
// keeping a keyless signature by identity over the bundle's manifest — what an
// earlier online fetch that verified it leaves. It returns the trust-root document
// that anchors the signature.
func seedKeylessCopy(t *testing.T, pages map[string]string, identity, issuer string) []byte {
	t.Helper()
	authority, err := signingtest.NewKeylessAuthority()
	if err != nil {
		t.Fatalf("NewKeylessAuthority: %v", err)
	}
	doc, err := authority.TrustedRoot()
	if err != nil {
		t.Fatalf("TrustedRoot: %v", err)
	}
	keep := func(_ context.Context, manifestDigest string) ([][]byte, error) {
		sig, err := authority.Sign(identity, issuer, manifestDigest, time.Now().Add(time.Minute))
		if err != nil {
			return nil, err
		}
		return [][]byte{sig.Bundle}, nil
	}
	ref := docsbundle.Ref(workspaceconfig.Default().DefaultRegistryLocation().Prefix())
	if _, err := docscache.ResolveAndCache(context.Background(), explainBundleStore(t, pages), ref,
		provenanceCompilerVersion(), explainCacheRoot(t), keep); err != nil {
		t.Fatalf("ResolveAndCache: %v", err)
	}
	return doc
}

// leftByAnEarlierFetch returns a TUF client cache directory holding doc where the
// client keeps the trust root it last fetched, and the file it is held in.
func leftByAnEarlierFetch(t *testing.T, doc []byte) (dir, file string) {
	t.Helper()
	dir = t.TempDir()
	file = livetrust.CachedTrustedRootPath(dir)
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatalf("create the cache layout: %v", err)
	}
	if err := os.WriteFile(file, doc, 0o644); err != nil {
		t.Fatalf("write the held trust root: %v", err)
	}
	return dir, file
}

// TestExplainServesTheWalkThroughOfflineFromTheTrustRootLastFetched is the
// offline read on the default configuration. With the trust root an earlier
// online fetch left, the walk-through is served. With it removed, the condition is
// still answered and the note says the copy was not admitted because the
// signature check could not run offline.
func TestExplainServesTheWalkThroughOfflineFromTheTrustRootLastFetched(t *testing.T) {
	// Not parallel: isolates the process environment and the working directory.
	isolateCLIEnvironment(t)
	short, code, message := explainProbe(t)
	identity, issuer := baselineSigner(t)
	doc := seedKeylessCopy(t, seededPages(short, code, message, true), identity, issuer)
	trustCache, held := leftByAnEarlierFetch(t, doc)
	ws := offlineDefaultWorkspace(t)
	ctx := docsbundle.WithTrustRootCache(explainContext(t), trustCache)

	out, notes, err := runExplainCommandWith(t, ctx, "explain", short, "--workspace-root", ws)
	if err != nil {
		t.Fatalf("aggrega explain %s offline: %v\nnotes: %s", short, err, notes)
	}
	for _, want := range []string{short, code, explainProbeHeading, explainProbeProse} {
		if !strings.Contains(out, want) {
			t.Errorf("offline, with the trust root an earlier fetch left, the answer omits %q:\n%s\nnotes: %s", want, out, notes)
		}
	}
	if strings.Contains(notes, "not available") {
		t.Errorf("offline, with the trust root an earlier fetch left, the walk-through was reported missing:\n%s", notes)
	}

	if err := os.Remove(held); err != nil {
		t.Fatalf("remove the held trust root: %v", err)
	}
	out, notes, err = runExplainCommandWith(t, ctx, "explain", short, "--workspace-root", ws)
	if err != nil {
		t.Fatalf("aggrega explain %s offline, the trust root removed: %v\nnotes: %s", short, err, notes)
	}
	if !strings.Contains(out, short) || !strings.Contains(out, code) {
		t.Errorf("the condition itself was not answered:\n%s", out)
	}
	if strings.Contains(out, explainProbeProse) {
		t.Errorf("with no trust root held, the walk-through was served from a copy no check admitted:\n%s", out)
	}
	for _, want := range []string{"not available", "does not admit", "could not run offline"} {
		if !strings.Contains(notes, want) {
			t.Errorf("with no trust root held, the note does not say %q:\n%s", want, notes)
		}
	}
}

// TestExplainRefusesOfflineASignerTheWorkspaceDoesNotTrust pins that the trust
// root an earlier fetch left only anchors the check, as the live one does online:
// a copy signed by an identity the built-in check does not trust is refused, for
// that reason and not for being offline.
func TestExplainRefusesOfflineASignerTheWorkspaceDoesNotTrust(t *testing.T) {
	// Not parallel: isolates the process environment and the working directory.
	isolateCLIEnvironment(t)
	short, code, message := explainProbe(t)
	_, issuer := baselineSigner(t)
	doc := seedKeylessCopy(t, seededPages(short, code, message, true), "someone-else@example.invalid", issuer)
	trustCache, _ := leftByAnEarlierFetch(t, doc)
	ws := offlineDefaultWorkspace(t)
	ctx := docsbundle.WithTrustRootCache(explainContext(t), trustCache)

	out, notes, err := runExplainCommandWith(t, ctx, "explain", short, "--workspace-root", ws)
	if err != nil {
		t.Fatalf("aggrega explain %s offline: %v\nnotes: %s", short, err, notes)
	}
	if strings.Contains(out, explainProbeProse) {
		t.Errorf("a copy signed by an identity the check does not trust was served:\n%s", out)
	}
	if !strings.Contains(notes, "does not admit") {
		t.Errorf("the note does not say the copy was refused:\n%s", notes)
	}
	// The untrusted-signer condition is reported only once the signature's chain has
	// verified against the trust root, so it says the held root anchored the check —
	// the condition an online check reports for the same signer.
	if untrusted := signing.ErrUntrustedIdentity().Error(); !strings.Contains(notes, untrusted) {
		t.Errorf("the copy was not refused for its signer (%q), so the check did not run against the held trust root:\n%s",
			untrusted, notes)
	}
	if strings.Contains(notes, "could not run offline") {
		t.Errorf("the copy was refused for being offline rather than for its signer:\n%s", notes)
	}
}
