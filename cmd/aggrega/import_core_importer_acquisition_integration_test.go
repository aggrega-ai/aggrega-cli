// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

// End-to-end proof that an import acquires a core importer of the compiler's
// release the first time it needs it: fetched from the workspace's default
// registry, held to the digest the binary carries, verified as every fetched
// plugin is, written to the content cache and read from there by every later
// import with no registry reached — and refused, writing nothing, where it does
// not verify or cannot be fetched.
//
// Every case runs on each surface that carries the import, driven in this
// process at the real command tree with nothing pointed: the command, the
// assistant's import tool over its own transport, and the session's
// aggrega/import over its own framing (acquisitionSurfaces). Each surface is held
// to the one expectation its case states, so every surface answers a case with
// the same outcome. The assistant's import tool is driven once more over a
// listener, as a caller on another machine reaches it, holding the fetch to the
// bound a registry read over that listener carries.
//
// The cases run against a real OCI registry on loopback that the case stands up
// and can stop (startStoppableRegistry). The published module is the guest the
// release publishes: the stamped core importer the build writes beside the
// binary's sources, whose content digest is the one the binary carries. Nothing
// here contacts any registry but the one the case started, and the per-user
// locations every surface reads configuration and stored logins from are pointed
// at the test's own directories (isolateFromThisMachine).

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
	"github.com/aggrega-ai/aggrega-cli/internal/publishops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// acquiredCoreImporter is the core importer these cases acquire: the Langflow one.
const acquiredCoreImporter = "importer/langflow"

// acquiredFormat is the format every case names, the one acquiredCoreImporter
// serves.
const acquiredFormat = "langflow"

// acquisitionSourceName is where each case lays the Langflow source inside the
// workspace it imports in, so every surface names the same file — the assistant's
// tool over a listener included, which reads only inside the workspace it serves.
const acquisitionSourceName = "flow.json"

// heldToTheRelease is the words a refusal of a module that is not the one the
// binary accepts names that module's digest by.
const heldToTheRelease = "the digest this compiler release publishes"

// publishedCoreImporter is what the compiler's release publishes for one core
// importer and what the binary carries of it.
type publishedCoreImporter struct {
	// uri is the importer's bare identity, <socket>/<name>.
	uri string
	// plugin is the generated entity the binary carries, decoded.
	plugin types.Plugin
	// entity is that entity's bytes — the document the release publishes beside
	// the guest.
	entity []byte
	// module is the stamped guest, decompressed: the module the release publishes.
	module []byte
	// digest is the content digest the binary carries for the importer.
	digest string
}

// requirePublishedCoreImporter returns what the release publishes for the core
// importer uri and what this binary carries of it, and fails the test — never
// skips it — when either is missing or when the two disagree: the binary must
// carry the importer's entity and digest, the build must have written the stamped
// guest beside the binary's sources, and that guest must be the module the digest
// names, with the entity written beside it the one the binary carries.
func requirePublishedCoreImporter(t *testing.T, uri string) publishedCoreImporter {
	t.Helper()
	var carried *embedded.PublishedPlugin
	for _, p := range embedded.PublishedPlugins() {
		if p.URI == uri {
			found := p
			carried = &found
			break
		}
	}
	if carried == nil || strings.TrimSpace(carried.Digest) == "" || len(carried.Entity) == 0 {
		t.Fatalf("this binary carries no entity and digest for %s: build the core plugins with the repository's build "+
			"script, which writes what the binary carries of each core plugin it does not embed", uri)
	}
	var plugin types.Plugin
	if err := yaml.Unmarshal(carried.Entity, &plugin); err != nil {
		t.Fatalf("read the entity the binary carries for %s: %v", uri, err)
	}

	dir := filepath.Join("..", "..", "internal", "wasm", "embedded", "published-only")
	gz, err := os.ReadFile(filepath.Join(dir, plugin.Metadata.Name+".wasm.gz"))
	if err != nil {
		t.Fatalf("the stamped guest the release publishes for %s is not beside the binary's sources: %v; build the "+
			"core plugins with the repository's build script", uri, err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatalf("open the stamped guest for %s: %v", uri, err)
	}
	module, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("decompress the stamped guest for %s: %v", uri, err)
	}
	if got := digest.ContentDigest(module); got != carried.Digest {
		t.Fatalf("the stamped guest for %s has digest %s, but the binary carries %s: they are not one build",
			uri, got, carried.Digest)
	}
	published, err := os.ReadFile(filepath.Join(dir, plugin.Metadata.Name+".plugin.yaml"))
	if err != nil {
		t.Fatalf("the entity the release publishes for %s is not beside its guest: %v", uri, err)
	}
	if !bytes.Equal(published, carried.Entity) {
		t.Fatalf("the entity the release publishes for %s is not the one the binary carries", uri)
	}
	return publishedCoreImporter{uri: uri, plugin: plugin, entity: carried.Entity, module: module, digest: carried.Digest}
}

