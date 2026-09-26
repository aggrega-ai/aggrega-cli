// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginfetchverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestEveryHostTheLoadAcceptsIsVerifiedUnderItsOwnPolicy holds the invariant the
// one reading of a registry host exists for: no spelling a configuration may name
// both loads and escapes verification.
//
// For each spelling the load accepts it resolves a real configuration naming the
// spelling as the default registry — once in a file and once through the
// environment variable, which is applied after every file is validated — with an
// entry for the same spelling requiring a signature. It then walks the decision
// chain the closure signature pass composes: the location binding, the host the
// location names, the verification filter, and the trust lookup, which must find
// the entry configured under that very spelling. And it runs the pass itself over
// an unsigned artifact fetched from that host, which must halt the pull.
//
// The single-label row is the one the verification filter used to drop: a bare
// localhost loaded, fetched, and was skipped with nothing said, so an unsigned
// artifact from it passed even with a signature required.
func TestEveryHostTheLoadAcceptsIsVerifiedUnderItsOwnPolicy(t *testing.T) {
	t.Parallel()
	hosts := []string{
		"hub.aggrega.ai",
		"localhost",
		"localhost:5000",
		"127.0.0.1:5000",
		"[::1]:5000",
		"registry.example.com",
		"registry.example.com:8443",
		"registry:5000",
	}
	for _, host := range hosts {
		for _, layer := range []string{"file", "environment"} {
			t.Run(layer+"/"+host, func(t *testing.T) {
				t.Parallel()
				cfg := resolveWithDefaultRegistry(t, host, layer)
				if cfg.DefaultRegistry != host {
					t.Fatalf("the resolved default registry is %q, want %q", cfg.DefaultRegistry, host)
				}

				const identity = "core/web-api"
				location, bound := catalog.BindEntityLocation(cfg.DefaultRegistry, "component", identity)
				if !bound {
					t.Fatalf("the default registry %q binds no location", host)
				}
				if got := registryhost.Of(location); got != host {
					t.Fatalf("the location %q names the host %q, want %q", location, got, host)
				}
				if !pluginfetchverify.IsRegistryHost(registryhost.Of(location)) {
					t.Fatalf("the verification filter skips %q, a host the configuration load accepted", host)
				}
				policy, found := cfg.RegistryTrustPolicy(registryhost.Of(location))
				if !found || policy.Host != host || !policy.Signing.Required {
					t.Fatalf("the trust lookup for %q found=%v host=%q required=%v; want the entry configured under that spelling",
						host, found, policy.Host, policy.Signing.Required)
				}

				// End to end through the pass: an unsigned artifact from this host.
				ctx := context.Background()
				store := memory.New()
				artifact := []byte("kind: Component\n")
				contentDg := digest.ContentDigest(artifact)
				manifestDg, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, artifact, nil)
				if err != nil {
					t.Fatalf("push the unsigned artifact: %v", err)
				}
				cl := closure.Closure{Entities: []closure.ResolvedRef{{
					URI: identity, KindHint: "component", Digest: contentDg, ManifestDigest: manifestDg,
					ResolutionPath: "tier-3-pull",
				}}}
				lf := &lockfile.Lockfile{Entities: []lockfile.Entity{{URI: identity, Digest: contentDg, ManifestDigest: manifestDg}}}
				var asked pullverify.Entity
				targetFor := func(e pullverify.Entity) (oras.ReadOnlyTarget, string, error) {
					asked = e
					return store, "1.0.0", nil
				}
				err = VerifyClosureSignatures(ctx, lf, cl, cfg, t.TempDir(), io.Discard, targetFor)
				if !errors.Is(err, signing.ErrNoSignatureFound()) {
					t.Fatalf("an unsigned artifact from %q under a required signature: got %v, want the pull halted with ErrNoSignatureFound", host, err)
				}
				if asked.Host != host || asked.Location != location {
					t.Errorf("the pass verified host %q at %q, want %q at %q", asked.Host, asked.Location, host, location)
				}
			})
		}
	}
}

// resolveWithDefaultRegistry resolves a real workspace whose default registry is
// host — written in the file, or supplied through AGGREGA_DEFAULT_REGISTRY over a
// file that names none — and whose one registry entry, keyed under the same
// spelling, requires a signature. The environment lookup is frozen, so no
// ambient variable and no global configuration file reaches the resolution.
func resolveWithDefaultRegistry(t *testing.T, host, layer string) workspaceconfig.Config {
	t.Helper()
	root := t.TempDir()
	doc := "workspace: {}\nregistries:\n  - host: " + strconv.Quote(host) + "\n    signing:\n      required: true\n"
	env := map[string]string{}
	switch layer {
	case "file":
		doc += "default_registry: " + strconv.Quote(host) + "\n"
	case "environment":
		env["AGGREGA_DEFAULT_REGISTRY"] = host
	}
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(doc), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	rw, err := workspaceconfig.Resolve(root, "", func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("resolve a configuration naming %q (%s): %v", host, layer, err)
	}
	return rw.Config()
}
