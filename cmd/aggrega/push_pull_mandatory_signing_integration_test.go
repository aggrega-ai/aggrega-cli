// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
)

// The org-wide signing policy (signing.required + signing.kinds) forces a
// signature on an in-scope artifact kind regardless of the destination or
// source registry's own per-registry policy. These integration tests drive the
// two enforcement sites — the production push path (producer refusal) and the
// production pull path (consumer rejection) — against an in-process real OCI
// registry, proving each acceptance arm from a real invocation.

const orgSignPluginBody = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: linter/example
  version: 1.0.0
`

// writeOrgSigningPushWorkspace writes a workspace config carrying the given body
// into a fresh tempdir and chdirs into it, returning the workspace root. The
// body sets default_registry and the org signing block; it deliberately carries
// NO per-registry signing.required, so any refusal is attributable to the
// org policy alone.
func writeOrgSigningPushWorkspace(t *testing.T, configBody string) string {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	writeWorkspaceConfig(t, root, configBody)
	return root
}

// TestIntegration_PushRefusesUnsignedUnderOrgPolicy proves the producer-side
// enforcement: with the org signing policy on, an unsigned push of an in-scope
// kind (plugin, component) is refused with the org-policy sentinel, a signed
// push succeeds, an out-of-scope kind is allowed unsigned, and with the policy
// off nothing changes. The org policy carries no per-registry
// signing.required, so each refusal is the org layer, not the registry.
func TestIntegration_PushRefusesUnsignedUnderOrgPolicy(t *testing.T) {
	// Arm 1: an unsigned Plugin under an org policy that requires every kind is
	// refused with the org-policy sentinel (not the per-registry one).
	t.Run("unsigned_plugin_refused", func(t *testing.T) {
		host := startRegistry(t, nil)
		root := writeOrgSigningPushWorkspace(t, "default_registry: "+host+"\nsigning:\n  required: true\n")
		entityPath := filepath.Join(root, "1.0.0", "plugin.yaml")
		writeEntityFixture(t, entityPath, orgSignPluginBody)

		noSign := false
		err := pushRun(pushConfig{EntityPath: entityPath, Registry: host, Sign: &noSign})
		if !errors.Is(err, signing.ErrSigningPolicyRequiresSignature) {
			t.Fatalf("want ErrSigningPolicyRequiresSignature for an unsigned in-scope Plugin, got %v", err)
		}
		if errors.Is(err, signing.ErrUnsignedPushRefused) {
			t.Fatalf("org-policy refusal must not carry the per-registry sentinel: %v", err)
		}
	})

	// Arm 2: an unsigned catalog entity (a Component) under an org policy scoped
	// to Component is refused with the org-policy sentinel.
	t.Run("unsigned_component_refused", func(t *testing.T) {
		host := startRegistry(t, nil)
		root := writeOrgSigningPushWorkspace(t, "default_registry: "+host+"\nsigning:\n  required: true\n  kinds: [Component]\n")
		entityPath := filepath.Join(root, "1.0.0", "component.yaml")
		writeEntityFixture(t, entityPath, signComponentBody)

		noSign := false
		err := pushRun(pushConfig{EntityPath: entityPath, Registry: host, Sign: &noSign})
		if !errors.Is(err, signing.ErrSigningPolicyRequiresSignature) {
			t.Fatalf("want ErrSigningPolicyRequiresSignature for an unsigned in-scope Component, got %v", err)
		}
	})

	// Arm 3: a signed push of an in-scope Component succeeds — the org policy
	// forces signing on by default and the signer satisfies it.
	t.Run("signed_component_succeeds", func(t *testing.T) {
		host := startRegistry(t, nil)
		root := writeOrgSigningPushWorkspace(t, "default_registry: "+host+"\nsigning:\n  required: true\n  kinds: [Component]\n")
		entityPath := filepath.Join(root, "1.0.0", "component.yaml")
		writeEntityFixture(t, entityPath, signComponentBody)

		fake := &signingtest.FakeSigner{}
		// Sign left nil: the org policy makes signing the default; the signer
		// satisfies the requirement.
		if err := pushRun(pushConfig{EntityPath: entityPath, Registry: host, Signer: fake}); err != nil {
			t.Fatalf("a signed push of an in-scope kind must succeed, got %v", err)
		}
		if len(fake.Signed) != 1 {
			t.Fatalf("org-policy default-sign push signed %d times, want 1", len(fake.Signed))
		}
	})

	// Arm 5 (push): a kind NOT in signing.kinds is allowed unsigned even with the
	// policy on — the scope is exact when kinds is a non-wildcard list.
	t.Run("out_of_scope_kind_allowed_unsigned", func(t *testing.T) {
		host := startRegistry(t, nil)
		root := writeOrgSigningPushWorkspace(t, "default_registry: "+host+"\nsigning:\n  required: true\n  kinds: [Plugin]\n")
		entityPath := filepath.Join(root, "1.0.0", "component.yaml")
		writeEntityFixture(t, entityPath, signComponentBody)

		if err := pushRun(pushConfig{EntityPath: entityPath, Registry: host}); err != nil {
			t.Fatalf("an out-of-scope kind must push unsigned without refusal, got %v", err)
		}
	})

	// Arm 6 (push): with the policy off (default), an unsigned push to a
	// non-requiring registry succeeds exactly as before — non-regression.
	t.Run("policy_off_unsigned_allowed", func(t *testing.T) {
		host := startRegistry(t, nil)
		root := writeOrgSigningPushWorkspace(t, "default_registry: "+host+"\n")
		entityPath := filepath.Join(root, "1.0.0", "component.yaml")
		writeEntityFixture(t, entityPath, signComponentBody)

		if err := pushRun(pushConfig{EntityPath: entityPath, Registry: host}); err != nil {
			t.Fatalf("with the org policy off, an unsigned push must succeed, got %v", err)
		}
	})
}

// TestIntegration_PushOrgPolicyUnremovableViaCIOverlay proves arm 7: a base
// config that turns the org policy OFF plus a CI overlay that turns it ON
// composes to ON at the enforcement site — an unsigned in-scope push is still
// refused. The policy cannot be removed by an ordinary lower-precedence config;
// enforcement reads the composed result, not the base layer.
func TestIntegration_PushOrgPolicyUnremovableViaCIOverlay(t *testing.T) {
	host := startRegistry(t, nil)
	root := writeOrgSigningPushWorkspace(t, "default_registry: "+host+"\nsigning:\n  required: false\n  kinds: [Component]\n")
	// CI overlay turns the policy on; the base said off. The monotonic compose
	// keeps it on.
	if err := os.WriteFile(filepath.Join(root, ".aggrega-config-append-ci.yaml"),
		[]byte("signing:\n  required: true\n  kinds: [Component]\n"), 0o644); err != nil {
		t.Fatalf("write CI overlay: %v", err)
	}
	entityPath := filepath.Join(root, "1.0.0", "component.yaml")
	writeEntityFixture(t, entityPath, signComponentBody)

	noSign := false
	err := pushRun(pushConfig{EntityPath: entityPath, Registry: host, Sign: &noSign})
	if !errors.Is(err, signing.ErrSigningPolicyRequiresSignature) {
		t.Fatalf("a CI-overlay-enabled org policy must still refuse an unsigned in-scope push through the enforcement site, got %v", err)
	}
}

// writeOrgSigningPullWorkspace writes a controlled fixture catalog (a Component
// addressed by its bare identity plus a Recipe that implements it), pushes each
// entity UNSIGNED to the registry at host, and writes a consumer workspace whose
// config carries the given org signing block (configBody appended under the
// signing key) but NO per-registry signing.required. It chdirs into the
// consumer workspace and returns the Component's bare closure URI. The consumer
// carries no local catalog directory, so every entity resolves through a Tier-3
// registry pull — the axis the org policy governs at the consumer.
func writeOrgSigningPullWorkspace(t *testing.T, host, signingBlock string) string {
	t.Helper()
	cat := t.TempDir()

	componentPath := filepath.Join(cat, "component/test/svc/1.0.0/component.yaml")
	writeFixture(t, componentPath, `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      port: { type: integer, default: 8080 }
      replicas: { type: integer, default: 1 }
