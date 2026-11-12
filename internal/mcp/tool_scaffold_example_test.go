// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

// The scaffold tool's example form, driven through tools/call against an
// in-process registry serving a signed documentation bundle: an example written
// and the examples listed, and each refusal — a directory holding anything, a
// directory a listener caller may not name, an unsigned bundle, one signed by a
// key the workspace does not trust, an offline workspace with nothing cached, and
// fields of two forms sent together — leaving nothing written.
//
// The registry is substituted at the narrowest seam, the target the run states, so
// the cache probe, the pull, the signature verification against the workspace's
// own trust policy, the cache write and the admission of every read are the ones
// every surface runs. Each case keeps the bundle under a cache root of its own.

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle"
	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle/docsbundletest"
	"github.com/aggrega-ai/aggrega-cli/internal/exampleops"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// toolExampleHost is the default registry the example cases' workspaces name.
const toolExampleHost = "examples.example.invalid"

// toolExampleVersion is the version the server reads the documentation at. It is
// deliberately not shaped like a release, so no release can come to match it.
const toolExampleVersion = "0.0.0-scaffold-example-fixture"

// toolExampleFixture is one case's registry, workspace and cache.
type toolExampleFixture struct {
	ws, cacheRoot string
	reaches       int
	reg           *docsbundletest.Registry
}

// newToolExampleFixture publishes members at toolExampleVersion, signed by signer
// where it is not nil, under a workspace whose policy trusts trusted's key.
func newToolExampleFixture(t *testing.T, members []docsbundletest.Member, signer, trusted *docsbundletest.Signer) *toolExampleFixture {
	t.Helper()
	f := &toolExampleFixture{ws: t.TempDir(), cacheRoot: t.TempDir(), reg: docsbundletest.NewRegistry()}
	f.reg.Publish(t, toolExampleVersion, docsbundletest.Bundle(t, members), signer)
	docsbundletest.WriteTrustingWorkspace(t, f.ws, toolExampleHost, trusted)
	return f
}

// server returns a server over the fixture's workspace on transport. Online, the
// run states the fixture's registry as the target; offline, it states none, so an
// acquisition reaches the production target through the workspace's egress.
func (f *toolExampleFixture) server(transport transportMode, online bool) *server {
	ctx := context.Background()
	if online {
		ctx = docsbundle.WithTarget(ctx, func(cfg workspaceconfig.Config, ref, version string, warn func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
			f.reaches++
			return f.reg, docsbundle.Verifier(cfg, ref, version, f.reg, warn), nil
		})
	}
	return &server{
		out: io.Discard, logw: io.Discard, serverVersion: toolExampleVersion, launchRoot: f.ws,
		transport: transport, docsRoot: f.cacheRoot, docsDecl: docsbundle.DeclarationsOf(ctx),
	}
}

// cached reports whether the fixture's cache holds any copy of the bundle.
func (f *toolExampleFixture) cached(t *testing.T) bool {
	t.Helper()
	versions, err := docscache.CachedVersions(f.cacheRoot, docsbundle.Ref(toolExampleHost))
	if err != nil {
		t.Fatalf("list the cached versions: %v", err)
	}
	return len(versions) > 0
}

// invokeScaffold runs the scaffold tool's handler on s with args, as tools/call
// hands them on, without the admission a listener's credential decides.
func invokeScaffold(t *testing.T, s *server, args map[string]any) *callToolResult {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal arguments: %v", err)
	}
	res, rpcErr := scaffoldToolSpec.invoke(s, context.Background(), raw)
	if rpcErr != nil {
		t.Fatalf("the scaffold tool returned a protocol error envelope: %+v", rpcErr)
	}
	if res == nil || len(res.Content) == 0 {
		t.Fatal("the scaffold tool returned no content")
	}
	return res
}

// toolEntries lists the names under dir, failing where dir cannot be read.
func toolEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// namesCondition reports whether text names the condition sentinel declares: by
// its message, or by its short code.
func namesCondition(text string, sentinel error) bool {
	if strings.Contains(text, sentinel.Error()) {
		return true
	}
	short, ok := cerrors.ShortCodeOf(sentinel)
	return ok && strings.Contains(text, short)
}