// publishCoreImporterModule publishes module under the core importer's entity to
// the registry at host, where the release publishes it — the location and the
// single manifest a plugin publication writes (publishops.BuildPushReference,
// oci.PushPluginManifest), tagged by the entity's version — and signs it with
// signer, unless signer is nil.
//
// It packs the manifest itself rather than running the publication command,
// because that command refuses a guest built from a working tree with
// uncommitted changes, which a guest built during development routinely is. The
// signature is made and attached by the publication's own atoms
// (publishops.SignDigest, publishops.AttachBundle).
func publishCoreImporterModule(t *testing.T, host string, imp publishedCoreImporter, module []byte, signer signing.Signer) {
	t.Helper()
	ctx := context.Background()
	reference := publishops.BuildPushReference(host, "Plugin", imp.plugin.Metadata.PluginType, imp.plugin.Metadata.Name)
	repo, err := oci.NewRemoteRepository(reference, &http.Client{})
	if err != nil {
		t.Fatalf("address %s: %v", reference, err)
	}
	annotations, err := oci.PluginSearchableMetadata(imp.plugin).Annotations()
	if err != nil {
		t.Fatalf("the searchable annotations of %s: %v", imp.uri, err)
	}
	manifestDigest, err := oci.PushPluginManifest(ctx, repo, imp.plugin.Metadata.Version, imp.entity, module, annotations)
	if err != nil {
		t.Fatalf("publish %s:%s: %v", reference, imp.plugin.Metadata.Version, err)
	}
	if signer == nil {
		return
	}
	bundle, artifactType, err := publishops.SignDigest(ctx, signer, manifestDigest)
	if err != nil {
		t.Fatalf("sign %s:%s: %v", reference, imp.plugin.Metadata.Version, err)
	}
	if err := publishops.AttachBundle(ctx, repo, imp.plugin.Metadata.Version, artifactType, bundle); err != nil {
		t.Fatalf("attach the signature of %s:%s: %v", reference, imp.plugin.Metadata.Version, err)
	}
}

// importerRepository is the repository, relative to the registry at host, the
// release publishes the core importer under — what a request about its module
// names after the registry's /v2/ prefix.
func importerRepository(host string, imp publishedCoreImporter) string {
	return strings.TrimPrefix(
		publishops.BuildPushReference(host, "Plugin", imp.plugin.Metadata.PluginType, imp.plugin.Metadata.Name), host+"/")
}

// keyedPublisher returns a keyed signer and its public key.
func keyedPublisher(t *testing.T) (signing.Signer, []byte) {
	t.Helper()
	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	return signer, pubPEM
}

// layLangflowSource writes the committed Langflow source at
// acquisitionSourceName in dir and returns its path.
func layLangflowSource(t *testing.T, dir string) string {
	t.Helper()
	committed, err := filepath.Abs(filepath.Join("..", "..", "internal", "compiler", "importersocket", "testdata",
		"langflow-flow.json"))
	if err != nil {
		t.Fatalf("locate the Langflow source: %v", err)
	}
	body, err := os.ReadFile(committed)
	if err != nil {
		t.Fatalf("the committed Langflow source is missing: %v", err)
	}
	path := filepath.Join(dir, acquisitionSourceName)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("lay the Langflow source: %v", err)
	}
	return path
}

// coreImporterWorkspace resolves a scratch workspace whose catalog folder holds the
// JSON parser and no importer, whose default registry is host — a registry that
// requires a signature by the key pubPEM — whose offline posture is offline, and
// whose configuration carries extra beside that. The Langflow source is laid at
// acquisitionSourceName in its root, and its content cache is empty.
func coreImporterWorkspace(t *testing.T, host string, pubPEM []byte, offline bool, extra string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	ws := t.TempDir()
	importopstest.LayJSONParser(t, ws, filepath.Join(ws, workspaceconfig.ConventionalCatalogDir))
	layLangflowSource(t, ws)
	body := signedPluginWorkspaceConfigBody(host, pubPEM)
	if offline {
		if strings.Count(body, "offline: false\n") != 1 {
			t.Fatalf("the registry configuration does not state the offline posture once:\n%s", body)
		}
		body = strings.Replace(body, "offline: false\n", "offline: true\n", 1)
	}
	writeWorkspaceConfig(t, ws, "workspace: {}\n"+body+extra)
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws})
	if rw.Config().Offline != offline {
		t.Fatalf("the workspace resolved with the offline posture %v; want %v", rw.Config().Offline, offline)
	}
	if got := rw.Config().DefaultRegistryLocation().Prefix(); got != host {
		t.Fatalf("the workspace's default registry is %q; want the registry the case started, %q", got, host)
	}
	if held := cacheFiles(t, rw); len(held) != 0 {
		t.Fatalf("the workspace's content cache is not empty before the first import: %v", held)
	}
	return rw
}

