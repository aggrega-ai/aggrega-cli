// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

// The daemon's example leg of aggrega/scaffoldEntity, driven through the
// production dispatch against an in-process registry serving a signed
// documentation bundle: an example written into the workspace the request names
// and the examples listed, and each refusal — a directory holding anything, a
// directory out of the workspace, an unsigned bundle, one signed by a key the
// workspace does not trust, an offline workspace with nothing cached, a request
// naming no workspace, and fields of two forms sent together — leaving nothing
// written.
//
// The registry is substituted at the narrowest seam, the target the launch
// states, so the cache probe, the pull, the signature verification against the
// workspace's own trust policy, the cache write and the admission of every read
// are the ones every surface runs.

import (
	"bytes"
	"context"
	"encoding/json"
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

// daemonExampleHost is the default registry the example cases' workspaces name.
const daemonExampleHost = "examples.example.invalid"

// daemonExampleVersion is the version the daemon reads the documentation at. It
// is deliberately not shaped like a release, so no release can come to match it.
const daemonExampleVersion = "0.0.0-scaffold-example-fixture"

// daemonExampleFixture is one case's registry, workspace and cache.
type daemonExampleFixture struct {
	ws, cacheRoot string
	reaches       int
	reg           *docsbundletest.Registry
}

// newDaemonExampleFixture publishes members at daemonExampleVersion, signed by
// signer where it is not nil, under a workspace whose policy trusts trusted's key.
func newDaemonExampleFixture(t *testing.T, members []docsbundletest.Member, signer, trusted *docsbundletest.Signer) *daemonExampleFixture {
	t.Helper()
	f := &daemonExampleFixture{ws: t.TempDir(), cacheRoot: t.TempDir(), reg: docsbundletest.NewRegistry()}
	f.reg.Publish(t, daemonExampleVersion, docsbundletest.Bundle(t, members), signer)
	docsbundletest.WriteTrustingWorkspace(t, f.ws, daemonExampleHost, trusted)
	return f
}

// session returns a daemon session launched somewhere else entirely, so the
// workspace a request names is resolved from disk as a client's would be. Online,
// the launch states the fixture's registry as the target; offline, only the cache
// root, so an acquisition reaches the production target through the workspace's
// egress.
func (f *daemonExampleFixture) session(t *testing.T, online bool) *session {
	t.Helper()
	ctx := docsbundle.WithCacheRoot(context.Background(), f.cacheRoot)
	if online {
		ctx = docsbundle.WithTarget(ctx, func(cfg workspaceconfig.Config, ref, version string, warn func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
			f.reaches++
			return f.reg, docsbundle.Verifier(cfg, ref, version, f.reg, warn), nil
		})
	}
	sess := newTestSession(t, t.TempDir(), nil, nil)
	sess.docs = docsbundle.DeclarationsOf(ctx)
	sess.compilerVersion = daemonExampleVersion
	return sess
}

// cached reports whether the fixture's cache holds any copy of the bundle.
func (f *daemonExampleFixture) cached(t *testing.T) bool {
	t.Helper()
	versions, err := docscache.CachedVersions(f.cacheRoot, docsbundle.Ref(daemonExampleHost))
	if err != nil {
		t.Fatalf("list the cached versions: %v", err)
	}
	return len(versions) > 0
}

// callScaffold sends one aggrega/scaffoldEntity request exactly as written.
func callScaffold(t *testing.T, sess *session, params map[string]any) jsonRPCResponse {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/scaffoldEntity", params))
	return runOneRequestVerbatim(t, sess, &in, &out)
}

// decodeExampleResult decodes a successful answer.
func decodeExampleResult(t *testing.T, resp jsonRPCResponse) scaffoldEntityResult {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("aggrega/scaffoldEntity refused: %+v", resp.Error)
	}
	var got scaffoldEntityResult
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("decode result: %v (body: %s)", err, resp.Result)
	}
	return got
}