// TestScaffoldExampleToolWritesTheExampleAndListsTheExamples is the primary proof
// on this surface: the list read from the bundle, then the example's own files in
// the directory named relative to the workspace root, the answer naming them and
// the page's commands — and a second example, into the directory named after it,
// read from the cache the first call filled.
func TestScaffoldExampleToolWritesTheExampleAndListsTheExamples(t *testing.T) {
	signer := docsbundletest.NewSigner(t)
	f := newToolExampleFixture(t, docsbundletest.ExampleMembers(), signer, signer)
	s := f.server(transportStdio, true)

	listed := callTool(t, s, "scaffold", map[string]any{"list": true})
	if listed.IsError {
		t.Fatalf("the list was refused: %s", listed.Content[0].Text)
	}
	var list scaffoldResult
	if err := json.Unmarshal(listed.StructuredContent, &list); err != nil {
		t.Fatalf("decode the list: %v (%s)", err, listed.StructuredContent)
	}
	want := []scaffoldExample{
		{Name: "demo", Level: "start", Summary: "A demo example."},
		{Name: "other", Level: "intermediate", Summary: "Another example."},
	}
	if !slices.Equal(list.Examples, want) {
		t.Errorf("the list is %+v, want %+v", list.Examples, want)
	}
	if list.Kind != "" || list.Skeleton != "" || list.Dir != "" || len(list.Files) != 0 {
		t.Errorf("the list carries another form's fields: %+v", list)
	}
	if got := toolEntries(t, f.ws); !slices.Equal(got, []string{workspaceconfig.ConfigFilename}) {
		t.Errorf("listing wrote into the workspace: %v", got)
	}

	res := callTool(t, s, "scaffold", map[string]any{"example": "demo", "dir": "apps/my-demo"})
	if res.IsError {
		t.Fatalf("the example was refused: %s", res.Content[0].Text)
	}
	var written scaffoldResult
	if err := json.Unmarshal(res.StructuredContent, &written); err != nil {
		t.Fatalf("decode the answer: %v (%s)", err, res.StructuredContent)
	}
	if written.Example != "demo" || written.Dir != "apps/my-demo" || written.Version != toolExampleVersion {
		t.Errorf("the answer is %+v, want example demo into apps/my-demo at %s", written, toolExampleVersion)
	}
	if !slices.Equal(written.Next, docsbundletest.ExampleNext) {
		t.Errorf("the next commands are %q, want %q", written.Next, docsbundletest.ExampleNext)
	}
	files := slices.Clone(written.Files)
	slices.Sort(files)
	if !slices.Equal(files, []string{"README.md", "aggregate.yaml", "release-compose.yaml"}) {
		t.Errorf("the files reported are %v", written.Files)
	}
	for name, body := range docsbundletest.ExampleFiles {
		got, err := os.ReadFile(filepath.Join(f.ws, "apps", "my-demo", name))
		if err != nil || string(got) != body {
			t.Errorf("%s holds %q (%v), want %q", name, got, err, body)
		}
	}
	if !strings.Contains(res.Content[0].Text, "cd apps/my-demo") ||
		!strings.Contains(res.Content[0].Text, "aggrega build --release release-compose.yaml --auto-pull") {
		t.Errorf("the text does not give the next commands:\n%s", res.Content[0].Text)
	}

	second := callTool(t, s, "scaffold", map[string]any{"example": "other"})
	if second.IsError {
		t.Fatalf("the second example was refused: %s", second.Content[0].Text)
	}
	if got, err := os.ReadFile(filepath.Join(f.ws, "other", "README.md")); err != nil || !strings.Contains(string(got), "# Other") {
		t.Errorf("the second example was not written into the directory named after it (%q, %v)", got, err)
	}
	if f.reaches != 1 {
		t.Errorf("the registry was reached %d times for a list and two examples, want once", f.reaches)
	}
}

// TestScaffoldExampleToolRefusesADirectoryHoldingAnything pins that a directory
// already holding something is left exactly as it was.
func TestScaffoldExampleToolRefusesADirectoryHoldingAnything(t *testing.T) {
	signer := docsbundletest.NewSigner(t)
	f := newToolExampleFixture(t, docsbundletest.ExampleMembers(), signer, signer)
	dest := filepath.Join(f.ws, "taken")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "mine.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := callTool(t, f.server(transportStdio, true), "scaffold", map[string]any{"example": "demo", "dir": "taken"})
	if !res.IsError || !strings.Contains(res.Content[0].Text, "not empty") {
		t.Fatalf("a directory holding a file answered (isError=%v):\n%s", res.IsError, res.Content[0].Text)
	}
	if got := toolEntries(t, dest); !slices.Equal(got, []string{"mine.txt"}) {
		t.Errorf("the directory changed: %v", got)
	}
}