// referenceDrafts is the expectation every surface that imports successfully is
// held to: the drafts the same module drafts run from beside its entity in a
// catalog folder — the core importer's own entity, at its identity and version,
// in an offline workspace — rendered as the reporting delivery renders them.
//
// It refuses an expectation that would measure nothing: an import refused, halted
// or drafting nothing.
func referenceDrafts(t *testing.T, imp publishedCoreImporter) string {
	t.Helper()
	ws := t.TempDir()
	catalogDir := filepath.Join(ws, workspaceconfig.ConventionalCatalogDir)
	importopstest.LayJSONParser(t, ws, catalogDir)
	entityDir := filepath.Join(catalogDir, imp.plugin.Metadata.PluginType, imp.plugin.Metadata.Name, imp.plugin.Metadata.Version)
	writeFixture(t, filepath.Join(entityDir, "plugin.yaml"), string(imp.entity))
	if err := os.WriteFile(filepath.Join(entityDir, pluginpath.PluginLeaf(imp.uri)), imp.module, 0o644); err != nil {
		t.Fatalf("lay the module beside the entity: %v", err)
	}
	writeWorkspaceConfig(t, ws, "workspace: {}\noffline: true\n")
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws})

	res, err := importops.Import(context.Background(), rw, importops.Options{
		SourcePath:    layLangflowSource(t, t.TempDir()),
		SourceName:    acquisitionSourceName,
		Format:        acquiredFormat,
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: importPluginBytes,
	})
	if err != nil {
		t.Fatalf("the importer run from beside its entity in a catalog folder was refused: %v", err)
	}
	if res.Halted() || len(res.Files) == 0 {
		t.Fatalf("the importer run from a catalog folder drafted nothing usable: %+v", res.Diagnostics)
	}
	docs := make([]authoredcontent.Document, 0, len(res.Files))
	for _, f := range res.Files {
		docs = append(docs, authoredcontent.Document{Path: f.Path, Content: f.Content})
	}
	return renderDrafts(t, docs)
}

// renderDrafts renders drafted files as the reporting delivery renders them — the
// form the command writes to its standard output under --dry-run — so drafts any
// surface handed back are compared byte for byte in one form.
func renderDrafts(t *testing.T, docs []authoredcontent.Document) string {
	t.Helper()
	var b bytes.Buffer
	if err := authoredcontent.Report(&b, docs); err != nil {
		t.Fatalf("render the drafts: %v", err)
	}
	return b.String()
}

// workspaceFiles returns every file under the workspace root, relative to it.
func workspaceFiles(t *testing.T, rw *workspaceconfig.ResolvedWorkspace) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(rw.Root(), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, rerr := filepath.Rel(rw.Root(), path)
			if rerr != nil {
				return rerr
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the workspace %s: %v", rw.Root(), err)
	}
	return files
}

// cacheFiles returns the files workspaceFiles lists that sit in the workspace's
// content cache.
func cacheFiles(t *testing.T, rw *workspaceconfig.ResolvedWorkspace) []string {
	t.Helper()
	cache := filepath.ToSlash(filepath.Clean(rw.Config().ResolvedArtifactCacheDir())) + "/"
	var held []string
	for _, f := range workspaceFiles(t, rw) {
		if strings.HasPrefix(f, cache) {
			held = append(held, f)
		}
	}
	return held
}

// requireOnlyTheCacheWritten fails the test when a file outside the workspace's
// content cache appeared since before was listed: the import writes the module it
// acquired there and nowhere else — no lockfile, no record of trusted plugins.
func requireOnlyTheCacheWritten(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, before []string) {
	t.Helper()
	cache := filepath.ToSlash(filepath.Clean(rw.Config().ResolvedArtifactCacheDir())) + "/"
	for _, f := range workspaceFiles(t, rw) {
		if slices.Contains(before, f) || strings.HasPrefix(f, cache) {
			continue
		}
		t.Errorf("the import wrote %s, which is not in the content cache", f)
	}
}

// requireTheAcceptedModuleCached fails the test unless the workspace's content
// cache holds, at the slot the importer's digest addresses, exactly the module the
// binary accepts and the manifest it was fetched in.
func requireTheAcceptedModuleCached(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, imp publishedCoreImporter) {
	t.Helper()
	root, cacheDir := rw.Root(), rw.Config().ResolvedArtifactCacheDir()
	cached, err := oci.ReadCached(root, cacheDir, imp.uri, imp.digest)
	if err != nil {
		t.Fatalf("the content cache holds no module for %s at %s: %v", imp.uri, imp.digest, err)
	}
	if !bytes.Equal(cached, imp.module) {
		t.Errorf("the content cache holds a module for %s that is not the one the binary accepts", imp.uri)
	}
	manifest, err := oci.ReadCachedManifest(root, cacheDir, imp.uri, imp.digest)
	if err != nil {
		t.Fatalf("the content cache holds no manifest beside the module for %s: %v", imp.uri, err)
	}
	if err := oci.VerifyContentAgainstManifest(manifest, cached); err != nil {
		t.Errorf("the manifest cached beside the module for %s does not describe it: %v", imp.uri, err)
	}
}

// surfaceAnswer is one surface's answer to an import, reduced to what every
// surface carries alike: the drafts, rendered as renderDrafts renders them, or
// the text the surface refused the import with.
type surfaceAnswer struct {
	refused bool
	drafts  string
	text    string
}

// acquisitionSurface is one surface that carries the import. importIn imports the
// Langflow source laid at acquisitionSourceName in the workspace rooted at root,
// naming the format and asking to be handed the drafts rather than having them
// written, and returns the surface's answer.
type acquisitionSurface struct {
	name     string
	importIn func(t *testing.T, root string) surfaceAnswer
}

// acquisitionSurfaces is every surface that carries the import, each driven in
// this process through the real command tree, so what reaches the import from
// each is what its own production wiring hands it: the workspace it resolved,
// the context of the call, and the plugin resolution this package supplies.
func acquisitionSurfaces() []acquisitionSurface {
	return []acquisitionSurface{
		{name: "the command", importIn: importByCommand},
		{name: "the assistant's import tool", importIn: importByAssistantTool},
		{name: "the session's aggrega/import", importIn: importBySession},
	}
}