// daemonEntries lists the names under dir.
func daemonEntries(t *testing.T, dir string) []string {
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

// refusalNames reports whether a refusal's message names the condition sentinel
// declares: by its message, or by its short code.
func refusalNames(message string, sentinel error) bool {
	if strings.Contains(message, sentinel.Error()) {
		return true
	}
	short, ok := cerrors.ShortCodeOf(sentinel)
	return ok && strings.Contains(message, short)
}

// TestScaffoldExampleDaemonWritesTheExampleAndListsTheExamples is the primary
// proof on this surface: the list read from the bundle, then the example's own
// files in the directory named relative to the workspace root, the answer naming
// them and the page's commands — and a second example, into the directory named
// after it, read from the cache the first request filled.
func TestScaffoldExampleDaemonWritesTheExampleAndListsTheExamples(t *testing.T) {
	signer := docsbundletest.NewSigner(t)
	f := newDaemonExampleFixture(t, docsbundletest.ExampleMembers(), signer, signer)
	sess := f.session(t, true)

	list := decodeExampleResult(t, callScaffold(t, sess, map[string]any{"workspaceRoot": f.ws, "list": true}))
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

	written := decodeExampleResult(t, callScaffold(t, sess, map[string]any{
		"workspaceRoot": f.ws, "example": "demo", "dir": "apps/my-demo",
	}))
	if written.Example != "demo" || written.Dir != "apps/my-demo" || written.Version != daemonExampleVersion {
		t.Errorf("the answer is %+v, want example demo into apps/my-demo at %s", written, daemonExampleVersion)
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

	second := decodeExampleResult(t, callScaffold(t, sess, map[string]any{"workspaceRoot": f.ws, "example": "other"}))
	if second.Dir != "other" {
		t.Errorf("the second example went into %q, want the directory named after it", second.Dir)
	}
	if got, err := os.ReadFile(filepath.Join(f.ws, "other", "README.md")); err != nil || !strings.Contains(string(got), "# Other") {
		t.Errorf("the second example was not written into ./other (%q, %v)", got, err)
	}
	if f.reaches != 1 {
		t.Errorf("the registry was reached %d times for a list and two examples, want once", f.reaches)
	}
}

// TestScaffoldExampleDaemonRefusesADirectoryHoldingAnything pins that a directory
// already holding something is left exactly as it was.
func TestScaffoldExampleDaemonRefusesADirectoryHoldingAnything(t *testing.T) {
	signer := docsbundletest.NewSigner(t)
	f := newDaemonExampleFixture(t, docsbundletest.ExampleMembers(), signer, signer)
	dest := filepath.Join(f.ws, "taken")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "mine.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := callScaffold(t, f.session(t, true), map[string]any{"workspaceRoot": f.ws, "example": "demo", "dir": "taken"})
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "not empty") {
		t.Fatalf("a directory holding a file answered %+v (%s)", resp.Error, resp.Result)
	}
	if got := daemonEntries(t, dest); !slices.Equal(got, []string{"mine.txt"}) {
		t.Errorf("the directory changed: %v", got)
	}
}

// TestScaffoldExampleDaemonRefusesADirectoryOutOfTheWorkspace pins that a
// directory climbing above the workspace root, an absolute one, and one leading
// out of the workspace through a link are each refused naming dir before anything
// is fetched or written.
func TestScaffoldExampleDaemonRefusesADirectoryOutOfTheWorkspace(t *testing.T) {
	signer := docsbundletest.NewSigner(t)
	f := newDaemonExampleFixture(t, docsbundletest.ExampleMembers(), signer, signer)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(f.ws, "linked")); err != nil {
		t.Skipf("this filesystem does not hold links: %v", err)
	}
	sess := f.session(t, true)

	for _, dir := range []string{"../escaped", filepath.Join(outside, "demo"), "linked/demo"} {
		resp := callScaffold(t, sess, map[string]any{"workspaceRoot": f.ws, "example": "demo", "dir": dir})
		if resp.Error == nil || resp.Error.Code != jsonRPCInvalidParams || !strings.Contains(resp.Error.Message, "dir must name") {
			t.Errorf("%q answered %+v (%s)", dir, resp.Error, resp.Result)
		}
	}
	if f.reaches != 0 || f.cached(t) {
		t.Errorf("a refused directory still reached the registry %d time(s) (cached=%v)", f.reaches, f.cached(t))
	}
	if got := daemonEntries(t, outside); len(got) != 0 {
		t.Errorf("a refused directory left %v outside the workspace", got)
	}
	if got := daemonEntries(t, filepath.Dir(f.ws)); slices.Contains(got, "escaped") {
		t.Error("a directory climbing above the workspace was written")
	}
}

// TestScaffoldExampleDaemonRefusesAnUnverifiedBundle pins that an unsigned bundle,
// and one signed by a key the workspace does not trust, are refused naming the
// condition, with nothing kept in the cache and nothing written.
func TestScaffoldExampleDaemonRefusesAnUnverifiedBundle(t *testing.T) {
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
			f := newDaemonExampleFixture(t, docsbundletest.ExampleMembers(), by, docsbundletest.NewSigner(t))

			resp := callScaffold(t, f.session(t, true), map[string]any{"workspaceRoot": f.ws, "example": "demo"})
			if resp.Error == nil || !refusalNames(resp.Error.Message, tc.want) {
				t.Fatalf("answered %+v (%s), want the refusal naming %v", resp.Error, resp.Result, tc.want)
			}
			if f.cached(t) {
				t.Error("a bundle that did not verify was kept in the cache")
			}
			if got := daemonEntries(t, f.ws); !slices.Equal(got, []string{workspaceconfig.ConfigFilename}) {
				t.Errorf("a refused example wrote %v", got)
			}
		})
	}
}