`)
	recipePath := filepath.Join(cat, "recipe/helm/svc/1.0.0/recipe.yaml")
	writeFixture(t, recipePath, `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  capability: helm
  implements: test/svc
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: {{ optional .infra `+"`replicas`"+` 1 }}
    containers:
      server:
        image: test-server:latest
        ports:
          web: {{ optional .infra `+"`port`"+` 8080 }}
`)
	// Push UNSIGNED: the org policy at the consumer, not a per-registry policy,
	// is what must reject the pull of an in-scope unsigned entity.
	for _, p := range []string{componentPath, recipePath} {
		if err := pushRun(pushConfig{EntityPath: p, Registry: host}); err != nil {
			t.Fatalf("publish unsigned entity %s: %v", p, err)
		}
	}

	ws := t.TempDir()
	writeFixture(t, filepath.Join(ws, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: test/svc
`)
	writeFixture(t, filepath.Join(ws, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/svc:1.0.0: helm/svc:1.0.0
`)
	writeFixture(t, filepath.Join(ws, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        app:
          infra:
            replicas: 1
`)
	writeWorkspaceConfig(t, ws, "default_registry: \""+host+"\"\nauto_pull: true\noffline: false\n"+signingBlock)
	t.Chdir(ws)
	return "test/svc"
}

