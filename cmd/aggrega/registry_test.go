// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestRegistryAddKeyless proves `registry add --identity` writes a keyless
// trust policy that loads back through the config resolver.
func TestRegistryAddKeyless(t *testing.T) {
	t.Parallel()
	rw := resolveTempWorkspace(t)
	var out bytes.Buffer
	err := registryAddRun(registryAddConfig{
		Host: "keyless.example.com",
		Identities: []string{
			"dev@example.com=https://accounts.google.com",
		},
		RequireSignatures: true,
		RequireValue:      true,
		ws:                rw,
		Out:               &out,
	})
	if err != nil {
		t.Fatalf("registryAddRun: %v", err)
	}

	reloaded, err := workspaceconfig.Resolve(rw.Root(), "")
	if err != nil {
		t.Fatalf("re-resolve: %v", err)
	}
	pol, found := reloaded.Config().RegistryTrustPolicy("keyless.example.com")
	if !found {
		t.Fatal("registry should be found after add")
	}
	if !pol.RequiresSignatures {
		t.Error("requires_signatures should be set")
	}
	if len(pol.TrustedIdentities) != 1 || pol.TrustedIdentities[0].Identity != "dev@example.com" {
		t.Errorf("trusted identities = %v", pol.TrustedIdentities)
	}
}

// TestRegistryAddKeyed proves `registry add --key` writes a pinned public
// key read from disk.
func TestRegistryAddKeyed(t *testing.T) {
	t.Parallel()
	rw := resolveTempWorkspace(t)
	keyPath := filepath.Join(t.TempDir(), "acme.pub")
	const pem = "-----BEGIN PUBLIC KEY-----\nMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE\n-----END PUBLIC KEY-----\n"
	if err := os.WriteFile(keyPath, []byte(pem), 0o644); err != nil {
		t.Fatalf("write key: %v", err)
	}
	var out bytes.Buffer
	if err := registryAddRun(registryAddConfig{
		Host:    "keyed.example.com",
		KeyPath: keyPath,
		ws:      rw,
		Out:     &out,
	}); err != nil {
		t.Fatalf("registryAddRun: %v", err)
	}
	reloaded, _ := workspaceconfig.Resolve(rw.Root(), "")
	pol, found := reloaded.Config().RegistryTrustPolicy("keyed.example.com")
	if !found {
		t.Fatal("registry should be found")
	}
	if pol.PublicKey != pem {
		t.Errorf("public key = %q, want %q", pol.PublicKey, pem)
	}
}

// TestRegistryAddHubBaseline proves adding the hub host with no policy
// flags pins the embedded baseline.
func TestRegistryAddHubBaseline(t *testing.T) {
	t.Parallel()
	rw := resolveTempWorkspace(t)
	var out bytes.Buffer
	if err := registryAddRun(registryAddConfig{
		Host: workspaceconfig.HubBaselineHost,
		ws:   rw,
		Out:  &out,
	}); err != nil {
		t.Fatalf("registryAddRun: %v", err)
	}
	reloaded, _ := workspaceconfig.Resolve(rw.Root(), "")
	pol, found := reloaded.Config().RegistryTrustPolicy(workspaceconfig.HubBaselineHost)
	if !found {
		t.Fatal("hub should be found")
	}
	if !pol.RequiresSignatures || len(pol.TrustedIdentities) < 2 {
		t.Errorf("hub baseline not pinned: %+v", pol)
	}
}

// TestRegistryAddPreservesExisting proves a second add does not clobber the
// first registry's entry.
func TestRegistryAddPreservesExisting(t *testing.T) {
	t.Parallel()
	rw := resolveTempWorkspace(t)
	var out bytes.Buffer
	add := func(host, identity string) {
		if err := registryAddRun(registryAddConfig{
			Host:       host,
			Identities: []string{identity + "=https://accounts.google.com"},
			ws:         rw,
			Out:        &out,
		}); err != nil {
			t.Fatalf("registryAddRun %s: %v", host, err)
		}
	}
	add("a.example.com", "a@example.com")
	add("b.example.com", "b@example.com")

	reloaded, _ := workspaceconfig.Resolve(rw.Root(), "")
	if _, found := reloaded.Config().RegistryTrustPolicy("a.example.com"); !found {
		t.Error("first registry should survive the second add")
	}
	if _, found := reloaded.Config().RegistryTrustPolicy("b.example.com"); !found {
		t.Error("second registry should be present")
	}
}

// TestRegistryAddRejectsMalformedIdentity proves an identity not in the
// identity=issuer form is rejected.
func TestRegistryAddRejectsMalformedIdentity(t *testing.T) {
	t.Parallel()
	rw := resolveTempWorkspace(t)
	err := registryAddRun(registryAddConfig{
		Host:       "x.example.com",
		Identities: []string{"no-issuer-here"},
		ws:         rw,
	})
	if err == nil {
		t.Fatal("malformed --identity should be rejected")
	}
}
