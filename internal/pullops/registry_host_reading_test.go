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
// entry for the same spelling requiring a signature; and then twice more with the
// default written as that entry's name instead, in the file and through the
// variable, which the resolution must read as the entry's host. It then walks the decision
// chain the closure signature pass composes: the location binding, the host the
// location names, the verification filter, and the trust lookup, which must find
// the entry configured under that very spelling. And it runs the pass itself over
// an unsigned artifact fetched from that host, which must halt the pull.
//
// The single-label row is the one the verification filter used to drop: a bare
// localhost loaded, fetched, and was skipped with nothing said, so an unsigned
// artifact from it passed even with a signature required. The name rows are the
// same hazard one step earlier: a default read as the name it is written as,
// rather than as the host its entry names, binds a location at a host named
// after the alias, which the filter reads as no registry at all.
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
		for _, layer := range []string{"file", "environment", "name in the file", "name through the environment"} {
			t.Run(layer+"/"+host, func(t *testing.T) {
				t.Parallel()
				cfg := resolveWithDefaultRegistry(t, host, layer)
				if cfg.DefaultRegistry != host {
					t.Fatalf("the resolved default registry is %q, want %q", cfg.DefaultRegistry, host)
				}

				const identity = "core/web-api"
				location, bound := catalog.BindEntityLocation(cfg.DefaultRegistryLocation().Prefix(), "component", identity)
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
				err = VerifyClosureSignatures(ctx, lf, cl, cfg, t.TempDir(), ".aggrega/cache", io.Discard, targetFor)
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

// TestEveryPrefixedDefaultIsVerifiedUnderItsHostsPolicy holds the same invariant
// for a default registry carrying a repository path: the path is where an entity
// is fetched from, and never part of the key its trust policy is found under.
//
// For each host the load accepts it resolves a real configuration whose default
// is that host followed by a repository path — in a file and through the
// environment variable — beside one entry for the host alone requiring a
// signature. The location a host-less identity binds must sit under the path; the
// host read out of it must be the entry's host exactly; the verification filter
// must read it as a registry; the trust lookup must find the entry; and the pass
// itself, over an unsigned artifact, must halt the pull having asked for it at
// the prefixed location and verified it against the host alone. The entry's name
// followed by the same path must be refused where it is configured, since a name
// stands for a host and not for a location under it.
func TestEveryPrefixedDefaultIsVerifiedUnderItsHostsPolicy(t *testing.T) {
	t.Parallel()
	hosts := []string{
		"hub.aggrega.ai",
		"localhost",
		"localhost:5000",
		"127.0.0.1:5000",
		"[::1]:5000",
		"registry.example.com:8443",
		"registry:5000",
	}
	const path = "acme/catalog"
	for _, host := range hosts {
		for _, layer := range []string{"file", "environment"} {
			t.Run(layer+"/"+host, func(t *testing.T) {
				t.Parallel()
				cfg, err := resolveWithDefault(t, host, host+"/"+path, layer)
				if err != nil {
					t.Fatalf("resolve a default %q/%s (%s): %v", host, path, layer, err)
				}
				if got := cfg.DefaultRegistryLocation(); got.Host != host || got.Path != path {
					t.Fatalf("the default reads as %+v, want host %q and path %q", got, host, path)
				}

				const identity = "core/web-api"
				location, bound := catalog.BindEntityLocation(cfg.DefaultRegistryLocation().Prefix(), "component", identity)
				if !bound {
					t.Fatalf("the default %q/%s binds no location", host, path)
				}
				if want := host + "/" + path + "/component/core/web-api"; location != want {
					t.Fatalf("the location is %q, want it under the default's path, %q", location, want)
				}
				if got := registryhost.Of(location); got != host {
					t.Fatalf("the location %q names the host %q, want %q", location, got, host)
				}
				if !pluginfetchverify.IsRegistryHost(registryhost.Of(location)) {
					t.Fatalf("the verification filter skips %q, a host the configuration load accepted", host)
				}
				policy, found := cfg.RegistryTrustPolicy(registryhost.Of(location))
				if !found || policy.Host != host || !policy.Signing.Required {
					t.Fatalf("the trust lookup for %q found=%v host=%q required=%v; want the entry configured for the host",
						host, found, policy.Host, policy.Signing.Required)
				}

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
				err = VerifyClosureSignatures(ctx, lf, cl, cfg, t.TempDir(), ".aggrega/cache", io.Discard, targetFor)
				if !errors.Is(err, signing.ErrNoSignatureFound()) {
					t.Fatalf("an unsigned artifact from %q/%s under the host's required signature: got %v, want the pull halted with ErrNoSignatureFound", host, path, err)
				}
				if asked.Host != host || asked.Location != location {
					t.Errorf("the pass verified host %q at %q, want %q at %q", asked.Host, asked.Location, host, location)
				}

				if _, err := resolveWithDefault(t, host, defaultEntryName+"/"+path, layer); !errors.Is(err, workspaceconfig.ErrConfigInvalid()) {
					t.Errorf("the entry's name followed by a path (%s): got %v, want it refused as a configuration error", layer, err)
				}
			})
		}
	}
}

// defaultEntryName is the name the one registry entry carries, which a default
// registry may be written as. It is a single name with no port — the shape that,
// read as a host, binds a location the verification filter does not read as a
// registry — so a default read as its name rather than its entry's host fails
// here rather than passing unverified.
const defaultEntryName = "local-registry"

// resolveWithDefaultRegistry resolves a real workspace whose one registry entry,
// keyed under host and named defaultEntryName, requires a signature, and whose
// default registry names it — as the host, or as the entry's name, written in the
// file or supplied through AGGREGA_DEFAULT_REGISTRY over a file that names none.
// The environment lookup is frozen, so no ambient variable and no global
// configuration file reaches the resolution.
func resolveWithDefaultRegistry(t *testing.T, host, layer string) workspaceconfig.Config {
	t.Helper()
	value := host
	switch layer {
	case "name in the file":
		value, layer = defaultEntryName, "file"
	case "name through the environment":
		value, layer = defaultEntryName, "environment"
	}
	cfg, err := resolveWithDefault(t, host, value, layer)
	if err != nil {
		t.Fatalf("resolve a configuration naming %q (%s): %v", host, layer, err)
	}
	return cfg
}

// resolveWithDefault resolves a real workspace whose one registry entry, keyed
// under host and named defaultEntryName, requires a signature, and whose default
// registry is value — written in the file, or supplied through
// AGGREGA_DEFAULT_REGISTRY over a file that names none. The environment lookup
// is frozen, so no ambient variable and no global configuration file reaches the
// resolution. It returns the resolution's error rather than failing on it, so a
// caller can assert a refusal.
func resolveWithDefault(t *testing.T, host, value, layer string) (workspaceconfig.Config, error) {
	t.Helper()
	root := t.TempDir()
	doc := "workspace: {}\nregistries:\n  - host: " + strconv.Quote(host) + "\n    name: " + defaultEntryName +
		"\n    signing:\n      required: true\n"
	env := map[string]string{}
	switch layer {
	case "file":
		doc += "default_registry: " + strconv.Quote(value) + "\n"
	case "environment":
		env["AGGREGA_DEFAULT_REGISTRY"] = value
	default:
		t.Fatalf("unknown layer %q", layer)
	}
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(doc), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	rw, err := workspaceconfig.Resolve(root, "", func(k string) string { return env[k] })
	if err != nil {
		return workspaceconfig.Config{}, err
	}
	return rw.Config(), nil
}
