// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package registryops

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress/livetrust/livetrusttest"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// keylessWorkspace resolves a workspace at root indexing host under a keyless
// policy that trusts identity and pins no trust root of its own, so every
// signature the walk verifies is verified against the live public trust root.
func keylessWorkspace(t *testing.T, root, host, identity string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	globalPath := filepath.Join(t.TempDir(), "config.yaml")
	global := fmt.Sprintf("registries:\n  - host: %s\n    trusted_identities:\n      - identity: %s\n        issuer: %s\n",
		host, identity, datedIssuer)
	if err := os.WriteFile(globalPath, []byte(global), 0o644); err != nil {
		t.Fatalf("write the global layer: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatalf("write the workspace layer: %v", err)
	}
	lookup := func(k string) string {
		if k == workspaceconfig.EnvConfigOverride {
			return globalPath
		}
		return ""
	}
	rw, err := workspaceconfig.Resolve(root, root, lookup)
	if err != nil {
		t.Fatalf("resolve the workspace: %v", err)
	}
	return rw
}

// TestTheWalkVerifiesEverySignatureAgainstOneFetchOfTheLiveRoot walks a registry
// of many repositories, each version signed keyless, under a policy that pins no
// trust root, so every signature is verified against the live public trust root —
// here a repository this test serves, whose root has been rotated the way the
// public one has. The walk reads several repositories at once, so its
// verifications run at once.
//
// It requires every version to be recorded with the signer that signed it, no
// repository to be reported unreadable, and the trust root to have been fetched
// once for the whole walk. A walk fetching it once per verification fetches it
// once per version, and its TUF clients, writing one cache directory together,
// fail some of those fetches — each such repository is then reported unreadable,
// with the client's "failed to persist metadata" in its reason.
//
// Run under the race detector, it is also the proof that the workers share the
// one root without a data race.
func TestTheWalkVerifiesEverySignatureAgainstOneFetchOfTheLiveRoot(t *testing.T) {
	// Not parallel: the repository every live fetch reads is the test binary's.
	authority, err := signingtest.NewKeylessAuthority()
	if err != nil {
		t.Fatalf("NewKeylessAuthority: %v", err)
	}
	trustRoot, err := authority.TrustedRoot()
	if err != nil {
		t.Fatalf("TrustedRoot: %v", err)
	}
	liveRoot := livetrusttest.Serve(t, trustRoot)

	const (
		repositories = 48
		identity     = firstSignerIdentity
	)
	signer := keylessSignerAs{authority: authority, identity: identity, at: time.Now().Add(time.Minute)}
	reg := newValidatingRegistry(t)
	names := make([]string, 0, repositories)
	for i := range repositories {
		name := fmt.Sprintf("component/core/signed-%02d", i)
		names = append(names, name)
		target := reg.repository(t, name)
		publish(t, target, "1.0.0", name)
		signTag(t, target, "1.0.0", signer)
	}

	root := t.TempDir()
	rw := keylessWorkspace(t, root, reg.host, identity)
	var out bytes.Buffer
	res, err := Update(t.Context(), rw, UpdateOptions{Now: func() time.Time { return time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC) }}, &out)
	if err != nil {
		t.Fatalf("Update: %v\n%s", err, out.String())
	}
	if len(res.Hosts) != 1 {
		t.Fatalf("hosts = %+v; want the one registry", res.Hosts)
	}
	hu := res.Hosts[0]
	for _, skipped := range hu.Skipped {
		t.Errorf("repository %s reported unreadable: %s", skipped.Repo, skipped.Reason)
	}
	if hu.Indexed != repositories {
		t.Errorf("indexed %d repositories; want %d\n%s", hu.Indexed, repositories, out.String())
	}
	ws := &refreshFixture{reg: reg, root: root, rw: rw}
	for _, name := range names {
		rec, ok := indexedRecord(t, ws, name)
		if !ok {
			continue // reported above, as unreadable or not indexed
		}
		if len(rec.Published) != 1 || rec.Published[0].Signer == nil || rec.Published[0].Signer.Identity != identity {
			t.Errorf("%s 1.0.0 is not recorded as signed by %s: %+v", name, identity, rec.Published)
		}
	}
	if fetched := liveRoot.Fetches(); fetched != 1 {
		t.Errorf("the walk verified %d signatures against the live trust root and fetched it %d times; want once for the walk",
			repositories, fetched)
	}
}