// importByCommand runs `aggrega --workspace-root root import <source> --dry-run
// --format langflow`. A run that exits unsuccessfully answers with what it wrote
// to the process error stream, which is where the command reports a failure.
func importByCommand(t *testing.T, root string) surfaceAnswer {
	t.Helper()
	code, stdout, _, process := runImportAtTheRoot(t, root, filepath.Join(root, acquisitionSourceName), "--dry-run",
		"--format", acquiredFormat)
	if code != 0 {
		return surfaceAnswer{refused: true, text: process}
	}
	return surfaceAnswer{drafts: stdout}
}

// acquisitionToolArguments is what the assistant's import tool is sent, on either
// transport.
func acquisitionToolArguments() map[string]any {
	return map[string]any{"source": acquisitionSourceName, "format": acquiredFormat, "dry_run": true}
}

// importByAssistantTool calls the assistant's import tool at `aggrega mcp` over
// its own transport.
func importByAssistantTool(t *testing.T, root string) surfaceAnswer {
	t.Helper()
	return assistantImportAnswer(t, callAssistantTool(t, root, "import", acquisitionToolArguments()))
}

// assistantImportAnswer reads the import tool's answer: the drafts its payload
// hands back, or the text of a refusal.
func assistantImportAnswer(t *testing.T, answer assistantToolAnswer) surfaceAnswer {
	t.Helper()
	if answer.isError {
		return surfaceAnswer{refused: true, text: answer.text}
	}
	var payload struct {
		Drafts []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		} `json:"drafts"`
	}
	if err := json.Unmarshal(answer.structured, &payload); err != nil {
		t.Fatalf("decode the import tool's payload: %v (body %s)", err, answer.structured)
	}
	docs := make([]authoredcontent.Document, 0, len(payload.Drafts))
	for _, d := range payload.Drafts {
		docs = append(docs, authoredcontent.Document{Path: d.Path, Content: d.Content})
	}
	return surfaceAnswer{drafts: renderDrafts(t, docs)}
}

// importBySession sends aggrega/import to `aggrega serve`, naming the workspace
// root as the output directory the drafted paths are relative to.
func importBySession(t *testing.T, root string) surfaceAnswer {
	t.Helper()
	frame, stderr := callSession(t, root, "aggrega/import", map[string]any{
		"source": acquisitionSourceName, "outputDir": ".", "format": acquiredFormat, "dryRun": true,
	})
	if raw, refused := frame["error"]; refused {
		var envelope struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatalf("decode the session's refusal: %v (body %s, stderr %s)", err, raw, stderr)
		}
		return surfaceAnswer{refused: true, text: envelope.Message}
	}
	var result struct {
		Files []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		} `json:"files"`
	}
	if err := json.Unmarshal(frame["result"], &result); err != nil {
		t.Fatalf("decode the session's answer: %v (body %s)", err, frame["result"])
	}
	docs := make([]authoredcontent.Document, 0, len(result.Files))
	for _, f := range result.Files {
		docs = append(docs, authoredcontent.Document{Path: f.Path, Content: f.Content})
	}
	return surfaceAnswer{drafts: renderDrafts(t, docs)}
}

// requireDrafted fails the test unless answer handed back exactly want.
func requireDrafted(t *testing.T, answer surfaceAnswer, want string) {
	t.Helper()
	if answer.refused {
		t.Fatalf("the import was refused: %s", answer.text)
	}
	if answer.drafts != want {
		t.Errorf("the import drafted\n%s\nwant\n%s", answer.drafts, want)
	}
}

// requireRefused fails the test unless answer refuses the import under condition:
// its text carries the condition's short code and registered sentence and every
// part of quote, and none of absent. Every surface frames the one failure in its
// own words, so this is what each says alike about one refused import.
func requireRefused(t *testing.T, answer surfaceAnswer, condition error, quote, absent []string) {
	t.Helper()
	if !answer.refused {
		t.Fatalf("the import was not refused; it drafted:\n%s", answer.drafts)
	}
	t.Logf("refusal: %s", answer.text)
	short, ok := cerrors.ShortCodeOf(condition)
	if !ok {
		t.Fatalf("the condition %v carries no short code, so no surface can name it", condition)
	}
	message, ok := cerrors.MessageOf(condition)
	if !ok {
		t.Fatalf("the condition %v carries no registered sentence", condition)
	}
	for _, part := range append([]string{short, message}, quote...) {
		if !strings.Contains(answer.text, part) {
			t.Errorf("the refusal does not carry %q: %s", part, answer.text)
		}
	}
	for _, part := range absent {
		if strings.Contains(answer.text, part) {
			t.Errorf("the refusal carries %q: %s", part, answer.text)
		}
	}
}

