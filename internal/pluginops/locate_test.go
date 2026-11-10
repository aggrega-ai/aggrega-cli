// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginops

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// countingRegistry is a registry whose catalog walk and per-repository tag
// listings are counted, so a test can say which of the two an addition reached.
// tags maps a repository path to the tags it lists; a repository absent from it
// lists none, as a registry answers for a repository it does not hold.
type countingRegistry struct {
	repos    []string
	tags     map[string][]string
	walks    int
	listings []string
}

func (r *countingRegistry) Repositories(_ context.Context, _ string, fn func(repos []string) error) error {
	r.walks++
	return fn(r.repos)
}

// withCountingRegistry points the registry, tag-listing and withdrawal seams at
// reg for the duration of the test. Withdrawal reads answer that nothing is
// withdrawn, so no row reaches a live registry.
func withCountingRegistry(t *testing.T, reg *countingRegistry) {
	t.Helper()
	withFakeRegistry(t, nil, nil)
	RegistryTarget = func(workspaceconfig.Config) (registryops.RepoLister, string, error) {
		return reg, "hub.aggrega.ai", nil
	}
	RepoTagLister = func(_ context.Context, _ workspaceconfig.Config, _, repoPath string) ([]string, error) {
		reg.listings = append(reg.listings, repoPath)
		return reg.tags[repoPath], nil
	}
}

// refusingRegistry points every registry seam at a function that fails the test,
// so a row proves its refusal was reached without any registry.
func refusingRegistry(t *testing.T) {
	t.Helper()
	withFakeRegistry(t, nil, nil)
	RegistryTarget = func(workspaceconfig.Config) (registryops.RepoLister, string, error) {
		t.Error("the registry was reached for a refusal decidable without it")
		return nil, "", context.Canceled
	}
	RepoTagLister = func(context.Context, workspaceconfig.Config, string, string) ([]string, error) {
		t.Error("a tag listing was made for a refusal decidable without it")
		return nil, context.Canceled
	}
}

// resolveNoWorkspace resolves a temporary folder nobody marked as a workspace,
// with lookup as the environment, and fails unless the resolution found no
// workspace — the precondition every row using it rests on.
func resolveNoWorkspace(t *testing.T, lookup func(string) string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	folder := t.TempDir()
	rw, err := workspaceconfig.Resolve(folder, "", lookup)
	if err != nil {
		t.Fatalf("resolve %s: %v", folder, err)
	}
	if !rw.FoundNoWorkspace() {
		t.Fatalf("precondition: %s resolved a workspace at %s, the row needs a folder that is none", folder, rw.Root())
	}
	return rw
}

// machineLookup is an environment naming a home directory, a machine-wide cache
// directory and a global configuration file, all inside the test's own scratch
// tree, so the machine-wide cache resolves on every platform's rule without
// reaching the developer's own.
func machineLookup(t *testing.T) func(string) string {
	t.Helper()
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")
	if err := writeFileAtomic(globalPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("seed the global configuration: %v", err)
	}
	return func(name string) string {
		switch name {
		case "AGGREGA_CONFIG":
			return globalPath
		case "HOME":
			return filepath.Join(dir, "home")
		case "XDG_CACHE_HOME":
			return filepath.Join(dir, "cache")
		case "LOCALAPPDATA":
			return filepath.Join(dir, "localappdata")
		}
		return ""
	}
}

