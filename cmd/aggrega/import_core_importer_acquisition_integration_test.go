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
// Every case runs the import operation the command runs, with the command's own
// plugin resolution, against a real OCI registry on loopback that the case stands
// up and can stop (startStoppableRegistry). The published module is the guest the
// release publishes: the stamped core importer the build writes beside the
// binary's sources, whose content digest is the one the binary carries. Nothing
// here contacts any registry but the one the case started.

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/internal/publishops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// acquiredCoreImporter is the core importer these cases acquire: the Langflow one.
const acquiredCoreImporter = "importer/langflow"

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
		t.Fatalf("this binary carries no entity and digest for %s: build the core plugins with the repository's "+
			"build script, which writes what the binary carries of each core plugin it does not embed", uri)
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

// coreImporterWorkspace resolves a scratch workspace whose catalog folder holds the
// JSON parser and no importer, whose default registry is host — a registry that
// requires a signature by the key pubPEM — and whose offline posture is offline.
// Its content cache is empty.
func coreImporterWorkspace(t *testing.T, host string, pubPEM []byte, offline bool) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	ws := t.TempDir()
	importopstest.LayJSONParser(t, ws, filepath.Join(ws, workspaceconfig.ConventionalCatalogDir))
	body := signedPluginWorkspaceConfigBody(host, pubPEM)
	if offline {
		if strings.Count(body, "offline: false\n") != 1 {
			t.Fatalf("the registry configuration does not state the offline posture once:\n%s", body)
		}
		body = strings.Replace(body, "offline: false\n", "offline: true\n", 1)
	}
	writeWorkspaceConfig(t, ws, "workspace: {}\n"+body)
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws})
	if rw.Config().Offline != offline {
		t.Fatalf("the workspace resolved with the offline posture %v; want %v", rw.Config().Offline, offline)
	}
	if got := rw.Config().DefaultRegistryLocation().Prefix(); got != host {
		t.Fatalf("the workspace's default registry is %q; want the registry the case started, %q", got, host)
	}
	return rw
}

// catalogFolderCoreImporterWorkspace resolves an offline scratch workspace whose
// catalog folder holds the JSON parser and the core importer's own entity — the
// one the binary carries, at its identity and version — with module laid beside
// it, so an import runs that module from the catalog folder as it runs any
// importer authored there.
func catalogFolderCoreImporterWorkspace(t *testing.T, imp publishedCoreImporter, module []byte) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	ws := t.TempDir()
	catalogDir := filepath.Join(ws, workspaceconfig.ConventionalCatalogDir)
	importopstest.LayJSONParser(t, ws, catalogDir)
	entityDir := filepath.Join(catalogDir, imp.plugin.Metadata.PluginType, imp.plugin.Metadata.Name, imp.plugin.Metadata.Version)
	writeFixture(t, filepath.Join(entityDir, "plugin.yaml"), string(imp.entity))
	if err := os.WriteFile(filepath.Join(entityDir, pluginpath.PluginLeaf(imp.uri)), module, 0o644); err != nil {
		t.Fatalf("lay the module beside the entity: %v", err)
	}
	writeWorkspaceConfig(t, ws, "workspace: {}\noffline: true\n")
	return resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws})
}

// langflowSource returns the committed Langflow source the cases import.
func langflowSource(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "internal", "compiler", "importersocket", "testdata", "langflow-flow.json"))
	if err != nil {
		t.Fatalf("locate the Langflow source: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the committed Langflow source is missing: %v", err)
	}
	return path
}

// importLangflow runs the import operation over the Langflow source in rw, naming
// the format and reporting rather than writing, through the plugin resolution the
// command hands it.
func importLangflow(t *testing.T, rw *workspaceconfig.ResolvedWorkspace) (importops.Result, error) {
	t.Helper()
	source := langflowSource(t)
	return importops.Import(context.Background(), rw, importops.Options{
		SourcePath:    source,
		SourceName:    filepath.Base(source),
		Format:        "langflow",
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: importPluginBytes,
	})
}

