// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
)

// TestADefaultRegistryCarryingARepositoryPathIsLocationAndItsHostIsTrust pins
// what a resolved default registry carrying a repository path reads as, on every
// layer that can set it: DefaultRegistryLocation splits it into the host every
// trust decision is keyed on and the path every location through it begins with,
// and every per-host lookup — the entry's policy, its signing requirement, the
// registry admission check, the hub's built-in baseline — finds the host's policy
// as it would for the host alone. That is what keeps a path spelling from
// reaching past the policy written for its host.
//
// Each row resolves a real workspace with the environment frozen, so nothing
// ambient reaches it.
func TestADefaultRegistryCarryingARepositoryPathIsLocationAndItsHostIsTrust(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		global, tree string
		env          map[string]string
		want         registryhost.Location
		// entryRequired says the host has an entry requiring a signature, which the
		// prefixed default must carry; hub says the host is the hub, whose baseline
		// applies with no entry at all.
		entryRequired bool
		hub           bool
	}{
		{name: "an owner path in the workspace file, the host's entry beside it",
			tree:          "workspace: {}\ndefault_registry: ghcr.io/acme\nregistries:\n  - host: ghcr.io\n    signing:\n      required: true\n",
			want:          registryhost.Location{Host: "ghcr.io", Path: "acme"},
			entryRequired: true},
		{name: "a nested path through the environment, the host's entry in the file",
			tree:          "workspace: {}\nregistries:\n  - host: 127.0.0.1:5000\n    signing:\n      required: true\n",
			env:           map[string]string{"AGGREGA_DEFAULT_REGISTRY": "127.0.0.1:5000/team/stable"},
			want:          registryhost.Location{Host: "127.0.0.1:5000", Path: "team/stable"},
			entryRequired: true},
		{name: "a path in the global file, the host's entry in the workspace file",
			global:        "default_registry: harbor.example.com/platform\n",
			tree:          "workspace: {}\nregistries:\n  - host: harbor.example.com\n    signing:\n      required: true\n",
			want:          registryhost.Location{Host: "harbor.example.com", Path: "platform"},
			entryRequired: true},
		{name: "a path on the hub, whose baseline needs no entry",
			tree: "workspace: {}\ndefault_registry: hub.aggrega.ai/acme\n",
			want: registryhost.Location{Host: HubBaselineHost, Path: "acme"},
			hub:  true},
		{name: "an entry named exactly like the host it holds, followed by a path",
			tree: "workspace: {}\ndefault_registry: ghcr.io/acme\nregistries:\n  - name: ghcr.io\n    host: ghcr.io\n",
			want: registryhost.Location{Host: "ghcr.io", Path: "acme"}},
		{name: "a host alone reads as a location with no path",
			tree: "workspace: {}\ndefault_registry: registry.example.com\n",
			want: registryhost.Location{Host: "registry.example.com"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := map[string]string{}
			for k, v := range tc.env {
				env[k] = v
			}
			if tc.global != "" {
				globalPath := filepath.Join(t.TempDir(), "config.yaml")
				writeFile(t, globalPath, tc.global)
				env[EnvConfigOverride] = globalPath
			}
			root := t.TempDir()
			writeFile(t, filepath.Join(root, ConfigFilename), tc.tree)

			rw, err := Resolve(root, "", fakeEnv(env))
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			cfg := rw.Config()
			got := cfg.DefaultRegistryLocation()
			if got != tc.want {
				t.Fatalf("DefaultRegistryLocation() = %+v, want %+v", got, tc.want)
			}
			if cfg.DefaultRegistry != tc.want.Prefix() {
				t.Errorf("the resolved default is %q, want it kept as the location %q", cfg.DefaultRegistry, tc.want.Prefix())
			}
			if !cfg.IsConfiguredRegistry(got.Host) {
				t.Errorf("the configured-registry check does not admit the default's host %q", got.Host)
			}
			if got.Path != "" && cfg.IsConfiguredRegistry(got.Path) {
				t.Errorf("the configured-registry check admits the default's path %q as a registry", got.Path)
			}
			policy, found := cfg.RegistryTrustPolicy(got.Host)
			switch {
			case tc.entryRequired:
				if !found || policy.Host != got.Host || !policy.Signing.Required {
					t.Errorf("the trust lookup for %q found=%v host=%q required=%v; want the host's entry", got.Host, found, policy.Host, policy.Signing.Required)
				}
				if !cfg.RegistrySigningRequiredForKind(got.Host, "Component") {
					t.Errorf("the host %q carries no signing requirement, though its entry states one", got.Host)
				}
			case tc.hub:
				if !found || policy.Host != HubBaselineHost {
					t.Errorf("the trust lookup for %q found=%v host=%q; want the hub's baseline", got.Host, found, policy.Host)
				}
			}
			origins, err := rw.ValueOrigins()
			if err != nil {
				t.Fatalf("value origins: %v", err)
			}
			for _, o := range origins {
				if o.Field == "default_registry" && o.Value != tc.want.Prefix() {
					t.Errorf("the provenance report shows default_registry = %q, want %q", o.Value, tc.want.Prefix())
				}
			}
		})
	}
}

