// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// The documentation bundle is kept once per machine and read by every session on
// it under that session's own signature check.
//
// Three properties, each driven through the session's own start-of-run
// acquisition and then its own resource read and explain-error tool:
//
//   - a session started over a folder where no workspace cache can be created —
//     the folder a desktop client starts an assistant server in — still acquires
//     the bundle, into the machine-wide cache, and serves it;
//   - a second session over a different folder reads the copy the first acquired
//     and downloads nothing;
//   - a session whose own check refuses the copy another session's looser check
//     let in is served no page and says why on its log, while one whose check
//     admits it is served that copy without a download.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// docsTestHost is the registry host the fixture bundle's reference names.
const docsTestHost = "reg.example.com"

// machineCacheFixtureStore publishes a bundle carrying the documentation pages and
// the halting-conditions pages for the probe condition, and returns the store.
func machineCacheFixtureStore(t *testing.T) *memory.Store {
	t.Helper()
	short, code, sentence := haltProbe(t)
	pages := []docsFixturePage{
		{"authoring/getting-started.md", authoringBody},
		{"language-reference/entities/component.md", langrefBody},
	}
	pages = append(pages, haltFixturePages(short, code, sentence)...)
	store := memory.New()
	if _, err := oci.Push(context.Background(), store, docsTestVersion, oci.MediaTypeDocs, docsBundleTar(t, pages), nil); err != nil {
		t.Fatalf("Push: %v", err)
	}
	return store
}

// signManifest signs the fixture bundle's manifest in store with a fresh key,
// attaches the signature, and returns the key's public PEM.
func signManifest(t *testing.T, store *memory.Store) []byte {
	t.Helper()
	ctx := context.Background()
	desc, err := store.Resolve(ctx, docsTestVersion)
	if err != nil {
		t.Fatalf("resolve the fixture manifest: %v", err)
	}
	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatal(err)
	}
	sig, artifactType, err := signer.Sign(ctx, desc.Digest.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oci.AttachReferrer(ctx, store, docsTestVersion, artifactType, sig); err != nil {
		t.Fatal(err)
	}
	return signer.PublicKeyPEM()
}

// countingDocsTarget serves store under the production pull-time verification for
// the session's own workspace (docsbundle.Verifier), and counts every reach.
func countingDocsTarget(store oras.ReadOnlyTarget, reaches *int) docsTargetFor {
	return func(tw *toolWorkspace, ref, version string, warn func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
		*reaches++
		return store, docsbundle.Verifier(tw.cfg, ref, version, store, warn), nil
	}
}

// docsSession returns a session over the workspace at ws, reading the fixture
// bundle from the cache root given and acquiring it through target.
func docsSession(ws, root string, target docsTargetFor, logw *bytes.Buffer) *server {
	return &server{
		out:               bytes.NewBuffer(nil),
		logw:              logw,
		serverVersion:     docsTestVersion,
		launchRoot:        ws,
		docsRoot:          root,
		docsBundleRef:     docsTestRef,
		docsBundleVersion: docsTestVersion,
		docsTarget:        target,
	}
}