// TestScaffoldExampleDaemonOfflineReadsOnlyWhatIsCached pins the offline posture:
// with the bundle cached, the example is written from the copy; with nothing
// cached, the offline refusal is the answer and nothing is written.
func TestScaffoldExampleDaemonOfflineReadsOnlyWhatIsCached(t *testing.T) {
	signer := docsbundletest.NewSigner(t)
	f := newDaemonExampleFixture(t, docsbundletest.ExampleMembers(), signer, signer)
	configPath := filepath.Join(f.ws, workspaceconfig.ConfigFilename)
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	decodeExampleResult(t, callScaffold(t, f.session(t, true), map[string]any{"workspaceRoot": f.ws, "list": true}))
	if err := os.WriteFile(configPath, append(config, []byte("offline: true\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	decodeExampleResult(t, callScaffold(t, f.session(t, false), map[string]any{"workspaceRoot": f.ws, "example": "demo"}))
	if _, err := os.Stat(filepath.Join(f.ws, "demo", "aggregate.yaml")); err != nil {
		t.Errorf("offline with the bundle cached wrote no example: %v", err)
	}

	empty := newDaemonExampleFixture(t, docsbundletest.ExampleMembers(), signer, signer)
	if err := os.WriteFile(filepath.Join(empty.ws, workspaceconfig.ConfigFilename), append(config, []byte("offline: true\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	resp := callScaffold(t, empty.session(t, false), map[string]any{"workspaceRoot": empty.ws, "example": "demo"})
	if resp.Error == nil || !refusalNames(resp.Error.Message, egress.ErrOfflineEgress()) {
		t.Fatalf("offline with nothing cached answered %+v (%s)", resp.Error, resp.Result)
	}
	if got := daemonEntries(t, empty.ws); !slices.Equal(got, []string{workspaceconfig.ConfigFilename}) {
		t.Errorf("offline with nothing cached wrote %v", got)
	}
}

// TestScaffoldExampleDaemonRefusesARequestNamingNoWorkspace pins that the example
// form, unlike drafting, is answered only in a workspace the request names.
func TestScaffoldExampleDaemonRefusesARequestNamingNoWorkspace(t *testing.T) {
	signer := docsbundletest.NewSigner(t)
	f := newDaemonExampleFixture(t, docsbundletest.ExampleMembers(), signer, signer)

	for _, params := range []map[string]any{{"example": "demo"}, {"list": true}} {
		resp := callScaffold(t, f.session(t, true), params)
		if resp.Error == nil || !strings.Contains(resp.Error.Message, "`workspaceRoot`") {
			t.Errorf("%v answered %+v (%s)", params, resp.Error, resp.Result)
		}
	}
	if f.reaches != 0 {
		t.Errorf("a request naming no workspace reached the registry %d time(s)", f.reaches)
	}
}

// TestScaffoldExampleDaemonRefusesFieldsOfTwoFormsInTheSharedWords pins that a
// request mixing the forms is refused before anything is read, in the shared
// layer's own words — the words the assistant tool refuses the same call in.
func TestScaffoldExampleDaemonRefusesFieldsOfTwoFormsInTheSharedWords(t *testing.T) {
	signer := docsbundletest.NewSigner(t)
	f := newDaemonExampleFixture(t, docsbundletest.ExampleMembers(), signer, signer)
	sess := f.session(t, true)

	for _, tc := range []struct {
		kind, name, example, dir string
		list                     bool
	}{
		{kind: "component", example: "demo"},
		{list: true, example: "demo"},
		{list: true, kind: "component"},
		{name: "api", example: "demo"},
		{kind: "component", dir: "here"},
	} {
		_, want := exampleops.RequestForm(tc.kind, tc.name, tc.example, tc.dir, tc.list)
		if want == nil {
			t.Fatalf("%+v is not a mixed request, so the row asserts nothing", tc)
		}
		params := map[string]any{"workspaceRoot": f.ws, "kind": tc.kind, "name": tc.name, "example": tc.example,
			"dir": tc.dir, "list": tc.list}
		resp := callScaffold(t, sess, params)
		if resp.Error == nil || resp.Error.Code != jsonRPCInvalidParams || resp.Error.Message != want.Error() {
			t.Errorf("%+v answered %+v, want %q", tc, resp.Error, want.Error())
		}
	}
	if f.reaches != 0 {
		t.Errorf("a refused request reached the registry %d time(s)", f.reaches)
	}
}
