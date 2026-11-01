// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// The scaffold command's example form, driven through the real command tree
// against an in-process registry serving a signed documentation bundle: an
// example written out and the examples listed, and every refusal — an unsigned
// bundle, one signed by a key the workspace does not trust, a member leaving the
// directory, a directory already holding something, and an offline workspace with
// nothing cached — leaving nothing written.
//
// The bundle is reached through the production acquisition: the registry is
// substituted at the narrowest seam, the target, so the cache probe, the pull, the
// signature verification against the workspace's own trust policy, the cache
// write and the admission of every read are the ones every surface runs. Each
// case states a cache root of its own, so the developer's machine-wide cache is
// neither read nor written.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle"
	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle/docsbundletest"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// exampleRegistryHost is the default registry the example cases' workspaces
// name, so the bundle's reference is one only these cases use.
const exampleRegistryHost = "examples.example.invalid"

// exampleDemoReadme is the page of the example the cases write out: a header, a
// shell block before the Build section that is not the next commands, and the
// Build section's own block.
const exampleDemoReadme = "---\nexample:\n  level: start\n---\n\n# Demo\n\nA demo example.\n\n" +
	"```sh\nnot a next command\n```\n\n## Build\n\n```sh\n# Compose\naggrega build --release release-compose.yaml --auto-pull\n```\n\n" +
	"## Notes\n"

// exampleMembers is a documentation bundle carrying the list of examples and two
// examples, with extra appended after them.
func exampleMembers(extra ...docsbundletest.Member) []docsbundletest.Member {
	return append([]docsbundletest.Member{
		{Name: "language-reference/examples/by-product-and-capability.md", Body: "# Examples by product and capability\n\n" +
			"## By product\n\n### `core/x`\n\n" +
			"- [demo](demo/README.md) (`start`) — A demo example.\n" +
			"- [other](other/README.md) (`intermediate`) — Another example.\n\n" +
			"## By capability\n\n### `credentials`\n\n" +
			"- [demo](demo/README.md) (`start`) — A demo example.\n"},
		{Name: "language-reference/examples/demo/README.md", Body: exampleDemoReadme},
		{Name: "language-reference/examples/demo/aggregate.yaml", Body: "kind: Aggregate\n"},
		{Name: "language-reference/examples/demo/release-compose.yaml", Body: "kind: Release\n"},
		{Name: "language-reference/examples/other/README.md", Body: "# Other\n\nAnother example.\n"},
	}, extra...)
}

// exampleFixture is one case's registry, workspace and cache.
type exampleFixture struct {
	ws, cacheRoot string
	reaches       int
	reg           *docsbundletest.Registry
}

// newExampleFixture publishes members at this binary's own version, signed by
// signer where it is not nil, and returns a workspace whose default registry is
// the fixture's and whose policy requires a signature verifying under trusted.
func newExampleFixture(t *testing.T, members []docsbundletest.Member, signer, trusted *docsbundletest.Signer) *exampleFixture {
	t.Helper()
	f := &exampleFixture{ws: t.TempDir(), cacheRoot: t.TempDir(), reg: docsbundletest.NewRegistry()}
	f.reg.Publish(t, provenanceCompilerVersion(), docsbundletest.Bundle(t, members), signer)

	var key strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(string(trusted.PublicKeyPEM())), "\n") {
		key.WriteString("      " + line + "\n")
	}
	config := "default_registry: " + exampleRegistryHost + "\nworkspace: {}\nregistries:\n" +
		"  - host: " + exampleRegistryHost + "\n" +
		"    signing:\n      required: true\n" +
		"    public_key: |\n" + key.String()
	if err := os.WriteFile(filepath.Join(f.ws, workspaceconfig.ConfigFilename), []byte(config), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
	return f
}

// online is the context a case drives the command with when the registry is
// reachable: the case's cache root, and the fixture's registry as the target.
func (f *exampleFixture) online() context.Context {
	ctx := docsbundle.WithCacheRoot(context.Background(), f.cacheRoot)
	return docsbundle.WithTarget(ctx, func(cfg workspaceconfig.Config, ref, version string, warn func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
		f.reaches++
		return f.reg, docsbundle.Verifier(cfg, ref, version, f.reg, warn), nil
	})
}

// production is the context with the case's cache root and no target stated, so
// an acquisition reaches the production target, through the workspace's egress.
func (f *exampleFixture) production() context.Context {
	return docsbundle.WithCacheRoot(context.Background(), f.cacheRoot)
}

// run drives the real command tree under ctx against the fixture's workspace.
func (f *exampleFixture) run(t *testing.T, ctx context.Context, args ...string) (out, notes string, err error) {
	t.Helper()
	return runExplainCommandWith(t, ctx, append(args, "--workspace-root", f.ws)...)
}

// cached reports whether the fixture's cache holds any copy of the bundle.
func (f *exampleFixture) cached(t *testing.T) bool {
	t.Helper()
	versions, err := docscache.CachedVersions(f.cacheRoot, docsbundle.Ref(exampleRegistryHost))
	if err != nil {
		t.Fatalf("list the cached versions: %v", err)
	}
	return len(versions) > 0
}