// TestIntegration_ImportAcquiresACoreImporterOnFirstUseAndRunsOfflineFromTheCache
// drives, on every surface, a workspace whose catalog holds the JSON parser and no
// importer, whose content cache is empty, and whose default registry is a
// registry the case started, requiring a signature by the publisher's key — the
// registry holding the Langflow core importer exactly as the release publishes
// it, signed.
//
// The first import fetches the importer, verifies it and runs it: the drafts are
// byte for byte the ones the same module produces run from beside its entity in a
// catalog folder, the content cache holds the module the binary accepts at the
// slot its digest addresses, and nothing but the cache was written. The registry
// is then stopped, and a second import produces the same drafts byte for byte,
// with no request about the importer reaching the registry: the module is read
// from the cache.
//
// 🔴 PLANTED DEFECTS:
//   - in resolveCarriedImporter (internal/importops/acquire.go), return after
//     acquireCarriedImporter rather than reading the module through the cascade,
//     or write it in acquireCarriedImporter at any other slot than the one the
//     digest addresses, and every surface's second import fails: the stopped
//     registry is asked for the module;
//   - delete the cat.CarriedImporterDigest branch from buildSeam's loader
//     (internal/importops/seam.go), and every surface's first import fails: the
//     core importer is handed to the surface's own resolution, which holds no
//     module of it;
//   - remove ResolveImportPlugin from the serve.RunConfig literal runServe builds
//     (cmd/aggrega/serve.go), and the session's row alone fails, refused for want
//     of a plugin resolver while the other two surfaces acquire — which is what
//     makes the rows three proofs rather than one.
func TestIntegration_ImportAcquiresACoreImporterOnFirstUseAndRunsOfflineFromTheCache(t *testing.T) {
	// Not parallel, rows included: every surface runs in this process, the command
	// swaps the process-wide error stream, and the environment is set for the whole
	// test.
	withCLIRegistrations(t, nil)
	isolateFromThisMachine(t)
	imp := requirePublishedCoreImporter(t, acquiredCoreImporter)
	signer, pubPEM := keyedPublisher(t)
	reference := referenceDrafts(t, imp)

	for _, surface := range acquisitionSurfaces() {
		t.Run(surface.name, func(t *testing.T) {
			reg := startStoppableRegistry(t)
			publishCoreImporterModule(t, reg.host, imp, imp.module, signer)
			rw := coreImporterWorkspace(t, reg.host, pubPEM, false, "")
			before := workspaceFiles(t, rw)

			first := surface.importIn(t, rw.Root())
			requireDrafted(t, first, reference)
			requireTheAcceptedModuleCached(t, rw, imp)
			requireOnlyTheCacheWritten(t, rw, before)

			reg.stopped.Store(true)
			second := surface.importIn(t, rw.Root())
			requireDrafted(t, second, first.drafts)
			if asked, all := reg.attemptsUnder(importerRepository(reg.host, imp)); len(asked) != 0 {
				t.Errorf("the second import asked the stopped registry about the importer: %v (every request: %v)",
					asked, all)
			}
		})
	}
}

// TestIntegration_ImportRefusesACoreImporterThatDoesNotVerify drives, on every
// surface, the workspace the first case drives, its content cache empty, against a
// registry holding a module that must not run — one fault per row — and holds each
// refusal to the check that makes it, with nothing left in the content cache.
//
// A module with one byte flipped, published and signed under the publisher's own
// key, so its signature verifies, is refused by the digest the binary carries:
// the condition for bytes that are not the ones named, naming that digest. The
// module the binary accepts, published with no signature where the workspace
// requires one, is refused by the signature check, under the condition for a
// required signature that is absent.
//
// 🔴 PLANTED DEFECTS, each turning one row red on every surface. In
// acquireCarriedImporter (internal/importops/acquire.go): delete the
// pluginpath.HoldToDigest call, and the flipped-byte row fails — the module is
// verified and written to the cache before the read refuses it, so the cache is
// not left empty; delete the pluginfetchverify.Verify call, and the unsigned row
// fails — the import runs.
func TestIntegration_ImportRefusesACoreImporterThatDoesNotVerify(t *testing.T) {
	// Not parallel, rows included: see the first case.
	withCLIRegistrations(t, nil)
	isolateFromThisMachine(t)
	imp := requirePublishedCoreImporter(t, acquiredCoreImporter)
	signer, pubPEM := keyedPublisher(t)

	for _, row := range unverifiableCoreImporterRows(t, imp, signer) {
		t.Run(row.name, func(t *testing.T) {
			for _, surface := range acquisitionSurfaces() {
				t.Run(surface.name, func(t *testing.T) {
					reg := startStoppableRegistry(t)
					publishCoreImporterModule(t, reg.host, imp, row.module, row.signer)
					rw := coreImporterWorkspace(t, reg.host, pubPEM, false, "")

					requireRefused(t, surface.importIn(t, rw.Root()), row.refusal, row.quote, nil)
					if held := cacheFiles(t, rw); len(held) != 0 {
						t.Errorf("the refused import left files in the content cache: %v", held)
					}
				})
			}
		})
	}
}

// unverifiableCoreImporterRow is one module a registry holds for the core importer
// that must not run, the signer it is published under, and the refusal it meets.
type unverifiableCoreImporterRow struct {
	name    string
	module  []byte
	signer  signing.Signer
	refusal error
	quote   []string
}

