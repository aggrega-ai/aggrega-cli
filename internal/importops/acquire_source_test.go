// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// acquiredSourceImporter is the core importer whose fetch location these cases
// read. Which one it is does not matter: the location is bound the same way for
// every core importer.
var acquiredSourceImporter = types.Plugin{
	Metadata: types.PluginMetadata{Name: "langflow", Version: "1.0.0", PluginType: "importer"},
}

// TestACoreImporterIsFetchedOnlyFromARegistryTheWorkspaceIsConfiguredWith resolves
// a workspace for every way its default registry can be written — through the
// resolution every surface reads, with the configuration file, the environment
// variable, or neither — and holds carriedImporterSource, the one place an import
// decides which registry it reaches, to the bound a registry read over the
// assistant tools' listener is held to.
//
// For each row the location is under the default registry's own location prefix,
// so it is the default registry's host the fetch contacts and its signature is
// asked about, and the configuration's own answer to whether a host is a
// registry it is configured with — the question the listener asks before it lets
// a registry read go ahead — admits that host. Where the workspace names no
// default registry there is no location at all, and the acquisition refuses
// before anything is dialled, naming the remedy that puts the module on the
// machine without a registry.
//
// So a fetch an import makes on a caller's behalf cannot reach a registry the
// listener would refuse to read for that caller: the refusal would never fire,
// and the import carries none.
//
// 🔴 PLANTED DEFECT: have carriedImporterSource (internal/importops/acquire.go)
// bind the identity under any registry but the default registry location — the
// built-in one whatever the configuration says, for instance — and every row but
// the built-in one fails, naming a host that is not the default registry's; bind
// it under a host taken from anywhere but the configuration, and the admission
// fails too.
func TestACoreImporterIsFetchedOnlyFromARegistryTheWorkspaceIsConfiguredWith(t *testing.T) {
	t.Parallel()
	builtIn := workspaceconfig.Default().DefaultRegistryLocation().Host

	for _, row := range []struct {
		name string
		// config is written beneath the workspace marker.
		config string
		// env is the environment the resolution reads, beside the redirect of its
		// global layer.
		env map[string]string
		// host is the registry host the default resolves to, and empty where the
		// workspace names no default registry.
		host string
	}{
		{name: "the built-in default, written nowhere", host: builtIn},
		{name: "a host with a port", config: "default_registry: \"127.0.0.1:5000\"\n", host: "127.0.0.1:5000"},
		{
			name:   "a host followed by a repository path",
			config: "default_registry: \"registry.example.test/team/platform\"\n",
			host:   "registry.example.test",
		},
		{
			name: "the name of an entry under registries",
			config: "default_registry: team\nregistries:\n" +
				"  - host: \"registry.example.test:5000\"\n    name: team\n",
			host: "registry.example.test:5000",
		},
		{
			name: "the name of an entry whose host another entry is named after",
			config: "default_registry: team\nregistries:\n" +
				"  - host: \"a.example.test\"\n    name: team\n" +
				"  - host: \"b.example.test\"\n    name: \"a.example.test\"\n",
			host: "a.example.test",
		},
		{
			name: "a host the environment variable sets",
			env:  map[string]string{"AGGREGA_DEFAULT_REGISTRY": "env.example.test:8443"},
			host: "env.example.test:8443",
		},
		{
			name:   "the name of an entry the environment variable sets",
			config: "registries:\n  - host: \"registry.example.test:5000\"\n    name: team\n",
			env:    map[string]string{"AGGREGA_DEFAULT_REGISTRY": "team"},
			host:   "registry.example.test:5000",
		},
		{name: "written empty, naming no default registry", config: "default_registry: \"\"\n"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			wsDir := aWorkspaceDir(t)
			if err := os.WriteFile(filepath.Join(wsDir, workspaceconfig.ConfigFilename),
				[]byte("workspace: {}\noffline: true\n"+row.config), 0o644); err != nil {
				t.Fatalf("write the workspace configuration: %v", err)
			}
			rw := resolveWorkspaceAtWith(t, wsDir, row.env)
			wsCfg := rw.Config()
			uri := acquiredSourceImporter.ArtifactReference()

			source, bound := carriedImporterSource(wsCfg, uri)
			if row.host == "" {
				if bound {
					t.Fatalf("a workspace naming no default registry bound the importer to %+v", source)
				}
				// The acquisition refuses before its egress gate is asked anything, so
				// an online workspace would dial nothing either; this one is offline
				// only so that a refusal coming later than that would still dial nothing.
				err := acquireCarriedImporter(context.Background(), rw, acquiredSourceImporter, unmatchedDigest)
				if !errors.Is(err, pluginpath.ErrPluginArtifactNotFound()) {
					t.Fatalf("an acquisition with no default registry was not refused as a plugin not on this machine: %v", err)
				}
				for _, part := range []string{"the workspace names no default registry to fetch it from", "in a catalog folder"} {
					if !strings.Contains(err.Error(), part) {
						t.Errorf("the refusal does not carry %q: %v", part, err)
					}
				}
				if strings.Contains(err.Error(), "with the network available") {
					t.Errorf("the refusal names a run with the network as a remedy, though no registry is named: %v", err)
				}
				return
			}
			if !bound {
				t.Fatalf("the importer was bound to no location, though the default registry is %q", row.host)
			}
			location := wsCfg.DefaultRegistryLocation()
			if location.Host != row.host {
				t.Fatalf("the default registry resolved to the host %q; this row writes it as %q", location.Host, row.host)
			}
			if source.Host != row.host {
				t.Errorf("the importer is fetched from the host %q; the default registry's is %q", source.Host, row.host)
			}
			if !strings.HasPrefix(source.Location, location.Prefix()+"/") {
				t.Errorf("the importer is fetched from %q, which is not under the default registry location %q",
					source.Location, location.Prefix())
			}
			if !wsCfg.IsConfiguredRegistry(source.Host) {
				t.Errorf("the host the importer is fetched from, %q, is not one the workspace is configured with, so "+
					"a registry read for it over the listener would be refused", source.Host)
			}
		})
	}
}