// TestScaffoldExampleToolRefusesADirectoryOutOfTheServedWorkspace pins the
// listener half of the path rule: a directory climbing above the workspace, and
// an absolute one, are refused naming `dir` before anything is fetched or written.
func TestScaffoldExampleToolRefusesADirectoryOutOfTheServedWorkspace(t *testing.T) {
	signer := docsbundletest.NewSigner(t)
	f := newToolExampleFixture(t, docsbundletest.ExampleMembers(), signer, signer)
	s := f.server(transportHTTP, true)
	outside := t.TempDir()

	for _, dir := range []string{"../escaped", filepath.Join(outside, "demo")} {
		res := invokeScaffold(t, s, map[string]any{"example": "demo", "dir": dir})
		if !res.IsError || !strings.Contains(res.Content[0].Text, "'dir'") {
			t.Errorf("%q answered (isError=%v):\n%s", dir, res.IsError, res.Content[0].Text)
		}
	}
	if f.reaches != 0 || f.cached(t) {
		t.Errorf("a refused directory still reached the registry %d time(s) (cached=%v)", f.reaches, f.cached(t))
	}
	if got := toolEntries(t, outside); len(got) != 0 {
		t.Errorf("a refused directory left %v outside the workspace", got)
	}
	if got := toolEntries(t, filepath.Dir(f.ws)); slices.Contains(got, "escaped") {
		t.Error("a directory climbing above the workspace was written")
	}
}

// TestScaffoldExampleToolTakesADirectoryAsNamedOverItsOwnStreams pins the other
// half: a caller that launched this server names a directory on the machine it is
// at, as it names every path, so an absolute directory outside the workspace is
// written — new or empty, as everywhere.
func TestScaffoldExampleToolTakesADirectoryAsNamedOverItsOwnStreams(t *testing.T) {
	signer := docsbundletest.NewSigner(t)
	f := newToolExampleFixture(t, docsbundletest.ExampleMembers(), signer, signer)
	dest := filepath.Join(t.TempDir(), "demo")

	res := callTool(t, f.server(transportStdio, true), "scaffold", map[string]any{"example": "demo", "dir": dest})
	if res.IsError {
		t.Fatalf("an absolute directory was refused over the process's own streams:\n%s", res.Content[0].Text)
	}
	if _, err := os.Stat(filepath.Join(dest, "aggregate.yaml")); err != nil {
		t.Errorf("the example was not written where it was named: %v", err)
	}
}

// TestScaffoldExampleToolRefusesAnUnverifiedBundle pins that an unsigned bundle,
// and one signed by a key the workspace does not trust, are refused naming the
// condition, with nothing kept in the cache and nothing written.
func TestScaffoldExampleToolRefusesAnUnverifiedBundle(t *testing.T) {
	for _, tc := range []struct {
		scenario string
		signed   bool
		want     error
	}{
		{scenario: "unsigned", want: signing.ErrNoSignatureFound()},
		{scenario: "signed by a key the workspace does not trust", signed: true, want: signing.ErrSignatureVerificationFailed()},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			var by *docsbundletest.Signer
			if tc.signed {
				by = docsbundletest.NewSigner(t)
			}
			f := newToolExampleFixture(t, docsbundletest.ExampleMembers(), by, docsbundletest.NewSigner(t))

			res := callTool(t, f.server(transportStdio, true), "scaffold", map[string]any{"example": "demo"})
			if !res.IsError || !namesCondition(res.Content[0].Text, tc.want) {
				t.Fatalf("answered (isError=%v), want the refusal naming %v:\n%s", res.IsError, tc.want, res.Content[0].Text)
			}
			if f.cached(t) {
				t.Error("a bundle that did not verify was kept in the cache")
			}
			if got := toolEntries(t, f.ws); !slices.Equal(got, []string{workspaceconfig.ConfigFilename}) {
				t.Errorf("a refused example wrote %v", got)
			}
		})
	}
}