// TestAPluginNameIsLookedUpBeforeAnyCatalogWalk pins the lookup order: a query
// that is a plugin's name is found with one tag listing of that plugin's
// repository and never walks the registry's catalog; only a query no plugin is
// named after is matched as a part of a name, by the walk; and a query naming a
// version is never matched that way.
//
// 🔴 PLANTED DEFECTS: in locatePlugin, skip the direct listing (go straight to
// EnumerateRepositories) and the rows "a plugin's name" and "a name and a published
// version" fail on the walk count; drop the `q.version != ""` refusal and the row
// "a version pinned to a name nothing publishes" walks and resolves the substring
// match instead.
func TestAPluginNameIsLookedUpBeforeAnyCatalogWalk(t *testing.T) {
	const moo = "plugin/cli/moo"
	for _, tc := range []struct {
		name         string
		query        string
		wantRef      string
		wantErr      string
		wantWalks    int
		wantListings []string
	}{
		{
			name: "a plugin's name", query: "moo",
			wantRef: "oci://hub.aggrega.ai/plugin/cli/moo:0.2.4", wantListings: []string{moo},
		},
		{
			name: "a name and a published version", query: "moo:0.2.3",
			wantRef: "oci://hub.aggrega.ai/plugin/cli/moo:0.2.3", wantListings: []string{moo},
		},
		{
			name: "a part of a name falls back to the walk", query: "mo",
			wantRef: "oci://hub.aggrega.ai/plugin/cli/moo:0.2.4", wantWalks: 1,
			wantListings: []string{"plugin/cli/mo", moo},
		},
		{
			name: "a version the plugin does not publish", query: "moo:9.9.9",
			wantErr: "plugin/cli/moo is not published at version 9.9.9; its published versions are 0.2.3, 0.2.4", wantListings: []string{moo},
		},
		{
			name: "a version pinned to a name nothing publishes", query: "mo:1.0.0",
			wantErr: `no cli plugin named "mo" is published in hub.aggrega.ai`, wantListings: []string{"plugin/cli/mo"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := &countingRegistry{
				repos: []string{moo, "plugin/governance/moo-policy"},
				tags:  map[string][]string{moo: {"0.2.3", "0.2.4"}},
			}
			withCountingRegistry(t, reg)
			rw := resolveScratch(t, scratchConfig)

			var got CLIInstallRequest
			res, err := Add(context.Background(), rw, AddOptions{
				Socket: "cli", Query: tc.query, AcceptCapabilities: true,
				InstallCLIPlugin: func(_ context.Context, _ *workspaceconfig.ResolvedWorkspace, req CLIInstallRequest) error {
					got = req
					return nil
				},
			})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Add(%q) = %v; want a refusal containing %q", tc.query, err, tc.wantErr)
				}
			} else {
				if err != nil {
					t.Fatalf("Add(%q): %v", tc.query, err)
				}
				if got.Reference != tc.wantRef || res.Reference != tc.wantRef {
					t.Errorf("Add(%q) installed %q (result %q); want %q", tc.query, got.Reference, res.Reference, tc.wantRef)
				}
			}
			if reg.walks != tc.wantWalks {
				t.Errorf("Add(%q) walked the catalog %d time(s); want %d", tc.query, reg.walks, tc.wantWalks)
			}
			if !slices.Equal(reg.listings, tc.wantListings) {
				t.Errorf("Add(%q) listed the tags of %q; want %q", tc.query, reg.listings, tc.wantListings)
			}
		})
	}
}

// TestARefusalDecidableWithoutARegistryReachesNone drives every refusal an
// addition can reach from its arguments and its resolution alone, against
// registry seams that fail the test when reached, so each is shown to come before
// any network.
//
// 🔴 PLANTED DEFECTS: move parsePluginQuery after RegistryTarget in Add and the
// version rows fail on the reach; delete the refuseWorkspaceFileOutsideAWorkspace
// call in Add and the "outside a workspace" socket row reaches the registry; delete
// the CommandExtensionCache check in addCLI and the "no machine-wide cache" row
// reaches it.
func TestARefusalDecidableWithoutARegistryReachesNone(t *testing.T) {
	install := func(context.Context, *workspaceconfig.ResolvedWorkspace, CLIInstallRequest) error {
		t.Error("the installer ran for a refused addition")
		return nil
	}
	inWorkspace := func(t *testing.T) *workspaceconfig.ResolvedWorkspace { return resolveScratch(t, scratchConfig) }
	outside := func(t *testing.T) *workspaceconfig.ResolvedWorkspace { return resolveNoWorkspace(t, machineLookup(t)) }
	outsideNoCache := func(t *testing.T) *workspaceconfig.ResolvedWorkspace {
		look := machineLookup(t)
		return resolveNoWorkspace(t, func(name string) string {
			switch name {
			case "HOME", "XDG_CACHE_HOME", "LOCALAPPDATA":
				return ""
			}
			return look(name)
		})
	}
	for _, tc := range []struct {
		name    string
		resolve func(*testing.T) *workspaceconfig.ResolvedWorkspace
		opts    AddOptions
		want    string
	}{
		{"a version that is not one", inWorkspace,
			AddOptions{Socket: "cli", Query: "moo:latest"}, `names the version "latest", which is not a version`},
		{"a version pinned to a part of a name", inWorkspace,
			AddOptions{Socket: "linter", Query: "Style:1.0.0"}, `pins a version to "Style", which is not a plugin name`},
		{"a version on the provider type", inWorkspace,
			AddOptions{Socket: ProviderSocket, Query: "helm:1.0.0", Capability: "helm"}, "a provider is bound by its name alone"},
		{"a socket attachment outside a workspace", outside,
			AddOptions{Socket: "governance", Query: "rego", Role: "render-verdict"}, "is not inside an Aggrega workspace"},
		{"a command extension pinned to the workspace outside one", outside,
			AddOptions{Socket: "cli", Query: "moo", Workspace: true}, "is not inside an Aggrega workspace"},
		{"a command extension outside a workspace with no machine-wide cache", outsideNoCache,
			AddOptions{Socket: "cli", Query: "moo"}, "no machine-wide cache location resolves"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refusingRegistry(t)
			opts := tc.opts
			opts.InstallCLIPlugin = install
			_, err := Add(context.Background(), tc.resolve(t), opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Add = %v; want a refusal containing %q", err, tc.want)
			}
		})
	}
}