// drafts renders every drafted file a result carries, in the importer's order, as
// its path and its content, so two imports are compared byte for byte.
func drafts(res importops.Result) []string {
	out := make([]string, 0, len(res.Files))
	for _, f := range res.Files {
		out = append(out, f.Path+"\x00"+f.Content)
	}
	return out
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

// TestIntegration_ImportAcquiresACoreImporterOnFirstUseAndRunsOfflineFromTheCache
// drives a workspace whose catalog holds the JSON parser and no importer, whose
// content cache is empty, and whose default registry is a registry the case
// started, requiring a signature by the publisher's key — the registry holding the
// Langflow core importer exactly as the release publishes it, signed.
//
// The first import fetches the importer, verifies it and runs it: the drafts are
// byte for byte the ones the same module produces run from beside its entity in a
// catalog folder, the content cache holds the module the binary accepts at the
// slot its digest addresses, and nothing but the cache was written. The registry
// is then stopped, and a second import produces the same drafts byte for byte,
// with no request reaching the registry: the module is read from the cache.
//
// 🔴 PLANTED DEFECT: in resolveCarriedImporter (internal/importops/acquire.go),
// return after acquireCarriedImporter rather than reading the module through the
// cascade, or write it in acquireCarriedImporter at any other slot than the one
// the digest addresses, and the second import fails: the stopped registry is
// asked for the module.
func TestIntegration_ImportAcquiresACoreImporterOnFirstUseAndRunsOfflineFromTheCache(t *testing.T) {
	imp := requirePublishedCoreImporter(t, acquiredCoreImporter)
	signer, pubPEM := keyedPublisher(t)
	reg := startStoppableRegistry(t)
	publishCoreImporterModule(t, reg.host, imp, imp.module, signer)

	reference, err := importLangflow(t, catalogFolderCoreImporterWorkspace(t, imp, imp.module))
	if err != nil {
		t.Fatalf("the importer run from beside its entity in a catalog folder was refused: %v", err)
	}
	if reference.Halted() || len(reference.Files) == 0 {
		t.Fatalf("the importer run from a catalog folder drafted nothing usable: %+v", reference.Diagnostics)
	}

	rw := coreImporterWorkspace(t, reg.host, pubPEM, false)
	if held := cacheFiles(t, rw); len(held) != 0 {
		t.Fatalf("the workspace's content cache is not empty before the first import: %v", held)
	}
	before := workspaceFiles(t, rw)

	first, err := importLangflow(t, rw)
	if err != nil {
		t.Fatalf("the first import, with the registry up, was refused: %v", err)
	}
	if first.Halted() {
		t.Fatalf("the importer halted: %+v", first.Diagnostics)
	}
	if !slices.Equal(drafts(first), drafts(reference)) {
		t.Errorf("the drafts of the acquired importer differ from the ones the same module drafts from a catalog "+
			"folder:\nacquired: %q\ncatalog folder: %q", drafts(first), drafts(reference))
	}
	requireTheAcceptedModuleCached(t, rw, imp)
	requireOnlyTheCacheWritten(t, rw, before)

	reg.stopped.Store(true)
	second, err := importLangflow(t, rw)
	if err != nil {
		t.Fatalf("the second import, with the registry stopped, was refused: %v", err)
	}
	if !slices.Equal(drafts(second), drafts(first)) {
		t.Errorf("the second import drafted different bytes from the first:\nsecond: %q\nfirst: %q",
			drafts(second), drafts(first))
	}
	if _, all := reg.attemptsUnder(""); len(all) != 0 {
		t.Errorf("the second import reached the stopped registry: %v", all)
	}
}

// TestIntegration_ImportRefusesACoreImporterThatDoesNotVerify drives the workspace
// the first case drives, its content cache empty, against a registry holding a
// module that must not run — one fault per row — and holds each refusal to the
// check that makes it, with nothing left in the content cache.
//
// A module with one byte flipped, published and signed under the publisher's own
// key, so its signature verifies, is refused by the digest the binary carries:
// the condition for bytes that are not the ones named, naming that digest. The
// module the binary accepts, published with no signature where the workspace
// requires one, is refused by the signature check, under the condition for a
// required signature that is absent.
//
// 🔴 PLANTED DEFECTS, each turning one row red. In acquireCarriedImporter
// (internal/importops/acquire.go): delete the pluginpath.HoldToDigest call, and
// the flipped-byte row fails — the module is verified and written to the cache
// before the read refuses it, so the cache is not left empty; delete the
// pluginfetchverify.Verify call, and the unsigned row fails — the import runs.
func TestIntegration_ImportRefusesACoreImporterThatDoesNotVerify(t *testing.T) {
	imp := requirePublishedCoreImporter(t, acquiredCoreImporter)
	signer, pubPEM := keyedPublisher(t)

	flipped := slices.Clone(imp.module)
	flipped[len(flipped)-1] ^= 0xff
	if digest.ContentDigest(flipped) == imp.digest {
		t.Fatal("flipping a byte left the module's digest unchanged")
	}

	rows := []struct {
		name    string
		module  []byte
		signer  signing.Signer
		refusal error
		quote   []string
	}{
		{
			name:    "one byte flipped, signed by the publisher",
			module:  flipped,
			signer:  signer,
			refusal: oci.ErrFetchIntegrity(),
			quote:   []string{"content digest " + digest.ContentDigest(flipped), "does not match " + heldToTheRelease + " " + imp.digest},
		},
		{
			name:    "the accepted module, unsigned where a signature is required",
			module:  imp.module,
			refusal: signing.ErrNoSignatureFound(),
			quote:   []string{`core importer "` + imp.uri + `" fetched from`},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			reg := startStoppableRegistry(t)
			publishCoreImporterModule(t, reg.host, imp, row.module, row.signer)
			rw := coreImporterWorkspace(t, reg.host, pubPEM, false)

			_, err := importLangflow(t, rw)
			if err == nil {
				t.Fatal("the import ran a module that does not verify")
			}
			t.Logf("refusal: %v", err)
			if !errors.Is(err, row.refusal) {
				t.Fatalf("the import was not refused under the condition this row's fault raises: %v", err)
			}
			for _, part := range row.quote {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("the refusal does not carry %q: %v", part, err)
				}
			}
			if held := cacheFiles(t, rw); len(held) != 0 {
				t.Errorf("the refused import left files in the content cache: %v", held)
			}
		})
	}
}

