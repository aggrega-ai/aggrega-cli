// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullverify_test

import (
	"context"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestAKeptSignatureIsAdmittedExactlyWhereAPullWouldAdmitIt is the conformance
// between the pull's verification pass (Verify) and the decision a read of kept
// signature bundles takes (VerifyStored). Over a grid of configurations and
// three states of an artifact's signature — none attached, one the policy's key
// verifies, one it does not — the read admits if and only if the pull admits, and
// records the same verified fact where both admit.
//
// Every configuration that reaches a signature pins a key, so every cell is
// decided from what is held locally and nothing asks for a live trust root. The
// configurations without a registries entry are run over the unsigned state only,
// for the same reason: an entry-less policy is keyless.
func TestAKeptSignatureIsAdmittedExactlyWhereAPullWouldAdmitIt(t *testing.T) {
	t.Parallel()
	const host = "registry.example.com"
	ctx := context.Background()

	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	_, _, otherPub := signedStore(t)
	signerPub := signer.PublicKeyPEM()

	artifact := []byte("kind: Component\n")
	unsigned := memory.New()
	dg, err := oci.Push(ctx, unsigned, "1.0.0", oci.MediaTypeComponent, artifact, nil)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	signed := memory.New()
	if _, err := oci.Push(ctx, signed, "1.0.0", oci.MediaTypeComponent, artifact, nil); err != nil {
		t.Fatalf("Push: %v", err)
	}
	sig, artifactType, err := signer.Sign(ctx, dg)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := oci.AttachReferrer(ctx, signed, "1.0.0", artifactType, sig); err != nil {
		t.Fatalf("AttachReferrer: %v", err)
	}

	states := []struct {
		name  string
		store *memory.Store
		kept  [][]byte
		key   []byte // the key the configuration's entry pins
	}{
		{name: "no signature", store: unsigned, kept: nil, key: signerPub},
		{name: "a signature the pinned key verifies", store: signed, kept: [][]byte{sig}, key: signerPub},
		{name: "a signature the pinned key does not verify", store: signed, kept: [][]byte{sig}, key: otherPub},
	}

	entry := func(key []byte, mod func(*workspaceconfig.RegistryConfig)) []workspaceconfig.RegistryConfig {
		r := workspaceconfig.RegistryConfig{Host: host, PublicKey: string(key)}
		mod(&r)
		return []workspaceconfig.RegistryConfig{r}
	}
	keyed := []struct {
		name string
		cfg  func(key []byte) workspaceconfig.Config
	}{
		{"an entry requiring nothing", func(k []byte) workspaceconfig.Config {
			return workspaceconfig.Config{Registries: entry(k, func(*workspaceconfig.RegistryConfig) {})}
		}},
		{"an entry requiring a signature", func(k []byte) workspaceconfig.Config {
			return workspaceconfig.Config{Registries: entry(k, func(r *workspaceconfig.RegistryConfig) { r.Signing.Required = true })}
		}},
		{"an advisory entry requiring a signature", func(k []byte) workspaceconfig.Config {
			return workspaceconfig.Config{Registries: entry(k, func(r *workspaceconfig.RegistryConfig) {
				r.Signing.Required = true
				r.Advisory = true
			})}
		}},
		{"an entry requiring a signature and skipping", func(k []byte) workspaceconfig.Config {
			return workspaceconfig.Config{Registries: entry(k, func(r *workspaceconfig.RegistryConfig) {
				r.Signing.Required = true
				r.SkipSignatureVerification = true
			})}
		}},
		{"the top-level policy over a skipping entry", func(k []byte) workspaceconfig.Config {
			return workspaceconfig.Config{
				Signing:    workspaceconfig.SigningConfig{Required: true},
				Registries: entry(k, func(r *workspaceconfig.RegistryConfig) { r.SkipSignatureVerification = true }),
			}
		}},
		{"the global skip over a requiring entry", func(k []byte) workspaceconfig.Config {
			return workspaceconfig.Config{
				SkipSignatureVerification: true,
				Registries:                entry(k, func(r *workspaceconfig.RegistryConfig) { r.Signing.Required = true }),
			}
		}},
	}

	type cell struct {
		name  string
		cfg   workspaceconfig.Config
		store *memory.Store
		kept  [][]byte
	}
	var cells []cell
	for _, c := range keyed {
		for _, s := range states {
			cells = append(cells, cell{c.name + "/" + s.name, c.cfg(s.key), s.store, s.kept})
		}
	}
	for _, c := range []struct {
		name string
		cfg  workspaceconfig.Config
	}{
		{"no entry and nothing requiring", workspaceconfig.Config{}},
		{"no entry under the top-level policy", workspaceconfig.Config{Signing: workspaceconfig.SigningConfig{Required: true}}},
	} {
		cells = append(cells, cell{c.name + "/no signature", c.cfg, unsigned, nil})
	}

	admitted, refused := 0, 0
	for _, c := range cells {
		t.Run(c.name, func(t *testing.T) {
			ent := pullverify.Entity{URI: host + "/component/x", ManifestDigest: dg, Host: host}
			pulled, perr := pullverify.Verify(ctx, []pullverify.Entity{ent}, pullverify.Options{
				Config: c.cfg, Target: targetForStore(c.store, "1.0.0"),
			})
			read, rerr := pullverify.VerifyStored(c.cfg, ent, c.kept, nil)
			if (perr == nil) != (rerr == nil) {
				t.Fatalf("the pull admitted = %v (%v), the read of the kept signatures admitted = %v (%v)",
					perr == nil, perr, rerr == nil, rerr)
			}
			if perr != nil {
				return
			}
			switch {
			case len(pulled) == 0 && read != nil:
				t.Errorf("the pull recorded no verified fact and the read recorded %+v", read)
			case len(pulled) == 1 && (read == nil || read.BundleDigest != pulled[0].BundleDigest || read.Identity != pulled[0].Identity):
				t.Errorf("the pull recorded %+v and the read recorded %+v", pulled[0], read)
			}
		})
		if _, err := pullverify.VerifyStored(c.cfg, pullverify.Entity{URI: host + "/component/x", ManifestDigest: dg, Host: host}, c.kept, nil); err == nil {
			admitted++
		} else {
			refused++
		}
	}
	// The grid is only evidence if both answers occur in it.
	if admitted == 0 || refused == 0 {
		t.Fatalf("the grid holds %d admitted and %d refused cells, so it cannot tell the two answers apart", admitted, refused)
	}
}