// unverifiableCoreImporterRows are the modules that must not run: one byte
// flipped and signed by the publisher, so only the digest refuses it; and the
// module the binary accepts, unsigned where a signature is required, so only the
// signature check refuses it.
func unverifiableCoreImporterRows(t *testing.T, imp publishedCoreImporter, signer signing.Signer) []unverifiableCoreImporterRow {
	t.Helper()
	flipped := slices.Clone(imp.module)
	flipped[len(flipped)-1] ^= 0xff
	if digest.ContentDigest(flipped) == imp.digest {
		t.Fatal("flipping a byte left the module's digest unchanged")
	}
	return []unverifiableCoreImporterRow{
		{
			name:    "one byte flipped, signed by the publisher",
			module:  flipped,
			signer:  signer,
			refusal: oci.ErrFetchIntegrity(),
			quote: []string{
				"content digest " + digest.ContentDigest(flipped),
				"does not match " + heldToTheRelease + " " + imp.digest,
			},
		},
		{
			name:    "the accepted module, unsigned where a signature is required",
			module:  imp.module,
			refusal: signing.ErrNoSignatureFound(),
			quote:   []string{`core importer "` + imp.uri + `" fetched from`},
		},
	}
}

// TestIntegration_ImportOfACoreImporterItsRegistryDoesNotHoldNamesThePlacingRemedy
// drives, on every surface, the workspace the first case drives, its content cache
// empty, against a registry that answers and holds no module of the importer. The
// import is refused under the condition for a plugin artifact that is not on this
// machine, naming the importer, its version and the location the registry holds
// nothing at, and naming placing the module beside an entity in a catalog folder
// as the remedy — not a run with the network, which would meet the same answer —
// and nothing is cached.
func TestIntegration_ImportOfACoreImporterItsRegistryDoesNotHoldNamesThePlacingRemedy(t *testing.T) {
	// Not parallel, rows included: see the first case.
	withCLIRegistrations(t, nil)
	isolateFromThisMachine(t)
	imp := requirePublishedCoreImporter(t, acquiredCoreImporter)
	_, pubPEM := keyedPublisher(t)

	for _, surface := range acquisitionSurfaces() {
		t.Run(surface.name, func(t *testing.T) {
			reg := startStoppableRegistry(t)
			rw := coreImporterWorkspace(t, reg.host, pubPEM, false, "")

			location := publishops.BuildPushReference(reg.host, "Plugin", imp.plugin.Metadata.PluginType,
				imp.plugin.Metadata.Name)
			requireRefused(t, surface.importIn(t, rw.Root()), pluginpath.ErrPluginArtifactNotFound(),
				[]string{
					`core importer "` + imp.uri + `" at version ` + imp.plugin.Metadata.Version + ` is not on this machine`,
					"holds no " + location + ":" + imp.plugin.Metadata.Version,
					"place its module, named " + pluginpath.PluginLeaf(imp.uri) + ", beside a Plugin entity of " + imp.uri,
				},
				[]string{"with the network available"})
			if held := cacheFiles(t, rw); len(held) != 0 {
				t.Errorf("the refused import left files in the content cache: %v", held)
			}
		})
	}
}

// TestIntegration_ImportWithNoRegistryReachableHaltsNamingTheImporterAndBothRemedies
// drives, on every surface, the workspace the first case drives, its content cache
// empty, with no registry it can reach: the registry stopped, and the workspace
// offline. Either way the import is refused under the condition for a plugin
// artifact that is not on this machine, naming the importer, its version, why it
// was not fetched, and the two ways to put its module on the machine — running
// once with the network, or placing the module beside an entity in a catalog
// folder — and nothing is cached. The offline workspace asks the registry nothing
// at all.
func TestIntegration_ImportWithNoRegistryReachableHaltsNamingTheImporterAndBothRemedies(t *testing.T) {
	// Not parallel, rows included: see the first case.
	withCLIRegistrations(t, nil)
	isolateFromThisMachine(t)
	imp := requirePublishedCoreImporter(t, acquiredCoreImporter)
	_, pubPEM := keyedPublisher(t)

	rows := []struct {
		name      string
		offline   bool
		reason    string
		asksNoOne bool
	}{
		{name: "the registry stopped", reason: "could not be reached"},
		{name: "the workspace offline", offline: true, reason: "the workspace is offline", asksNoOne: true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			for _, surface := range acquisitionSurfaces() {
				t.Run(surface.name, func(t *testing.T) {
					reg := startStoppableRegistry(t)
					reg.stopped.Store(true)
					rw := coreImporterWorkspace(t, reg.host, pubPEM, row.offline, "")

					requireRefused(t, surface.importIn(t, rw.Root()), pluginpath.ErrPluginArtifactNotFound(),
						[]string{
							`core importer "` + imp.uri + `" at version ` + imp.plugin.Metadata.Version + ` is not on this machine`,
							row.reason,
							"run the import once with the network available",
							"place its module, named " + pluginpath.PluginLeaf(imp.uri) + ", beside a Plugin entity of " + imp.uri,
							"in a catalog folder",
						}, nil)
					if held := cacheFiles(t, rw); len(held) != 0 {
						t.Errorf("the refused import left files in the content cache: %v", held)
					}
					asked, all := reg.attemptsUnder(importerRepository(reg.host, imp))
					switch {
					case row.asksNoOne && len(all) != 0:
						t.Errorf("the offline import asked the registry: %v", all)
					case !row.asksNoOne && len(asked) == 0:
						t.Errorf("the import never asked the stopped registry about the importer, so this row did not "+
							"drive a registry that does not answer (every request: %v)", all)
					}
				})
			}
		})
	}
}

