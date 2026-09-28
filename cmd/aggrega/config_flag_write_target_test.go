// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/hometest"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The named configuration file governs writes, not only reads.
//
// Every case here asserts the same pair on disk, and the pair is the point: the
// named file CHANGED, and the machine-wide file did NOT. Asserting only the first
// half passes while a write lands in both places, or in neither and the named
// file merely happened to be written by the fixture — and the half that matters
// is the second, because a write that reaches a machine-wide file reaches every
// other project on the machine.
//
// Each case redirects the per-user homes for its own duration before resolving
// anything, so the machine-wide path these assertions name is inside a throwaway
// directory and the real one is unreachable. The redirect is asserted rather than
// trusted: a case that could not name a machine-wide path under its own sandbox
// fails before it writes anything.

// namedConfigCase is the fixture every case runs against: a workspace root, the
// file --config names, and the machine-wide file the write must not touch.
type namedConfigCase struct {
	root        string
	named       string
	machineWide string
}

// newNamedConfigCase redirects the per-user homes for this test, seeds a
// workspace root and a named global config file, and returns the three paths.
//
// The machine-wide path is read from the production resolver under the redirect
// rather than assembled here, so the file this fixture guards is the one the code
// under test would actually reach for.
//
// Three preconditions are checked before any case writes, and each is what makes
// a later assertion mean something. The path must sit under the redirected
// configuration home — otherwise the case is pointed at a real file. It must not
// sit under the developer's actual home, read BEFORE the redirect moved it,
// because after the redirect the process cannot tell one from the other. And it
// must be absent, because "this file did not appear" is vacuous if the file could
// never have appeared there at all.
func newNamedConfigCase(t *testing.T, namedBody string) namedConfigCase {
	t.Helper()
	realHome, realHomeErr := os.UserHomeDir()
	hometest.Isolate(t)
	// Isolate covers the configuration and credential homes; the cache home is
	// redirected here so nothing this case runs writes into a shared cache either.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	machineWide, ok := workspaceconfig.GlobalConfigPath(nil)
	if !ok {
		t.Fatal("no machine-wide config path resolves under the redirected home; the sandbox cannot be checked")
	}
	configHome := hometest.ConfigHome()
	if configHome == "" || !strings.HasPrefix(machineWide, configHome+string(os.PathSeparator)) {
		t.Fatalf("machine-wide path %q is not inside the redirected configuration home %q; refusing to run", machineWide, configHome)
	}
	if realHomeErr == nil && realHome != "" && strings.HasPrefix(machineWide, realHome+string(os.PathSeparator)) {
		t.Fatalf("machine-wide path %q is inside the developer's own home %q; refusing to run", machineWide, realHome)
	}
	if _, err := os.Stat(machineWide); !os.IsNotExist(err) {
		t.Fatalf("machine-wide config %q already exists (stat err %v); the case could not tell a leak from the fixture", machineWide, err)
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename),
		[]byte("workspace: {}\ndefault_registry: hub.aggrega.ai\noffline: false\n"), 0o644); err != nil {
		t.Fatalf("seed workspace config: %v", err)
	}

	named := filepath.Join(t.TempDir(), "named", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(named), 0o755); err != nil {
		t.Fatalf("create the named config's directory: %v", err)
	}
	if err := os.WriteFile(named, []byte(namedBody), 0o644); err != nil {
		t.Fatalf("seed named config: %v", err)
	}
	return namedConfigCase{root: root, named: named, machineWide: machineWide}
}

// assertNamedChanged reads the named file and the machine-wide file and asserts
// the pair: the named file now carries want, and the machine-wide file is still
// absent. The two are checked together because either alone is satisfiable by a
// write that went to both places.
func (c namedConfigCase) assertNamedChanged(t *testing.T, want string) {
	t.Helper()
	body, err := os.ReadFile(c.named)
	if err != nil {
		t.Fatalf("read the named config %q: %v", c.named, err)
	}
	if !strings.Contains(string(body), want) {
		t.Errorf("the named config %q does not carry %q; the write did not land there:\n%s", c.named, want, body)
	}
	c.assertMachineWideUntouched(t)
}

// assertMachineWideUntouched asserts the machine-wide file was never created.
// This is the half an obvious test omits, and it is the half that describes the
// damage: a write that reaches this file reaches every project on the machine.
func (c namedConfigCase) assertMachineWideUntouched(t *testing.T) {
	t.Helper()
	body, err := os.ReadFile(c.machineWide)
	if err == nil {
		t.Errorf("the machine-wide config %q was written; the named file was ignored:\n%s", c.machineWide, body)
		return
	}
	if !os.IsNotExist(err) {
		t.Errorf("machine-wide config %q: unexpected stat error %v", c.machineWide, err)
	}
}