// TestACommandExtensionIsAddedOutsideAWorkspace adds a command extension from a
// folder that is no workspace: the addition succeeds, records the plugin in the
// global configuration, and the installer is handed the resolution whose
// command-extension cache is the machine-wide one, so nothing is written to the
// folder.
//
// 🔴 PLANTED DEFECT: return the workspace's own cache from
// ResolvedWorkspace.CommandExtensionCache whatever the resolution found, and the
// cache assertion fails naming the folder.
func TestACommandExtensionIsAddedOutsideAWorkspace(t *testing.T) {
	reg := &countingRegistry{tags: map[string][]string{"plugin/cli/moo": {"0.2.4"}}}
	withCountingRegistry(t, reg)
	rw := resolveNoWorkspace(t, machineLookup(t))

	var cacheRoot, cacheDir string
	res, err := Add(context.Background(), rw, AddOptions{
		Socket: "cli", Query: "moo", AcceptCapabilities: true,
		InstallCLIPlugin: func(_ context.Context, rw *workspaceconfig.ResolvedWorkspace, _ CLIInstallRequest) error {
			var cerr error
			cacheRoot, cacheDir, cerr = rw.CommandExtensionCache()
			return cerr
		},
	})
	if err != nil {
		t.Fatalf("adding a command extension outside a workspace: %v", err)
	}
	if !res.ConfigIsMachineWide {
		t.Errorf("the addition was recorded in %s, which is not the machine-wide configuration", res.ConfigPath)
	}
	machineRoot, machineDir, merr := rw.MachineArtifactCache()
	if merr != nil {
		t.Fatalf("the machine-wide cache does not resolve: %v", merr)
	}
	if cacheRoot != machineRoot || cacheDir != machineDir {
		t.Errorf("the plugin's bytes are cached under %s/%s; want the machine-wide cache %s/%s, not the folder %s",
			cacheRoot, cacheDir, machineRoot, machineDir, rw.Root())
	}
	if reg.walks != 0 {
		t.Errorf("the addition walked the catalog %d time(s) for a plugin's own name", reg.walks)
	}
}

// TestAPluginPublishingOnlyPreReleasesNamesOneToPin pins the refusal of a plugin
// whose every published version is a pre-release: it names them and the newest as
// a query to type, rather than saying nothing is published.
//
// 🔴 PLANTED DEFECT: return the "no published version tag" refusal from
// noReleasedVersion unconditionally and the row fails on the missing versions.
func TestAPluginPublishingOnlyPreReleasesNamesOneToPin(t *testing.T) {
	reg := &countingRegistry{tags: map[string][]string{"plugin/governance/rego": {"0.9.0-rc.1", "0.9.0-rc.2"}}}
	withCountingRegistry(t, reg)
	rw := resolveScratch(t, scratchConfig)

	_, err := Add(context.Background(), rw, AddOptions{Socket: "governance", Query: "rego", Role: "render-verdict"})
	want := "plugin/governance/rego publishes only pre-release versions (0.9.0-rc.1, 0.9.0-rc.2), and the latest version " +
		"is never taken from those; name one to attach it, as in rego:0.9.0-rc.2"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Add = %v; want a refusal containing %q", err, want)
	}

	res, err := Add(context.Background(), rw, AddOptions{Socket: "governance", Query: "rego:0.9.0-rc.2", Role: "render-verdict"})
	if err != nil {
		t.Fatalf("adding the pre-release the refusal names: %v", err)
	}
	if res.Identity != "governance/rego:0.9.0-rc.2" {
		t.Errorf("attached %q; want governance/rego:0.9.0-rc.2", res.Identity)
	}
}