// TestScaffoldExampleToolOfflineReadsOnlyWhatIsCached pins the offline posture:
// with nothing cached the offline refusal is the answer and nothing is written;
// with the bundle cached, the example is written from the copy.
func TestScaffoldExampleToolOfflineReadsOnlyWhatIsCached(t *testing.T) {
	signer := docsbundletest.NewSigner(t)
	f := newToolExampleFixture(t, docsbundletest.ExampleMembers(), signer, signer)
	config, err := os.ReadFile(filepath.Join(f.ws, workspaceconfig.ConfigFilename))
	if err != nil {
		t.Fatal(err)
	}

	if res := callTool(t, f.server(transportStdio, true), "scaffold", map[string]any{"list": true}); res.IsError {
		t.Fatalf("filling the cache: %s", res.Content[0].Text)
	}
	if err := os.WriteFile(filepath.Join(f.ws, workspaceconfig.ConfigFilename), append(config, []byte("offline: true\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := callTool(t, f.server(transportStdio, false), "scaffold", map[string]any{"example": "demo"}); res.IsError {
		t.Fatalf("offline with the bundle cached: %s", res.Content[0].Text)
	}
	if _, err := os.Stat(filepath.Join(f.ws, "demo", "aggregate.yaml")); err != nil {
		t.Errorf("offline with the bundle cached wrote no example: %v", err)
	}

	empty := newToolExampleFixture(t, docsbundletest.ExampleMembers(), signer, signer)
	if err := os.WriteFile(filepath.Join(empty.ws, workspaceconfig.ConfigFilename), append(config, []byte("offline: true\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	res := callTool(t, empty.server(transportStdio, false), "scaffold", map[string]any{"example": "demo"})
	if !res.IsError || !namesCondition(res.Content[0].Text, egress.ErrOfflineEgress()) {
		t.Fatalf("offline with nothing cached answered (isError=%v):\n%s", res.IsError, res.Content[0].Text)
	}
	if got := toolEntries(t, empty.ws); !slices.Equal(got, []string{workspaceconfig.ConfigFilename}) {
		t.Errorf("offline with nothing cached wrote %v", got)
	}
}

// TestScaffoldExampleToolRefusesFieldsOfTwoFormsInTheSharedWords pins that a call
// mixing the forms is refused before anything is read, in the shared layer's own
// words — the words the editor daemon refuses the same request in.
func TestScaffoldExampleToolRefusesFieldsOfTwoFormsInTheSharedWords(t *testing.T) {
	signer := docsbundletest.NewSigner(t)
	f := newToolExampleFixture(t, docsbundletest.ExampleMembers(), signer, signer)
	s := f.server(transportStdio, true)

	for _, args := range []map[string]any{
		{"kind": "component", "example": "demo"},
		{"list": true, "example": "demo"},
		{"list": true, "kind": "component"},
		{"name": "api", "example": "demo"},
		{"dir": "here", "kind": "component"},
	} {
		_, want := exampleops.RequestForm(scaffoldTestArg(args["kind"]), scaffoldTestArg(args["name"]),
			scaffoldTestArg(args["example"]), scaffoldTestArg(args["dir"]), args["list"] == true)
		if want == nil {
			t.Fatalf("%v is not a mixed request, so the row asserts nothing", args)
		}
		res := callTool(t, s, "scaffold", args)
		if !res.IsError || res.Content[0].Text != want.Error() {
			t.Errorf("%v answered (isError=%v) %q, want %q", args, res.IsError, res.Content[0].Text, want.Error())
		}
	}
	if f.reaches != 0 {
		t.Errorf("a refused call reached the registry %d time(s)", f.reaches)
	}
}

// TestScaffoldExampleToolWritingReachesTheWorkspace pins the class a call is
// admitted against: a write reaches the workspace, a list and a draft reach
// nothing.
func TestScaffoldExampleToolWritingReachesTheWorkspace(t *testing.T) {
	for _, tc := range []struct {
		args string
		want string
	}{
		{`{"example":"demo"}`, "workspace"},
		{`{"example":"demo","dir":"d"}`, "workspace"},
		{`{"list":true}`, "none"},
		{`{"kind":"component"}`, "none"},
		{`{"kind":"component","example":"demo"}`, "none"},
	} {
		if got := string(scaffoldReach(nil, json.RawMessage(tc.args))); got != tc.want {
			t.Errorf("%s reaches %q, want %q", tc.args, got, tc.want)
		}
	}
}

// scaffoldTestArg is a test argument as a string, empty where it is absent.
func scaffoldTestArg(v any) string {
	s, _ := v.(string)
	return s
}