// lockedLog is a goroutine-safe writer a listener running in this process writes
// its log stream to: the requests it answers are answered on goroutines of their
// own, each writing its own line.
type lockedLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// assistantListener is the assistant server serving one workspace over a
// listener, as a caller on another machine reaches it: started in this process
// through the real command tree as `aggrega mcp --transport http`, on a Unix
// socket, with its log stream kept for reading.
//
// stop ends the run and waits for the listener to be released, and may be called
// more than once.
type assistantListener struct {
	client *http.Client
	log    *lockedLog
	stop   func()
}

// serveOverAListener starts the assistant server over a listener for the
// workspace rooted at root, and returns once it is accepting. The run ends, and
// the listener with it, when the test does.
//
// The workspace's configuration says who issues the tokens the listener accepts,
// as a listener requires before it binds. A launch the listener refuses ends the
// test with the refusal, read off the log stream, rather than waiting out the
// liveness ceiling.
func serveOverAListener(t *testing.T, root string) assistantListener {
	t.Helper()
	socket := filepath.Join(mcpSocketDir(t), "s")
	log := &lockedLog{}
	cmd := newRootCmd()
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)
	cmd.SetErr(log)
	cmd.SetArgs([]string{"mcp", "--workspace-root", root, "--transport", "http", "--listen", "unix:" + socket})

	ctx, cancel := context.WithCancel(context.Background())
	ended := make(chan struct{})
	var code int
	go func() {
		code = executeRoot(ctx, cmd)
		close(ended)
	}()
	// The ceiling bounds a listener that never stops; it says nothing about how
	// quickly one should.
	stop := func() {
		cancel()
		select {
		case <-ended:
		case <-time.After(responseLivenessCeiling(t)):
			t.Errorf("the listener did not stop within the liveness ceiling after its run ended:\n%s", log.String())
		}
	}
	t.Cleanup(stop)

	deadline := time.Now().Add(responseLivenessCeiling(t))
	for time.Now().Before(deadline) {
		select {
		case <-ended:
			t.Fatalf("aggrega mcp ended with exit %d before it was accepting:\n%s", code, log.String())
		default:
		}
		if conn, err := net.Dial("unix", socket); err == nil {
			_ = conn.Close()
			return assistantListener{client: mcpUnixClient(socket), log: log, stop: stop}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("nothing was accepting at %s within the liveness ceiling:\n%s", socket, log.String())
	return assistantListener{}
}

// importIn calls the import tool over the listener, presenting token, in the
// revision a request states for itself, with the arguments the tool is sent on
// every transport.
func (l assistantListener) importIn(t *testing.T, token string) surfaceAnswer {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name":      "import",
			"arguments": acquisitionToolArguments(),
			"_meta": map[string]any{
				"io.modelcontextprotocol/protocolVersion":    mcpPerRequestRevision,
				"io.modelcontextprotocol/clientCapabilities": map[string]any{},
			},
		},
	})
	if err != nil {
		t.Fatalf("encode the import call: %v", err)
	}
	got := postToTheEndpoint(t, l.client, token, "tools/call", "import", string(body))
	if got.status != http.StatusOK {
		t.Fatalf("the import call over the listener was answered %d: %s\nlog:\n%s", got.status, got.body, l.log.String())
	}
	var frame rpcFrame
	if err := json.Unmarshal(got.body, &frame); err != nil {
		t.Fatalf("the answer over the listener is not a protocol frame: %v (body %s)", err, got.body)
	}
	if frame.Error != nil {
		t.Fatalf("the import call over the listener was not dispatched: %s", frame.Error.Message)
	}
	return assistantImportAnswer(t, decodeAssistantToolAnswer(t, "import", frame.Result))
}

// importCallReaches stops the listener and returns, for every call to the import
// tool its log stream records, the class the call reached as its operation
// declares it and the class it escalated to, empty where it escalated nothing.
//
// It stops the listener first because a request's line is written as its
// handler returns, which can be after the caller has read the answer; a stopped
// listener has let every call it was answering finish, so every line is there.
func (l assistantListener) importCallReaches(t *testing.T) [][2]string {
	t.Helper()
	l.stop()
	var reaches [][2]string
	for _, line := range strings.Split(l.log.String(), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) != nil {
			continue
		}
		if _, lifecycle := record["event"]; lifecycle {
			continue
		}
		if record["method"] != "tools/call" || record["target"] != "import" {
			continue
		}
		reach, _ := record["reach"].(string)
		escalated, _ := record["reach_escalated_to"].(string)
		reaches = append(reaches, [2]string{reach, escalated})
	}
	return reaches
}

// importDeclaredReach returns the class the import operation declares — the one
// its tool is listed under and admitted by — failing the test where the
// operation set holds no import.
func importDeclaredReach(t *testing.T) opsurface.Reach {
	t.Helper()
	op, known := opsurface.Lookup("import")
	if !known {
		t.Fatal("the operation set holds no import operation")
	}
	return op.Reach
}