// requireNothingWritten fails when dir holds any entry.
func requireNothingWritten(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("a refused example left %v in %s", names, dir)
	}
}

// TestScaffoldExampleWritesTheExampleAndPrintsItsNextCommands is the primary
// proof: the example's own files in the directory named, the Build section's
// commands printed, and a second example — into the default directory, named
// after it — read from the cache the first one filled, reaching no registry.
func TestScaffoldExampleWritesTheExampleAndPrintsItsNextCommands(t *testing.T) {
	isolateCLIEnvironment(t)
	signer := docsbundletest.NewSigner(t)
	f := newExampleFixture(t, exampleMembers(), signer, signer)
	ctx := f.online()
	dest := filepath.Join(t.TempDir(), "my-demo")

	out, notes, err := f.run(t, ctx, "scaffold", "--example", "demo", dest)
	if err != nil {
		t.Fatalf("aggrega scaffold --example demo: %v\nnotes: %s", err, notes)
	}
	for name, body := range map[string]string{
		"README.md":            exampleDemoReadme,
		"aggregate.yaml":       "kind: Aggregate\n",
		"release-compose.yaml": "kind: Release\n",
	} {
		got, rerr := os.ReadFile(filepath.Join(dest, name))
		if rerr != nil || string(got) != body {
			t.Errorf("%s holds %q (%v), want %q", name, got, rerr, body)
		}
	}
	for _, want := range []string{"Wrote the example demo into " + dest, "cd " + dest, "  # Compose",
		"  aggrega build --release release-compose.yaml --auto-pull"} {
		if !strings.Contains(out, want) {
			t.Errorf("the answer omits %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "not a next command") {
		t.Errorf("a block outside the Build section was printed as a next command:\n%s", out)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if _, notes, err := f.run(t, ctx, "scaffold", "--example", "other"); err != nil {
		t.Fatalf("aggrega scaffold --example other: %v\nnotes: %s", err, notes)
	}
	if got, err := os.ReadFile(filepath.Join(cwd, "other", "README.md")); err != nil || !strings.Contains(string(got), "# Other") {
		t.Errorf("the second example was not written into ./other (%q, %v)", got, err)
	}
	if f.reaches != 1 {
		t.Errorf("the registry was reached %d times for two examples, want once", f.reaches)
	}
}

// TestScaffoldExampleListPrintsEachExampleOnce pins the list: every example the
// bundle's list names, once each with its level and sentence, and nothing written.
func TestScaffoldExampleListPrintsEachExampleOnce(t *testing.T) {
	isolateCLIEnvironment(t)
	signer := docsbundletest.NewSigner(t)
	f := newExampleFixture(t, exampleMembers(), signer, signer)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	out, notes, err := f.run(t, f.online(), "scaffold", "--example", "--list")
	if err != nil {
		t.Fatalf("aggrega scaffold --example --list: %v\nnotes: %s", err, notes)
	}
	var rows []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		rows = append(rows, strings.Join(strings.Fields(line), " "))
	}
	want := []string{
		"EXAMPLE LEVEL DESCRIPTION",
		"demo start A demo example.",
		"other intermediate Another example.",
	}
	if !slices.Equal(rows, want) {
		t.Errorf("the list reads\n%s\nwant rows %q", out, want)
	}
	requireNothingWritten(t, cwd)
}

// TestScaffoldExampleRefusesAnUnsignedBundle pins that a workspace requiring a
// signature is never handed an unsigned bundle: refused at the fetch, nothing
// kept in the cache, nothing written.
func TestScaffoldExampleRefusesAnUnsignedBundle(t *testing.T) {
	isolateCLIEnvironment(t)
	f := newExampleFixture(t, exampleMembers(), nil, docsbundletest.NewSigner(t))
	parent := t.TempDir()

	_, _, err := f.run(t, f.online(), "scaffold", "--example", "demo", filepath.Join(parent, "demo"))
	if !errors.Is(err, signing.ErrNoSignatureFound()) {
		t.Fatalf("an unsigned bundle answered %v, want the missing-signature refusal", err)
	}
	if f.cached(t) {
		t.Error("an unsigned bundle was kept in the cache")
	}
	requireNothingWritten(t, parent)
}

// TestScaffoldExampleRefusesABundleSignedByAKeyTheWorkspaceDoesNotTrust pins the
// other half: a signature the workspace's pinned key does not verify is refused
// as firmly as none.
func TestScaffoldExampleRefusesABundleSignedByAKeyTheWorkspaceDoesNotTrust(t *testing.T) {
	isolateCLIEnvironment(t)
	f := newExampleFixture(t, exampleMembers(), docsbundletest.NewSigner(t), docsbundletest.NewSigner(t))
	parent := t.TempDir()

	_, _, err := f.run(t, f.online(), "scaffold", "--example", "demo", filepath.Join(parent, "demo"))
	if !errors.Is(err, signing.ErrSignatureVerificationFailed()) {
		t.Fatalf("a bundle signed by an untrusted key answered %v, want the verification refusal", err)
	}
	if f.cached(t) {
		t.Error("a bundle the workspace does not trust was kept in the cache")
	}
	requireNothingWritten(t, parent)
}

// TestScaffoldExampleRefusesAMemberLeavingTheDirectory pins that a signed bundle
// carrying a member that climbs out of the example is refused whole, and that the
// place it climbs to is not written either.
func TestScaffoldExampleRefusesAMemberLeavingTheDirectory(t *testing.T) {
	isolateCLIEnvironment(t)
	signer := docsbundletest.NewSigner(t)
	f := newExampleFixture(t, exampleMembers(docsbundletest.Member{
		Name: "language-reference/examples/demo/../../../../planted.txt", Body: "planted\n",
	}), signer, signer)
	parent := t.TempDir()
	dest := filepath.Join(parent, "a", "b", "c", "demo")

	_, _, err := f.run(t, f.online(), "scaffold", "--example", "demo", dest)
	if !errors.Is(err, oci.ErrFetchIntegrity()) {
		t.Fatalf("a member leaving the example answered %v, want the integrity refusal", err)
	}
	requireNothingWritten(t, parent)
}

// TestScaffoldExampleRefusesADirectoryHoldingAnything pins that the directory
// named is left exactly as it was when it already holds something.
func TestScaffoldExampleRefusesADirectoryHoldingAnything(t *testing.T) {
	isolateCLIEnvironment(t)
	signer := docsbundletest.NewSigner(t)
	f := newExampleFixture(t, exampleMembers(), signer, signer)
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "mine.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := f.run(t, f.online(), "scaffold", "--example", "demo", dest)
	if err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("a directory holding a file answered %v, want it refused as not empty", err)
	}
	entries, rerr := os.ReadDir(dest)
	if rerr != nil || len(entries) != 1 || entries[0].Name() != "mine.txt" {
		t.Errorf("the directory changed: %v (%v)", entries, rerr)
	}
	if got, _ := os.ReadFile(filepath.Join(dest, "mine.txt")); string(got) != "mine\n" {
		t.Errorf("the file already there now holds %q", got)
	}
}

// TestScaffoldExampleOfflineReadsOnlyWhatIsCached pins the offline posture: with
// the bundle cached, an example is written from the copy with no registry
// reached; with nothing cached, the offline refusal is what the reader sees,
// unchanged, and nothing is written.
func TestScaffoldExampleOfflineReadsOnlyWhatIsCached(t *testing.T) {
	isolateCLIEnvironment(t)
	signer := docsbundletest.NewSigner(t)
	f := newExampleFixture(t, exampleMembers(), signer, signer)

	if _, notes, err := f.run(t, f.online(), "scaffold", "--example", "--list"); err != nil {
		t.Fatalf("filling the cache: %v\nnotes: %s", err, notes)
	}
	dest := filepath.Join(t.TempDir(), "demo")
	if _, notes, err := f.run(t, f.production(), "scaffold", "--example", "demo", dest, "--offline"); err != nil {
		t.Fatalf("offline with the bundle cached: %v\nnotes: %s", err, notes)
	}
	if _, err := os.Stat(filepath.Join(dest, "aggregate.yaml")); err != nil {
		t.Errorf("offline with the bundle cached wrote no example: %v", err)
	}

	empty := newExampleFixture(t, exampleMembers(), signer, signer)
	parent := t.TempDir()
	_, _, err := empty.run(t, empty.production(), "scaffold", "--example", "demo", filepath.Join(parent, "demo"), "--offline")
	if !errors.Is(err, egress.ErrOfflineEgress()) {
		t.Fatalf("offline with nothing cached answered %v, want the offline refusal", err)
	}
	if !strings.Contains(err.Error(), egress.ErrOfflineEgress().Error()) {
		t.Errorf("the offline refusal's own message did not reach the reader: %v", err)
	}
	requireNothingWritten(t, parent)
}

// TestScaffoldExampleKeepsTheDraftingForm pins that the kind form is untouched by
// the example form beside it, and that --list without --example is refused before
// anything is read.
func TestScaffoldExampleKeepsTheDraftingForm(t *testing.T) {
	isolateCLIEnvironment(t)
	out, _, err := runExplainCommandWith(t, context.Background(), "scaffold", "component", "my-service")
	if err != nil || !strings.Contains(out, "kind: Component") || !strings.Contains(out, "my-service") {
		t.Fatalf("aggrega scaffold component my-service answered (%v):\n%s", err, out)
	}
	if _, _, err := runExplainCommandWith(t, context.Background(), "scaffold", "--list"); err == nil ||
		!strings.Contains(err.Error(), "--example") {
		t.Errorf("--list without --example answered %v, want it refused naming --example", err)
	}
}