// TestIntegration_ImportOfACoreImporterItsRegistryDoesNotHoldNamesThePlacingRemedy
// drives the workspace the first case drives, its content cache empty, against a
// registry that answers and holds no module of the importer. The import is
// refused under the condition for a plugin artifact that is not on this machine,
// naming the importer, its version and the location the registry holds nothing
// at, and naming placing the module beside an entity in a catalog folder as the
// remedy — not a run with the network, which would meet the same answer — and
// nothing is cached.
func TestIntegration_ImportOfACoreImporterItsRegistryDoesNotHoldNamesThePlacingRemedy(t *testing.T) {
	imp := requirePublishedCoreImporter(t, acquiredCoreImporter)
	_, pubPEM := keyedPublisher(t)
	reg := startStoppableRegistry(t)
	rw := coreImporterWorkspace(t, reg.host, pubPEM, false)

	_, err := importLangflow(t, rw)
	if err == nil {
		t.Fatal("the import ran with no module of the core importer anywhere")
	}
	t.Logf("refusal: %v", err)
	if !errors.Is(err, pluginpath.ErrPluginArtifactNotFound()) {
		t.Fatalf("the import was not refused as a plugin artifact not on this machine: %v", err)
	}
	location := publishops.BuildPushReference(reg.host, "Plugin", imp.plugin.Metadata.PluginType, imp.plugin.Metadata.Name)
	for _, part := range []string{
		`core importer "` + imp.uri + `" at version ` + imp.plugin.Metadata.Version + ` is not on this machine`,
		"holds no " + location + ":" + imp.plugin.Metadata.Version,
		"place its module, named " + pluginpath.PluginLeaf(imp.uri) + ", beside a Plugin entity of " + imp.uri,
	} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("the refusal does not carry %q: %v", part, err)
		}
	}
	if strings.Contains(err.Error(), "with the network available") {
		t.Errorf("the refusal names a run with the network as a remedy, though the registry answered: %v", err)
	}
	if held := cacheFiles(t, rw); len(held) != 0 {
		t.Errorf("the refused import left files in the content cache: %v", held)
	}
}

// TestIntegration_ImportWithNoRegistryReachableHaltsNamingTheImporterAndBothRemedies
// drives the workspace the first case drives, its content cache empty, with no
// registry it can reach: the registry stopped, and the workspace offline. Either
// way the import is refused under the condition for a plugin artifact that is not
// on this machine, naming the importer, its version, why it was not fetched, and
// the two ways to put its module on the machine — running once with the network,
// or placing the module beside an entity in a catalog folder — and nothing is
// cached. The offline workspace asks the registry nothing at all.
func TestIntegration_ImportWithNoRegistryReachableHaltsNamingTheImporterAndBothRemedies(t *testing.T) {
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
			reg := startStoppableRegistry(t)
			reg.stopped.Store(true)
			rw := coreImporterWorkspace(t, reg.host, pubPEM, row.offline)

			_, err := importLangflow(t, rw)
			if err == nil {
				t.Fatal("the import ran with no module of the core importer on this machine and no registry reachable")
			}
			t.Logf("refusal: %v", err)
			if !errors.Is(err, pluginpath.ErrPluginArtifactNotFound()) {
				t.Fatalf("the import was not refused as a plugin artifact not on this machine: %v", err)
			}
			for _, part := range []string{
				`core importer "` + imp.uri + `" at version ` + imp.plugin.Metadata.Version + ` is not on this machine`,
				row.reason,
				"run the import once with the network available",
				"place its module, named " + pluginpath.PluginLeaf(imp.uri) + ", beside a Plugin entity of " + imp.uri,
				"in a catalog folder",
			} {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("the refusal does not carry %q: %v", part, err)
				}
			}
			if held := cacheFiles(t, rw); len(held) != 0 {
				t.Errorf("the refused import left files in the content cache: %v", held)
			}
			_, all := reg.attemptsUnder("")
			switch {
			case row.asksNoOne && len(all) != 0:
				t.Errorf("the offline import asked the registry: %v", all)
			case !row.asksNoOne && len(all) == 0:
				t.Error("the import never asked the stopped registry, so this row did not drive a registry that " +
					"does not answer")
			}
		})
	}
}