// TestIntegration_AnImportOverTheListenerAcquiresACoreImporterUnderTheBoundARegistryReadThereCarries
// drives the assistant's import tool over a listener, as a caller on another
// machine reaches it, against a workspace whose default registry is a registry the
// case started, requiring a signature by the publisher's key. The caller presents
// a token granting the workspace-write scope alone: the one the import's declared
// class needs, and not the one a registry class would.
//
// The registry the import fetches from is the served workspace's default registry
// — the only registry the acquisition ever binds to, and one the workspace's own
// configuration admits as configured, which is the bound a registry read over
// this listener is held to. Where the module there is the one the binary accepts,
// signed, the import fetches it, verifies it, caches it and drafts exactly what the
// same module drafts from a catalog folder, writing nothing but the cache; with the
// registry then stopped a second import over the listener drafts the same bytes
// from the cache, asking the registry nothing about the importer. Where the
// module does not verify — one byte flipped, or unsigned where a signature is
// required — it is refused under the condition and in the words every surface at
// the host's own keyboard refuses it with (unverifiableCoreImporterRows), and
// nothing is cached.
//
// Every call reached exactly the class the import operation declares, and
// escalated to none — read off the line the listener records for the call — and
// the class the operation declares is the same after the fetches as before.
//
// No case here names a registry the workspace is not configured with, because no
// import can: the location the acquisition fetches from is decided from the
// served workspace's configuration alone, and a caller on a listener cannot name
// another workspace. That construction is held, for every way a default registry
// can be written, by the import operation's own coverage
// (TestACoreImporterIsFetchedOnlyFromARegistryTheWorkspaceIsConfiguredWith).
//
// 🔴 PLANTED DEFECTS:
//   - refuse the import tool's call where it arrives over a listener
//     (internal/mcp/tool_import.go, importInvoke, before importops.Import) and the
//     accepted-module row fails: nothing is fetched over the listener;
//   - declare the import operation's class as the registry one
//     (internal/opsurface/opsurface.go) and every row fails: the declared-class
//     assertions, and the call itself, refused for want of the registry scope;
//   - delete the pluginfetchverify.Verify call in acquireCarriedImporter
//     (internal/importops/acquire.go) and the unsigned row fails over the
//     listener as it does at the keyboard.
func TestIntegration_AnImportOverTheListenerAcquiresACoreImporterUnderTheBoundARegistryReadThereCarries(t *testing.T) {
	// Not parallel, rows included: see the first case.
	withCLIRegistrations(t, nil)
	isolateFromThisMachine(t)
	declared := importDeclaredReach(t)
	if declared != opsurface.ReachWorkspace {
		t.Fatalf("the import operation declares the %q class; this proof is about the %q one", declared,
			opsurface.ReachWorkspace)
	}
	imp := requirePublishedCoreImporter(t, acquiredCoreImporter)
	signer, pubPEM := keyedPublisher(t)
	reference := referenceDrafts(t, imp)
	authorization := startMCPAuthorizationServer(t)
	token := authorization.mintGranting(t, mcpWorkspaceWriteScope)
	served := "mcp:\n  authorization:\n    issuer: " + authorization.issuer() + "\n    resource: " + mcpPublishedResource + "\n"

	rows := []unverifiableCoreImporterRow{{name: "the accepted module, signed by the publisher", module: imp.module,
		signer: signer}}
	rows = append(rows, unverifiableCoreImporterRows(t, imp, signer)...)
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			reg := startStoppableRegistry(t)
			publishCoreImporterModule(t, reg.host, imp, row.module, row.signer)
			rw := coreImporterWorkspace(t, reg.host, pubPEM, false, served)
			if got := rw.Config().DefaultRegistryLocation().Host; got != reg.host {
				t.Fatalf("the served workspace's default registry is %q, not the registry the case started, %q", got,
					reg.host)
			}
			if !rw.Config().IsConfiguredRegistry(reg.host) {
				t.Fatalf("the served workspace does not admit its own default registry %q as configured", reg.host)
			}
			listener := serveOverAListener(t, rw.Root())
			before := workspaceFiles(t, rw)

			answer := listener.importIn(t, token)
			calls := 1
			if row.refusal != nil {
				requireRefused(t, answer, row.refusal, row.quote, nil)
				if held := cacheFiles(t, rw); len(held) != 0 {
					t.Errorf("the refused import left files in the content cache: %v", held)
				}
			} else {
				requireDrafted(t, answer, reference)
				requireTheAcceptedModuleCached(t, rw, imp)
				requireOnlyTheCacheWritten(t, rw, before)

				reg.stopped.Store(true)
				requireDrafted(t, listener.importIn(t, token), answer.drafts)
				calls++
				if asked, all := reg.attemptsUnder(importerRepository(reg.host, imp)); len(asked) != 0 {
					t.Errorf("the second import over the listener asked the stopped registry about the importer: %v "+
						"(every request: %v)", asked, all)
				}
			}

			reaches := listener.importCallReaches(t)
			if len(reaches) != calls {
				t.Fatalf("the listener recorded %d call(s) to the import tool; this row made %d:\n%s", len(reaches),
					calls, listener.log.String())
			}
			for _, reach := range reaches {
				if reach[0] != string(declared) || reach[1] != "" {
					t.Errorf("a call to the import tool reached %q and escalated to %q; it declares %q and escalates "+
						"to nothing", reach[0], reach[1], declared)
				}
			}
		})
	}
	if after := importDeclaredReach(t); after != declared {
		t.Errorf("the import operation declares the %q class after the fetches, and declared %q before", after, declared)
	}
}
