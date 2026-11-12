// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// TestADefaultRegistryNamingAnEntryIsResolvedOnceToThatEntrysHost pins where a
// default registry written as an entry's name is resolved, and what every reader
// of the resolved configuration then sees.
//
// The resolution runs once, over every layer, so the entry a name refers to may
// be declared by a layer other than the one that set the default — the global
// file beneath a workspace file, or the other way round — and a nearer layer
// whose registries list replaces a farther one's leaves the farther entries
// unreachable. Each row resolves a real workspace, with the environment frozen
// so nothing ambient reaches it, and states the host the default must resolve
// to, or the condition it must be refused with and the layer the refusal names.
func TestADefaultRegistryNamingAnEntryIsResolvedOnceToThatEntrysHost(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		// global and tree are the two configuration files; an empty one is not
		// written.
		global, tree string
		env          map[string]string
		// host is the resolved default, where the resolution accepts it.
		host string
		// refusal is the condition a refused resolution carries, and inGlobal says
		// whether the refusal names the global file rather than the tree's.
		refusal  error
		inGlobal bool
		// mentions are substrings the refusal carries.
		mentions []string
	}{
		{name: "a name in the workspace file, its entry in the global file",
			global: "registries:\n  - name: corp\n    host: registry.corp.example:5000\n",
			tree:   "workspace: {}\ndefault_registry: corp\n",
			host:   "registry.corp.example:5000"},
		{name: "a name in the global file, its entry in the workspace file",
			global: "default_registry: corp\n",
			tree:   "workspace: {}\nregistries:\n  - name: corp\n    host: registry.corp.example:5000\n",
			host:   "registry.corp.example:5000"},
		{name: "a name the environment sets, its entry in the workspace file",
			tree: "workspace: {}\nregistries:\n  - name: local\n    host: 127.0.0.1:5000\n",
			env:  map[string]string{"AGGREGA_DEFAULT_REGISTRY": "local"},
			host: "127.0.0.1:5000"},
		{name: "the built-in default, with an entry named like it",
			tree: "workspace: {}\nregistries:\n  - name: hub.aggrega.ai\n    host: mirror.example.com:8443\n",
			host: "mirror.example.com:8443"},
		{name: "a host no entry is named after stays that host",
			tree: "workspace: {}\ndefault_registry: registry.example.com\nregistries:\n  - name: local\n    host: 127.0.0.1:5000\n",
			host: "registry.example.com"},
		{name: "a name whose entry a nearer layer's list replaced",
			global:   "default_registry: corp\nregistries:\n  - name: corp\n    host: registry.corp.example:5000\n",
			tree:     "workspace: {}\nregistries:\n  - host: registry.example.com\n",
			refusal:  ErrConfigInvalid(),
			inGlobal: true,
			mentions: []string{`default_registry "corp"`, "the name of an entry under registries"}},
		{name: "a name two entries carry",
			tree: "workspace: {}\ndefault_registry: shared\nregistries:\n" +
				"  - name: shared\n    host: a.example.com\n  - name: shared\n    host: b.example.com\n",
			refusal:  ErrAmbiguousRegistryAlias(),
			mentions: []string{"default_registry", `registry alias "shared" matches 2 hosts`, "a.example.com", "b.example.com"}},
		{name: "a host with a repository path is that location",
			tree: "workspace: {}\ndefault_registry: ghcr.io/acme/catalog\n",
			host: "ghcr.io/acme/catalog"},
		{name: "an entry's name followed by a path",
			tree:     "workspace: {}\ndefault_registry: local/acme\nregistries:\n  - name: local\n    host: 127.0.0.1:5000\n",
			refusal:  ErrConfigInvalid(),
			mentions: []string{`default_registry "local/acme" is the name of the registries entry "local" followed by a path`, `write "local"`, `"127.0.0.1:5000/acme"`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := map[string]string{}
			for k, v := range tc.env {
				env[k] = v
			}
			var globalPath string
			if tc.global != "" {
				globalPath = filepath.Join(t.TempDir(), "config.yaml")
				writeFile(t, globalPath, tc.global)
				env[EnvConfigOverride] = globalPath
			}
			root := t.TempDir()
			treePath := filepath.Join(root, ConfigFilename)
			writeFile(t, treePath, tc.tree)

			rw, err := Resolve(root, "", fakeEnv(env))
			if tc.refusal != nil {
				if !errors.Is(err, tc.refusal) {
					t.Fatalf("got %v, want the resolution refused with %v", err, tc.refusal)
				}
				names := treePath
				if tc.inGlobal {
					names = globalPath
				}
				for _, m := range append([]string{names}, tc.mentions...) {
					if !strings.Contains(err.Error(), m) {
						t.Errorf("the refusal does not carry %q: %v", m, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			cfg := rw.Config()
			if cfg.DefaultRegistry != tc.host {
				t.Fatalf("the resolved default is %q, want %q", cfg.DefaultRegistry, tc.host)
			}
			// Every reader sees the one host: the admission check admits it, and
			// the provenance report shows it as the effective value.
			if !cfg.IsConfiguredRegistry(tc.host) {
				t.Errorf("the configured-registry check does not admit the resolved default %q", tc.host)
			}
			origins, err := rw.ValueOrigins()
			if err != nil {
				t.Fatalf("value origins: %v", err)
			}
			for _, o := range origins {
				if o.Field == "default_registry" && o.Value != tc.host {
					t.Errorf("the provenance report shows default_registry = %q, want the resolved host %q", o.Value, tc.host)
				}
			}
		})
	}
}

// TestRemovingTheEntryADefaultNamesLeavesTheDefaultNamingNothing pins what the
// registry removal documents: it leaves default_registry as written, so a
// default written as the removed entry's name names nothing afterwards, and the
// next resolution refuses it, naming the file, rather than reading the name as a
// host.
func TestRemovingTheEntryADefaultNamesLeavesTheDefaultNamingNothing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, ConfigFilename)
	writeFile(t, path, "workspace: {}\ndefault_registry: local\nregistries:\n  - name: local\n    host: 127.0.0.1:5000\n")
	rw, err := Resolve(root, "", fakeEnv(nil))
	if err != nil {
		t.Fatalf("resolve before the removal: %v", err)
	}
	if got := rw.Config().DefaultRegistry; got != "127.0.0.1:5000" {
		t.Fatalf("the default resolves to %q before the removal, want the entry's host", got)
	}
	removed, err := RemoveRegistry(path, "local")
	if err != nil || !removed {
		t.Fatalf("remove the entry: removed=%v err=%v", removed, err)
	}
	_, err = Resolve(root, "", fakeEnv(nil))
	if !errors.Is(err, ErrConfigInvalid()) {
		t.Fatalf("got %v, want the default that now names nothing refused", err)
	}
	if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), `default_registry "local"`) {
		t.Errorf("the refusal does not name the file and the value: %v", err)
	}
}
