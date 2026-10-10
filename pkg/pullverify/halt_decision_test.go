// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullverify_test

import (
	"context"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestThePullHaltsOnAMissingSignatureExactlyWhereTheCheckSaysItHalts is the
// conformance between the pull's verification pass and the one halt decision a
// build applies to a lockfile row recording no signature
// (workspaceconfig.SignatureCheck.HaltsWithoutSignature). Over a grid of
// configurations and for both ways a signature fails to turn up — the registry
// answered and nothing is attached, and the registry could not be asked — the pull
// halts if and only if the check says a missing signature halts.
//
// Two implementations agreeing on the day they are written is what this exists to
// prevent from becoming two implementations disagreeing later: the pull reads the
// decision from the check rather than composing it, and this holds that it still
// does, cell by cell, so a setting cannot come to mean one thing at a pull and
// another at a build.
func TestThePullHaltsOnAMissingSignatureExactlyWhereTheCheckSaysItHalts(t *testing.T) {
	t.Parallel()
	const host = "registry.example.com"
	withEntry := func(mod func(*workspaceconfig.RegistryConfig)) []workspaceconfig.RegistryConfig {
		r := workspaceconfig.RegistryConfig{Host: host, PublicKey: "unused: no signature is ever fetched"}
		mod(&r)
		return []workspaceconfig.RegistryConfig{r}
	}
	configs := []struct {
		name string
		cfg  workspaceconfig.Config
		kind string
	}{
		{name: "nothing requires", cfg: workspaceconfig.Config{}, kind: "Component"},
		{name: "top-level policy", cfg: workspaceconfig.Config{Signing: workspaceconfig.SigningConfig{Required: true}}, kind: "Component"},
		{name: "top-level policy, kind out of scope",
			cfg: workspaceconfig.Config{Signing: workspaceconfig.SigningConfig{Required: true, Kinds: []string{"Plugin"}}}, kind: "Component"},
		{name: "top-level policy, kind undetermined",
			cfg: workspaceconfig.Config{Signing: workspaceconfig.SigningConfig{Required: true, Kinds: []string{"Plugin"}}}, kind: ""},
		{name: "host stance", cfg: workspaceconfig.Config{Registries: withEntry(func(r *workspaceconfig.RegistryConfig) {
			r.Signing.Required = true
		})}, kind: "Component"},
		{name: "host stance, advisory", cfg: workspaceconfig.Config{Registries: withEntry(func(r *workspaceconfig.RegistryConfig) {
			r.Signing.Required = true
			r.Advisory = true
		})}, kind: "Component"},
		{name: "host stance, host skip", cfg: workspaceconfig.Config{Registries: withEntry(func(r *workspaceconfig.RegistryConfig) {
			r.Signing.Required = true
			r.SkipSignatureVerification = true
		})}, kind: "Component"},
		{name: "top-level policy, host skip", cfg: workspaceconfig.Config{
			Signing: workspaceconfig.SigningConfig{Required: true},
			Registries: withEntry(func(r *workspaceconfig.RegistryConfig) {
				r.SkipSignatureVerification = true
			})}, kind: "Component"},
		{name: "top-level policy, advisory host", cfg: workspaceconfig.Config{
			Signing: workspaceconfig.SigningConfig{Required: true},
			Registries: withEntry(func(r *workspaceconfig.RegistryConfig) {
				r.Advisory = true
			})}, kind: "Component"},
		{name: "global skip over both scopes", cfg: workspaceconfig.Config{
			SkipSignatureVerification: true,
			Signing:                   workspaceconfig.SigningConfig{Required: true},
			Registries: withEntry(func(r *workspaceconfig.RegistryConfig) {
				r.Signing.Required = true
			})}, kind: "Component"},
	}

	ctx := context.Background()
	store := memory.New()
	dg, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, []byte("kind: Component\n"), nil)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	absences := []struct {
		name   string
		target pullverify.TargetFor
	}{
		{name: "nothing attached", target: targetForStore(store, "1.0.0")},
		{name: "lookup unanswerable", target: unanswerableTarget(store)},
	}

	halting := 0
	for _, c := range configs {
		for _, a := range absences {
			t.Run(c.name+"/"+a.name, func(t *testing.T) {
				want := c.cfg.SignatureCheckFor(host, c.kind).HaltsWithoutSignature()
				ent := pullverify.Entity{URI: host + "/component/x", ManifestDigest: dg, Host: host, Kind: c.kind}
				_, verr := pullverify.Verify(ctx, []pullverify.Entity{ent}, pullverify.Options{Config: c.cfg, Target: a.target})
				if got := verr != nil; got != want {
					t.Errorf("the pull halted = %v, the check says a missing signature halts = %v (err %v)", got, want, verr)
				}
			})
		}
		if c.cfg.SignatureCheckFor(host, c.kind).HaltsWithoutSignature() {
			halting++
		}
	}
	// The grid is only evidence if both answers occur in it: one where nothing
	// halts would pass on a pull that never halts, and one where everything halts
	// on a pull that always does.
	if halting == 0 || halting == len(configs) {
		t.Fatalf("the grid holds %d halting configurations of %d, so it cannot tell the two answers apart", halting, len(configs))
	}
}