// TestADefaultRegistryIsRefusedWhereItIsNeitherAnEntryNorALocation pins the
// refusals a path-bearing default draws at resolution, on the file layer and the
// environment layer alike: an entry's name followed by a path, naming both
// spellings that are written instead; a host in another spelling, naming the
// location to write; and a path that is not a repository path. Each is a
// configuration error naming the layer that set the value, and an ambiguous
// name followed by a path is refused as the ambiguity it is.
func TestADefaultRegistryIsRefusedWhereItIsNeitherAnEntryNorALocation(t *testing.T) {
	t.Parallel()
	const entries = "registries:\n  - name: local\n    host: 127.0.0.1:5000\n"
	cases := []struct {
		name     string
		value    string
		entries  string
		refusal  error
		mentions []string
		absent   []string
	}{
		{name: "an entry's name followed by a path", value: "local/acme", entries: entries,
			refusal: ErrConfigInvalid(),
			mentions: []string{`default_registry "local/acme" is the name of the registries entry "local" followed by a path`,
				"an entry's name stands for that entry's host alone", `write "local" to use the entry`, `its host followed by the path, "127.0.0.1:5000/acme"`}},
		{name: "an entry named like another host, followed by a path", value: "ghcr.io/acme",
			entries: "registries:\n  - name: ghcr.io\n    host: mirror.example.com\n",
			refusal: ErrConfigInvalid(),
			mentions: []string{`is the name of the registries entry "ghcr.io" followed by a path`, `write "ghcr.io" to use the entry`,
				`its host followed by the path, "mirror.example.com/acme"`}},
		{name: "an entry's name followed by something that is not a repository path", value: "local/Acme", entries: entries,
			refusal:  ErrConfigInvalid(),
			mentions: []string{`write "local" to use the entry`, `its host, "127.0.0.1:5000", followed by a repository path`}},
		{name: "a name two entries carry, followed by a path", value: "shared/acme",
			entries: "registries:\n  - name: shared\n    host: a.example.com\n  - name: shared\n    host: b.example.com\n",
			refusal: ErrAmbiguousRegistryAlias(), mentions: []string{`registry alias "shared" matches 2 hosts`}},
		{name: "a host in capitals before a path", value: "GHCR.io/acme",
			refusal: ErrConfigInvalid(), mentions: []string{`write "ghcr.io/acme"`}},
		{name: "credentials before a path", value: "user:secret@ghcr.io/acme",
			refusal: ErrConfigInvalid(), mentions: []string{`write "ghcr.io/acme"`}, absent: []string{"secret"}},
		{name: "a tag after the path", value: "ghcr.io/acme:1.0.0",
			refusal: ErrConfigInvalid(), mentions: []string{`names the registry host "ghcr.io" followed by something that is not a repository path`}},
		{name: "a single name with a path, no entry of that name", value: "nowhere/acme", entries: entries,
			refusal: ErrConfigInvalid(), mentions: []string{"is not a registry host, alone or followed by a repository path", "the name of an entry under registries"}},
	}
	for _, tc := range cases {
		for _, layer := range []string{"file", "environment"} {
			t.Run(tc.name+"/"+layer, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				path := filepath.Join(root, ConfigFilename)
				doc := "workspace: {}\n" + tc.entries
				env := map[string]string{}
				names := path
				if layer == "file" {
					doc += "default_registry: " + tc.value + "\n"
				} else {
					env["AGGREGA_DEFAULT_REGISTRY"] = tc.value
					names = "$AGGREGA_DEFAULT_REGISTRY"
				}
				writeFile(t, path, doc)
				_, err := Resolve(root, "", fakeEnv(env))
				if !errors.Is(err, tc.refusal) {
					t.Fatalf("got %v, want the resolution refused with %v", err, tc.refusal)
				}
				for _, m := range append([]string{names, "default_registry"}, tc.mentions...) {
					if !strings.Contains(err.Error(), m) {
						t.Errorf("the refusal does not carry %q: %v", m, err)
					}
				}
				for _, a := range tc.absent {
					if strings.Contains(err.Error(), a) {
						t.Errorf("the refusal repeats %q: %v", a, err)
					}
				}
			})
		}
	}
}