// runNamed executes the real command tree with --config naming the case's file,
// which is the whole path under test: the flag is folded into the resolution's
// environment reader, and the write destination comes off that resolution.
func (c namedConfigCase) runNamed(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(append([]string{"--config", c.named, "--workspace-root", c.root}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("aggrega --config %s %s: %v\n%s", c.named, strings.Join(args, " "), err, out.String())
	}
	return out.String()
}

// TestNamedConfigGovernsRegistryAdd proves the registry path, which is the
// control: it honoured the named file before this behaviour was made uniform, so
// a case that fails here is reporting a regression rather than the defect.
func TestNamedConfigGovernsRegistryAdd(t *testing.T) {
	c := newNamedConfigCase(t, "workspace: {}\n")
	c.runNamed(t, "registry", "add", "named.example.com", "--fetch-trust=false")
	c.assertNamedChanged(t, "named.example.com")
}

// TestNamedConfigGovernsRegistryRemove proves the removal path resolves the same
// file the addition did.
func TestNamedConfigGovernsRegistryRemove(t *testing.T) {
	c := newNamedConfigCase(t, "workspace: {}\nregistries:\n  - host: drop.example.com\n")
	c.runNamed(t, "registry", "remove", "drop.example.com")

	body, err := os.ReadFile(c.named)
	if err != nil {
		t.Fatalf("read the named config: %v", err)
	}
	if strings.Contains(string(body), "drop.example.com") {
		t.Errorf("the entry was not removed from the named config %q:\n%s", c.named, body)
	}
	c.assertMachineWideUntouched(t)
}

// TestNamedConfigGovernsPluginAddCLI proves installing a command extension writes
// the pin into the named file. A cli plugin is machine-wide tooling, so its write
// defaults to the global file — which is exactly why an ignored --config sent it
// to the machine-wide one.
func TestNamedConfigGovernsPluginAddCLI(t *testing.T) {
	c := newNamedConfigCase(t, "workspace: {}\n")
	withCLIRegistrations(t, nil)
	withSearchTarget(t, fakeRepoLister{repos: []string{"plugin/cli/moo"}}, nil)
	withTagLister(t, []string{"1.0.0"})
	withInstallSeams(t,
		fixedFetch("oci://hub.aggrega.ai/plugin/cli/moo:1.0.0", []byte("guest-bytes")),
		fixedDescribe(cliDesc("moo", "Print a greeting")))

	c.runNamed(t, "plugin", "add", "cli", "moo", "--yes")
	c.assertNamedChanged(t, "plugin/cli/moo")
}

// TestNamedConfigGovernsPluginRemoveCLI proves detaching a command extension
// removes it from the named file. Symmetric with the install above, and global by
// the same nature.
func TestNamedConfigGovernsPluginRemoveCLI(t *testing.T) {
	c := newNamedConfigCase(t, "workspace: {}\nplugins:\n  cli:\n"+
		"    - uri: oci://hub.aggrega.ai/plugin/cli/moo:1.0.0\n      digest: "+placeholderPin('c')+"\n")
	withCLIRegistrations(t, nil)

	c.runNamed(t, "plugin", "remove", "cli", "moo")

	body, err := os.ReadFile(c.named)
	if err != nil {
		t.Fatalf("read the named config: %v", err)
	}
	if strings.Contains(string(body), "plugin/cli/moo") {
		t.Errorf("the command extension was not detached from the named config %q:\n%s", c.named, body)
	}
	c.assertMachineWideUntouched(t)
}

// TestNamedConfigGovernsPluginAddGlobal proves a socket attachment forced global
// writes the named file. A socket attachment defaults to the project's own config,
// so this case is what makes the flag load-bearing for the attach path at all.
func TestNamedConfigGovernsPluginAddGlobal(t *testing.T) {
	c := newNamedConfigCase(t, "workspace: {}\n")
	withCLIRegistrations(t, nil)
	withSearchTarget(t, fakeRepoLister{repos: []string{"plugin/governance/tag-injector"}}, nil)
	withTagLister(t, []string{"1.0.0"})

	c.runNamed(t, "plugin", "add", "governance", "tag-injector",
		"--role", "label-transform", "--global")
	c.assertNamedChanged(t, "tag-injector")
}

// TestNamedConfigGovernsPluginRemoveGlobal proves the same for the inverse.
func TestNamedConfigGovernsPluginRemoveGlobal(t *testing.T) {
	c := newNamedConfigCase(t, "workspace: {}\nplugins:\n  governance:\n"+
		"    - uri: governance/tag-injector:1.0.0\n      role: label-transform\n")
	withCLIRegistrations(t, nil)

	c.runNamed(t, "plugin", "remove", "governance", "tag-injector", "--global")

	body, err := os.ReadFile(c.named)
	if err != nil {
		t.Fatalf("read the named config: %v", err)
	}
	if strings.Contains(string(body), "tag-injector") {
		t.Errorf("the attachment was not detached from the named config %q:\n%s", c.named, body)
	}
	c.assertMachineWideUntouched(t)
}

// TestNamedConfigGovernsConfigInit proves seeding a configuration writes the
// named file.
//
// It drives the command body rather than the command tree, and the reason is the
// trust fetch: init has no flag to skip it, so a tree-driven case would reach for
// a registry's own origin over the network. The workspace is resolved exactly as
// the command's own body resolves it — the flag folded into an environment
// reader, then resolved without the global layer because the file is about to be
// created — so the seam under test is the production one and only the transport
// is substituted.
func TestNamedConfigGovernsConfigInit(t *testing.T) {
	c := newNamedConfigCase(t, "workspace: {}\n")
	// init refuses an existing file without --force, and the fixture seeded one.
	if err := os.Remove(c.named); err != nil {
		t.Fatalf("clear the named config so init may create it: %v", err)
	}

	ctx := withWorkspaceResolver(context.Background(), c.root, globalConfigLookup(c.named, os.Getenv))
	rw, err := workspaceconfig.ResolveWithoutGlobal("", workspaceRootFromContext(ctx), configLookupFromContext(ctx))
	if err != nil {
		t.Fatalf("resolve as the command body does: %v", err)
	}
	if err := configInitRun(t.Context(), configInitConfig{
		Registry:    workspaceconfig.HubBaselineHost,
		ws:          rw,
		Out:         &bytes.Buffer{},
		trustClient: offlineTrustClient(),
	}); err != nil {
		t.Fatalf("configInitRun: %v", err)
	}
	c.assertNamedChanged(t, workspaceconfig.HubBaselineHost)
}

// TestNamedConfigLeavesProviderBindingInTheProject proves the interactive
// provider-binding answer lands in the project's own config file and nowhere
// else.
//
// A provider binding is scoped to how one project builds, so the named global
// file is NOT its destination — and that is the assertion, not an omission. What
// this case proves is the other half of the same property: the site resolves its
// destination off the resolution rather than assembling one, so it cannot reach
// the machine-wide file whatever the environment says. It drives the helper
// directly because reaching it from a command line needs an ambiguously-served
// capability and a person at a terminal.
func TestNamedConfigLeavesProviderBindingInTheProject(t *testing.T) {
	c := newNamedConfigCase(t, "workspace: {}\n")

	ctx := withWorkspaceResolver(context.Background(), c.root, globalConfigLookup(c.named, os.Getenv))
	rw, err := workspaceconfig.Resolve("", workspaceRootFromContext(ctx), configLookupFromContext(ctx))
	if err != nil {
		t.Fatalf("resolve as the pull path does: %v", err)
	}

	stdin, restore := replaceStdin(t, "1\n")
	defer restore()
	_ = stdin

	wsCfg := rw.Config()
	candidates := []types.Plugin{
		{Metadata: types.PluginMetadata{Name: "provider/helm", Version: "1.0.0", PluginType: "provider"}},
		{Metadata: types.PluginMetadata{Name: "provider/helm-alt", Version: "1.0.0", PluginType: "provider"}},
	}
	if err := runProviderBindingHelper("helm", candidates, rw, &wsCfg, &bytes.Buffer{}); err != nil {
		t.Fatalf("runProviderBindingHelper: %v", err)
	}

	projectFile := filepath.Join(c.root, workspaceconfig.ConfigFilename)
	body, err := os.ReadFile(projectFile)
	if err != nil {
		t.Fatalf("read the project config %q: %v", projectFile, err)
	}
	if !strings.Contains(string(body), "provider/helm") {
		t.Errorf("the binding did not land in the project config %q:\n%s", projectFile, body)
	}
	named, err := os.ReadFile(c.named)
	if err != nil {
		t.Fatalf("read the named config: %v", err)
	}
	if strings.Contains(string(named), "provider/helm") {
		t.Errorf("the project-scoped binding leaked into the named global config %q:\n%s", c.named, named)
	}
	c.assertMachineWideUntouched(t)
}

// replaceStdin points os.Stdin at a pipe carrying answer for the duration of the
// caller's use, and returns the restore. The provider-binding helper reads the
// operator's choice from os.Stdin directly, so a case that exercises it has to
// supply one.
func replaceStdin(t *testing.T, answer string) (*os.File, func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create a stdin pipe: %v", err)
	}
	if _, err := w.WriteString(answer); err != nil {
		t.Fatalf("write the answer: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close the answer writer: %v", err)
	}
	prev := os.Stdin
	os.Stdin = r
	return r, func() {
		os.Stdin = prev
		_ = r.Close()
	}
}