// TestIntegration_PullRejectsUnsignedUnderOrgPolicy proves the consumer-side
// enforcement driven through the PRODUCTION pull command. A Component resolved
// by its bare identity from a registry with NO per-registry signing.required
// is still rejected at pull when the org policy requires its kind — the org
// layer. An out-of-scope kind, and the policy-off default, pull unsigned as
// before.
func TestIntegration_PullRejectsUnsignedUnderOrgPolicy(t *testing.T) {
	// Arm 4: org policy scoped to Component; the unsigned Component is rejected
	// at pull with ErrNoSignatureFound, proving the org layer (the registry
	// carries no per-registry signing.required).
	t.Run("in_scope_unsigned_rejected", func(t *testing.T) {
		host := startRegistry(t, nil)
		writeOrgSigningPullWorkspace(t, host, "signing:\n  required: true\n  kinds: [Component]\n")

		err := pullRun(pullConfig{ReleasePath: "release.yaml"})
		if !errors.Is(err, signing.ErrNoSignatureFound) {
			t.Fatalf("an unsigned in-scope Component under the org policy must be REJECTED at pull with ErrNoSignatureFound, got %v", err)
		}
	})

	// Arm 5 (pull): org policy scoped to Plugin only; the Component and Recipe
	// are out of scope, so the unsigned pull succeeds.
	t.Run("out_of_scope_unsigned_allowed", func(t *testing.T) {
		host := startRegistry(t, nil)
		writeOrgSigningPullWorkspace(t, host, "signing:\n  required: true\n  kinds: [Plugin]\n")

		if err := pullRun(pullConfig{ReleasePath: "release.yaml"}); err != nil {
			t.Fatalf("an out-of-scope kind must pull unsigned without rejection, got %v", err)
		}
	})

	// Arm 6 (pull): no org policy; the unsigned pull succeeds exactly as before —
	// non-regression.
	t.Run("policy_off_unsigned_allowed", func(t *testing.T) {
		host := startRegistry(t, nil)
		writeOrgSigningPullWorkspace(t, host, "")

		if err := pullRun(pullConfig{ReleasePath: "release.yaml"}); err != nil {
			t.Fatalf("with the org policy off, an unsigned pull must succeed, got %v", err)
		}
	})
}