// workspaceTrusting writes a workspace configuration whose entry for the fixture
// registry is entry, indented beneath the registries list, and returns its root.
func workspaceTrusting(t *testing.T, entry string) string {
	t.Helper()
	ws := t.TempDir()
	body := "workspace: {}\nregistries:\n  - host: " + docsTestHost + "\n" + entry
	if err := os.WriteFile(filepath.Join(ws, workspaceconfig.ConfigFilename), []byte(body), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
	return ws
}

// requiringKey is a registries-entry body requiring a signature and pinning key.
func requiringKey(key []byte) string {
	var b strings.Builder
	b.WriteString("    signing:\n      required: true\n    public_key: |\n")
	for _, line := range strings.Split(strings.TrimRight(string(key), "\n"), "\n") {
		b.WriteString("      " + line + "\n")
	}
	return b.String()
}

// TestALaunchWhereNoWorkspaceCacheCanBeCreatedServesTheDocumentation is the launch
// that used to download and verify the whole bundle and then discard it: the
// session is started over a folder where no workspace cache can be created, and
// the bundle is kept in the machine-wide cache instead, so the run's acquisition
// lands, a language-reference read answers the page, and explain-error carries
// the walk-through.
func TestALaunchWhereNoWorkspaceCacheCanBeCreatedServesTheDocumentation(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	if err := os.Chmod(ws, 0o555); err != nil {
		t.Fatalf("make the workspace folder read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(ws, 0o755) })
	// The precondition, without which the case below would pass over a folder a
	// cache could have been created in.
	if err := os.MkdirAll(filepath.Join(ws, workspaceconfig.Default().ResolvedArtifactCacheDir()), 0o755); err == nil {
		t.Skip("this process can create a directory in a read-only folder, so no folder here stands for one a cache cannot be created in")
	}

	root := t.TempDir()
	reaches := 0
	var logged bytes.Buffer
	s := docsSession(ws, root, countingDocsTarget(machineCacheFixtureStore(t), &reaches), &logged)

	runServer(t, s)

	if reaches != 1 {
		t.Fatalf("the run reached the bundle's registry %d times, want once; log:\n%s", reaches, logged.String())
	}
	if cached, err := docscache.Cached(root, docsTestRef, docsTestVersion); err != nil || !cached {
		t.Fatalf("the run left no bundle in the machine-wide cache (%v); log:\n%s", err, logged.String())
	}

	rr, rerr := readResource(t, s, "langref://entities/component")
	if rerr != nil {
		t.Fatalf("the language-reference read answered %+v after the run; log:\n%s", rerr, logged.String())
	}
	if len(rr.Contents) != 1 || rr.Contents[0].Text != langrefBody {
		t.Errorf("the language-reference read answered %+v, want the page", rr.Contents)
	}

	short, _, sentence := haltProbe(t)
	payload, _ := explainErrorPayload(t, s, explainErrorArgs{Message: haltDiagnostic(short, sentence)})
	if len(payload.Entries) == 0 || payload.Note != "" {
		t.Errorf("explain-error carried %d entries and the note %q, want the walk-through", len(payload.Entries), payload.Note)
	}
}

// TestASecondSessionReadsTheCopyTheFirstAcquired is the download the move ends:
// a second session over a different folder, on the same machine, finds the copy
// the first acquired and reaches the registry not at all.
func TestASecondSessionReadsTheCopyTheFirstAcquired(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store := machineCacheFixtureStore(t)

	firstReaches, secondReaches := 0, 0
	var firstLog, secondLog bytes.Buffer
	first := docsSession(t.TempDir(), root, countingDocsTarget(store, &firstReaches), &firstLog)
	second := docsSession(t.TempDir(), root, countingDocsTarget(store, &secondReaches), &secondLog)

	runServer(t, first)
	if firstReaches != 1 {
		t.Fatalf("the first session reached the registry %d times, want once; log:\n%s", firstReaches, firstLog.String())
	}

	runServer(t, second)
	if secondReaches != 0 {
		t.Errorf("the second session reached the registry %d times for a bundle already on this machine; log:\n%s",
			secondReaches, secondLog.String())
	}
	rr, rerr := readResource(t, second, "langref://entities/component")
	if rerr != nil || len(rr.Contents) != 1 || rr.Contents[0].Text != langrefBody {
		t.Errorf("the second session answered %+v (%+v), want the page the first acquired", rr.Contents, rerr)
	}
}

// TestASessionIsServedOnlyACopyItsOwnCheckAdmits holds the copy one session let
// in to the next session's own signature check.
//
// In the first row the first session's workspace skips verification for the
// registry, so its copy keeps no signature; the second's requires a signature from
// a key that did not sign the bundle. The second refuses the copy, fetches the
// bundle again under its own check, that check refuses the signature the registry
// serves, and the session is served no page — and its log says why. In the second
// row both workspaces pin the key that signed the bundle: the second session is
// served the first's copy and reaches the registry not at all.
func TestASessionIsServedOnlyACopyItsOwnCheckAdmits(t *testing.T) {
	t.Parallel()
	store := machineCacheFixtureStore(t)
	signer := signManifest(t, store)
	other := signManifest(t, memoryStoreWithFixture(t))

	for _, tc := range []struct {
		name          string
		first, second string
		served        bool
	}{
		{
			name:   "a copy a skipping workspace let in, read under a check that refuses its signer",
			first:  "    skip_signature_verification: true\n",
			second: requiringKey(other),
			served: false,
		},
		{
			name:   "a copy a requiring workspace let in, read under a check that admits its signer",
			first:  requiringKey(signer),
			second: requiringKey(signer),
			served: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			firstReaches, secondReaches := 0, 0
			var firstLog, secondLog bytes.Buffer
			first := docsSession(workspaceTrusting(t, tc.first), root, countingDocsTarget(store, &firstReaches), &firstLog)
			second := docsSession(workspaceTrusting(t, tc.second), root, countingDocsTarget(store, &secondReaches), &secondLog)

			runServer(t, first)
			if _, rerr := readResource(t, first, "langref://entities/component"); rerr != nil {
				t.Fatalf("the first session is not served the copy it acquired: %+v; log:\n%s", rerr, firstLog.String())
			}

			runServer(t, second)
			_, rerr := readResource(t, second, "langref://entities/component")
			if tc.served {
				if rerr != nil {
					t.Errorf("a session whose check admits the copy was refused it: %+v; log:\n%s", rerr, secondLog.String())
				}
				if secondReaches != 0 {
					t.Errorf("a session whose check admits the copy downloaded it again (%d reaches)", secondReaches)
				}
				return
			}
			if rerr == nil || rerr.Code != codeResourceNotFound {
				t.Fatalf("a session whose check refuses the copy was answered %+v, want resource-not-found; log:\n%s",
					rerr, secondLog.String())
			}
			for _, says := range []string{"not admitted", "fetching it again"} {
				if !strings.Contains(secondLog.String(), says) {
					t.Errorf("the refusing session's log does not say %q:\n%s", says, secondLog.String())
				}
			}
			short, _, sentence := haltProbe(t)
			payload, _ := explainErrorPayload(t, second, explainErrorArgs{Message: haltDiagnostic(short, sentence)})
			if len(payload.Entries) != 0 || !strings.Contains(payload.Note, "does not admit") {
				t.Errorf("explain-error in the refusing session carried %d entries and the note %q, "+
					"want the copy reported as refused", len(payload.Entries), payload.Note)
			}
		})
	}
}

// memoryStoreWithFixture is a second store holding the same fixture bundle, so a
// key can sign it without its signature being attached to the store the
// sessions read.
func memoryStoreWithFixture(t *testing.T) *memory.Store {
	t.Helper()
	return machineCacheFixtureStore(t)
}

// TestASessionWithNoCacheRootAcquiresNothingAndSaysWhy is a machine where no
// per-user cache directory resolves: the session acquires nothing, its log says
// why, and the documentation answers not-found as an unreachable registry's does.
func TestASessionWithNoCacheRootAcquiresNothingAndSaysWhy(t *testing.T) {
	t.Parallel()
	reaches := 0
	var logged bytes.Buffer
	_, rootErr := docsbundle.CacheRoot(docsbundle.WithCacheRoot(context.Background(), ""))
	if rootErr == nil {
		t.Fatal("a stated empty cache root resolved, so this case cannot stand for a machine with none")
	}
	s := docsSession(t.TempDir(), "", countingDocsTarget(machineCacheFixtureStore(t), &reaches), &logged)
	s.docsRootErr = rootErr

	runServer(t, s)

	if reaches != 0 {
		t.Errorf("a session with nowhere to keep the bundle reached the registry %d times", reaches)
	}
	if !strings.Contains(logged.String(), "not acquired") || !strings.Contains(logged.String(), "per-user cache directory") {
		t.Errorf("the log does not say why nothing was acquired:\n%s", logged.String())
	}
	if _, rerr := readResource(t, s, "langref://entities/component"); rerr == nil || rerr.Code != codeResourceNotFound {
		t.Errorf("the documentation answered %+v, want resource-not-found", rerr)
	}
	short, _, sentence := haltProbe(t)
	payload, _ := explainErrorPayload(t, s, explainErrorArgs{Message: haltDiagnostic(short, sentence)})
	if !strings.Contains(payload.Note, "per-user cache directory") {
		t.Errorf("explain-error does not say why the walk-through is missing: %q", payload.Note)
	}
}
