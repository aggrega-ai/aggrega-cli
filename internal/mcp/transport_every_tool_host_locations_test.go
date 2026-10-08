// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/aggrega-ai/aggrega-cli/internal/cacheops"
	"github.com/aggrega-ai/aggrega-cli/internal/gencatalog"
	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/attribindex"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/provindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegment"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/gitrepo"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// What every tool tells a caller about a place on this host, read on both
// transports over one workspace.
//
// Each call is sent twice — over the process's own streams, and over a real
// listener with a credential admitting every tool — and the two answers are read
// together. A caller that launched this process is told every location as the
// operation holds it. A caller reaching a listener is told a location inside the
// workspace by its name relative to the workspace root, and nothing about any
// other location, the root itself included. So the two structured answers must
// agree everywhere but at a location, and must disagree at every location: a
// location carried through unchanged reads as two answers agreeing, which is the
// failure this file exists for, and an answer that kept everything back would
// disagree where nothing named a place.
//
// The whole listener frame is then searched as raw bytes for the workspace root,
// for every location the workspace is configured with, and for the directory
// above the root, which is where a climb out of it lands. A walk of two answers
// can only judge the fields it reaches; the byte search is what catches a
// location in a text block, or in a field nothing here was written to expect.
//
// 🔴 What this check covers, and what it does not:
//
//   - A text block is searched as raw bytes only, and is not compared word by
//     word. It is prose a tool composes for its caller, and it may say different
//     words to the two callers about the same answer.
//   - An answer's prose spells only the locations the workspace holds as a caller
//     may be told them: its root and anything under it, the config files its
//     configuration was read from, and the directories its catalog is read from.
//     A location outside all of those that a lower layer composes into prose, and
//     that this fixture did not configure, is neither rewritten by the server nor
//     searched for here.
//   - Every place the fixture writes sits under one directory, so a value names a
//     place on this host here when it carries that directory, or when it is, as a
//     whole, an absolute path beginning with a directory this host holds. A place
//     spelled some other way is not recognised.
//   - A call whose two answers differ by design for a reason that is not a
//     location — which credential a signing call spends, how each transport
//     advertises a tool's own definition — is searched as raw bytes and not
//     walked, and its row says why.
//   - The machine-wide caches the cache inventory reads are the fixture's own,
//     seeded with what the rule has to tell apart. The ones this process owns
//     sit where the process found them when it started, whatever the
//     environment says afterwards, and they hold whatever the tests before this
//     one left there — so a comparison over them would judge a different cache
//     on every machine, and none at all on a fresh one. The workspace's own
//     source-artifact cache is seeded the same way, with slots whose labels spell
//     a repository on this host and one whose label names only a network host.

// Spellings the fixture's calls send, each distinctive enough that finding one
// in an answer cannot be a coincidence.
const (
	// hostLocationRootName is the workspace root's own directory name.
	hostLocationRootName = "ws-root-q7x3"

	// hostLocationClimb climbs out of the workspace root. It lands in the
	// directory holding the root, which the byte search covers.
	hostLocationClimb = "../climb-target-q7x3.yaml"

	// hostLocationRegistry is the registry the workspace names by default. It is
	// never reached: the workspace declares itself offline.
	hostLocationRegistry = "registry-z3q9.invalid:5000"

	// hostLocationKeptBackWords is what prose reads where it would have named a
	// place a caller reaching a listener is not told. It is restated here rather
	// than read from the package, because it is the phrase the reference
	// documentation tells a reader to expect, and a change to it is a change this
	// check should notice.
	hostLocationKeptBackWords = "(location withheld)"

	// hostLocationsWithheldKey is the statement an answer makes about itself — that
	// it is one that keeps locations back — rather than a mark on a sibling value.
	hostLocationsWithheldKey = "host_locations_withheld"
)

// hostLocationFixture is one workspace every tool is asked about, and every
// place on this host that workspace names.
type hostLocationFixture struct {
	// base holds the workspace and every location outside it that the workspace
	// names, so every place an answer could name for this fixture lies under it.
	base string
	root string

	outsideCatalog   string
	outsideEntity    string
	keyPath          string
	pluginPath       string
	machineWide      string
	authoringRelease string

	// machineCaches holds the machine-wide caches every session of this fixture
	// reads in place of the ones this process owns.
	machineCaches string

	// localRepos is the directory outside the root holding the repositories the
	// workspace's source-artifact cache records slots read from.
	localRepos string

	// pristine is the workspace as every call finds it. It is copied back before
	// each call on each transport, so the two transports answer the same bytes at
	// the same path and a call that writes leaves nothing behind for the next.
	pristine string
}

// newHostLocationFixture writes the workspace and everything it names, resolves
// its Release's dependencies once so the Release has a lockfile, and keeps a copy
// every call starts from.
//
// The workspace names a location through every kind of setting a place can be
// named by: a catalog directory inside the root and one outside it, a per-Release
// catalog entry, a signing key file outside, an authoring Release named
// absolutely, and a plugin reference that is a path outside. The machine-wide
// configuration layer is a file outside the root, reached through the override
// the configuration reads, which also keeps the cascade read here the one written
// here rather than that of whoever runs the tests. It also carries what the tools
// need to reach their answers: an entity lint objects to, a Recipe the pin step
// can pin, a document an edit can address, a source an import can read, an
// artifact a publication can bundle, and a module file inside the root that is
// not a module at all. And it holds machine-wide caches of its own, seeded as
// seedMachineWideCaches says, which every session it equips reads, and a
// source-artifact cache seeded as seedSourceArtifactSlots says.
func newHostLocationFixture(t *testing.T) *hostLocationFixture {
	t.Helper()
	base := t.TempDir()
	fx := &hostLocationFixture{
		base:           base,
		root:           filepath.Join(base, hostLocationRootName),
		outsideCatalog: filepath.Join(base, "outside-catalog-k9m2"),
		keyPath:        filepath.Join(base, "outside-keys-h4v8", "signing-key-h4v8.pem"),
		pluginPath:     filepath.Join(base, "outside-plugins-p5t1", "importer-p5t1.wasm"),
		machineWide:    filepath.Join(base, "machine-wide-b6d9", "config.yaml"),
		machineCaches:  filepath.Join(base, "machine-caches-c3m8"),
		localRepos:     filepath.Join(base, "outside-repos-w3r5"),
		pristine:       t.TempDir(),
	}
	fx.outsideEntity = filepath.Join(fx.outsideCatalog, "components", "outside-widget-k9m2.yaml")
	fx.authoringRelease = filepath.Join(fx.root, "release.yaml")

	src := filepath.Join(moduleRoot(t), "internal", "compiler", "testdata", "fullstack-app")
	if err := os.CopyFS(fx.root, os.DirFS(src)); err != nil {
		t.Fatalf("copy the fixture workspace: %v", err)
	}
	for path, content := range map[string]string{
		fx.outsideEntity: "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n" +
			"  name: fixture/misc/outside-widget-k9m2\n  version: \"1.0.0\"\n  labels:\n" +
			"    component.aggrega.ai/type: resource\nspec:\n  schema:\n    values:\n" +
			"      name: { type: string, default: \"outside-widget\" }\n",
		fx.keyPath:     "not-a-key-h4v8\n",
		fx.pluginPath:  "not-a-module-p5t1\n",
		fx.machineWide: "registries: []\n",

		filepath.Join(fx.root, workspaceconfig.ConfigFilename):             fx.configuration(),
		filepath.Join(fx.root, "lint-subjects-q7x3", "bad-component.yaml"): lintParityBadComponent,
		filepath.Join(fx.root, "imports", "flow-q7x3.json"):                string(readImportFixture(t, langflowFixture)),
		filepath.Join(fx.root, "docs-bundle-q7x3.txt"):                     "a documentation bundle q7x3\n",
		filepath.Join(fx.root, "deprecations-q7x3.yaml"):                   "- reference: " + hostLocationRegistry + "/component/core/raw/manifest:1.0.0\n  severity: warning\n  reason: withdrawn\n",
		filepath.Join(fx.root, "plugins-q7x3", "not-a-module-q7x3.wasm"):   "not-a-module-q7x3\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("make the directory for %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	t.Setenv(workspaceconfig.EnvConfigOverride, fx.machineWide)
	// The environment overlay sits above the files, so a machine whose owner
	// exports a signing key would configure one into this workspace. An empty
	// value is what the overlay reads as unset.
	t.Setenv(signingKeyEnvVarName, "")
	fx.seedMachineWideCaches(t)

	// The lockfile is written by the tool that writes one, so the Release every
	// call below reads is one a caller could have resolved itself.
	pulled := callTool(t, fx.overTheProcessStreams(t), "pull", map[string]any{"release": "release.yaml"})
	if pulled.IsError {
		t.Fatalf("the fixture's Release did not resolve, so no call below reads a lockfile: %s", pulled.Content[0].Text)
	}
	fx.seedSourceArtifactSlots(t)
	if err := os.CopyFS(fx.pristine, os.DirFS(fx.root)); err != nil {
		t.Fatalf("keep the workspace every call starts from: %v", err)
	}
	return fx
}

// configuration is the workspace's own configuration document.
func (fx *hostLocationFixture) configuration() string {
	return "workspace:\n" +
		"  catalog_dirs:\n" +
		"    - catalog\n" +
		"    - " + fx.outsideCatalog + "\n" +
		"  releases:\n" +
		"    release.yaml:\n" +
		"      catalog_dirs_append:\n" +
		"        - " + fx.outsideCatalog + "\n" +
		"offline: true\n" +
		"default_registry: " + hostLocationRegistry + "\n" +
		"signing:\n" +
		"  key_path: " + fx.keyPath + "\n" +
		"build:\n" +
		"  authoring_release: " + fx.authoringRelease + "\n" +
		"plugins:\n" +
		"  importer:\n" +
		"    - uri: " + fx.pluginPath + "\n"
}

// hostLocationModuleDir is the directory the seeded compiled modules sit in
// beneath the compiled-module cache, in the shape the runtime names its own
// with: a version, then the architecture and system the code was compiled for.
const hostLocationModuleDir = "wazero-1.0-amd64-linux"

// hostLocationCachedModule is one compiled module the fixture's machine-wide
// cache holds, the guest digest it was compiled for, and every identity the
// provenance index records for that digest.
type hostLocationCachedModule struct {
	leaf       string
	digest     string
	identities []provindex.Identity
}

// cachedModules are the compiled modules the fixture's machine-wide cache holds:
// one built for a plugin loaded from a file, one for a registry reference, and
// one for bytes a file and a registry reference share. The first is what a
// caller reaching a listener must not be told, the second what it must be told
// whole, and the third is both at once.
func (fx *hostLocationFixture) cachedModules() []hostLocationCachedModule {
	digestOf := func(pair string) string { return "sha256:" + strings.Repeat(pair, 32) }
	fromFile, fromRegistry, shared := digestOf("a1"), digestOf("b2"), digestOf("c3")
	return []hostLocationCachedModule{
		{leaf: "from-file-q7x3", digest: fromFile, identities: []provindex.Identity{
			{URI: fx.pluginPath, Version: fromFile},
		}},
		{leaf: "from-registry-q7x3", digest: fromRegistry, identities: []provindex.Identity{
			{URI: "oci://" + hostLocationRegistry + "/plugin/importer/registry-q7x3", Version: "1.0.0"},
		}},
		{leaf: "shared-q7x3", digest: shared, identities: []provindex.Identity{
			{URI: fx.pluginPath, Version: shared},
			{URI: "oci://" + hostLocationRegistry + "/plugin/importer/shared-q7x3", Version: "2.0.0"},
		}},
	}
}

// cacheLocations is where every session of this fixture finds the caches: the
// workspace's own where the call resolved them, and the fixture's in place of
// the machine-wide ones this process owns.
func (fx *hostLocationFixture) cacheLocations(workspaceRoot, cacheDir string) cacheops.Locations {
	return cacheops.Locations{
		WorkspaceRoot:   workspaceRoot,
		CacheDir:        cacheDir,
		CompileDir:      filepath.Join(fx.machineCaches, "wasm-compile"),
		DescribeDir:     filepath.Join(fx.machineCaches, "plugin-describe"),
		ProvIndexPath:   filepath.Join(fx.machineCaches, "provenance-index.json"),
		AttribIndexPath: attribindex.DefaultIndexPath(fx.machineCaches),
	}
}

// seedMachineWideCaches writes each cached module where the compiled-module
// cache keeps one, attributes it to its guest digest, and records that digest's
// identities — each index through its own package's writer, so the inventory
// reads them as it reads what a real compile and a real resolution leave.
func (fx *hostLocationFixture) seedMachineWideCaches(t *testing.T) {
	t.Helper()
	loc := fx.cacheLocations("", "")
	moduleDir := filepath.Join(loc.CompileDir, hostLocationModuleDir)
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatalf("make the compiled-module directory: %v", err)
	}
	seen := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, module := range fx.cachedModules() {
		compiled := []byte("compiled " + module.leaf + "\n")
		if err := os.WriteFile(filepath.Join(moduleDir, module.leaf), compiled, 0o644); err != nil {
			t.Fatalf("write the compiled module %s: %v", module.leaf, err)
		}
		leafKey := hostLocationModuleDir + "/" + module.leaf
		if err := attribindex.RecordGuestLeaf(loc.AttribIndexPath, leafKey, int64(len(compiled)), module.digest); err != nil {
			t.Fatalf("attribute %s: %v", leafKey, err)
		}
		for _, identity := range module.identities {
			if err := provindex.Upsert(loc.ProvIndexPath, module.digest, identity, int64(len(compiled)), seen); err != nil {
				t.Fatalf("record %s@%s: %v", identity.URI, identity.Version, err)
			}
		}
	}
}

// hostLocationSourceSlot is one slot the workspace's source-artifact cache holds:
// the URL its repository was resolved from, the commit and archive it holds, and
// whether that URL names a repository read from this host's filesystem.
type hostLocationSourceSlot struct {
	url     string
	version string
	archive []byte
	onHost  bool
}

// digest is the content digest the slot records for its archive.
func (s hostLocationSourceSlot) digest() string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256(s.archive))
}

// digestHex is that digest as the inventory answers it.
func (s hostLocationSourceSlot) digestHex() string {
	return strings.TrimPrefix(s.digest(), "sha256:")
}

// sourceSlots are the slots the workspace's source-artifact cache holds: a
// repository read through a file URL, one read from a path written with no
// scheme, and one cloned over the network. The labels of the first two spell a
// place on this host, which a caller reaching a listener must not be told, and the
// third names only the host it was cloned from, which it must be told whole.
func (fx *hostLocationFixture) sourceSlots() []hostLocationSourceSlot {
	slot := func(url, commitPair string, onHost bool) hostLocationSourceSlot {
		return hostLocationSourceSlot{url: url, version: strings.Repeat(commitPair, 20),
			archive: []byte("archive of " + url + "\n"), onHost: onHost}
	}
	return []hostLocationSourceSlot{
		slot("file://"+filepath.Join(fx.localRepos, "chart-q7x3.git"), "5e", true),
		slot(filepath.Join(fx.localRepos, "plain-q7x3.git"), "6f", true),
		slot("https://git-z3q9.invalid/team/chart-q7x3.git", "7a", false),
	}
}

// sourceSlotDir is where the slot writer places a slot in the workspace's cache.
func (fx *hostLocationFixture) sourceSlotDir(t *testing.T, slot hostLocationSourceSlot) string {
	t.Helper()
	dir, err := cacheslot.Slot(fx.root, workspaceconfig.Config{}.ResolvedArtifactCacheDir(), gitrepo.Kind, slot.url,
		"", "", slot.version, slot.digest())
	if err != nil {
		t.Fatalf("place the slot for %s: %v", slot.url, err)
	}
	return dir
}

// sourceSlotLabel is the label the inventory lists a slot under: its location
// beneath the source-artifact cache, with each directory name read back into the
// value it was spelled from — the reader's own spelling of the host, path and
// version, rather than the escapes the names carry on disk. Every name of the
// slot's location was written by the cache's one spelling, so a name that does
// not read back fails here rather than being compared as it stands.
func (fx *hostLocationFixture) sourceSlotLabel(t *testing.T, slot hostLocationSourceSlot) string {
	t.Helper()
	rel, err := filepath.Rel(cacheslot.SourceArtifactsRoot(fx.root, workspaceconfig.Config{}.ResolvedArtifactCacheDir()),
		fx.sourceSlotDir(t, slot))
	if err != nil {
		t.Fatalf("relate the slot for %s to its cache: %v", slot.url, err)
	}
	names := strings.Split(filepath.ToSlash(rel), "/")
	for i, name := range names {
		value, ok := pathsegment.Decode(name)
		if !ok {
			t.Fatalf("the slot for %s carries the name %q, which the cache's spelling did not write", slot.url, name)
		}
		names[i] = value
	}
	return strings.Join(names, "/")
}

// seedSourceArtifactSlots writes each source slot where a resolution writes one,
// through the slot writer's own path builder and writer, so the inventory reads
// each slot's label and recorded URL as it reads what a real pull leaves.
func (fx *hostLocationFixture) seedSourceArtifactSlots(t *testing.T) {
	t.Helper()
	for _, slot := range fx.sourceSlots() {
		if err := cacheslot.WriteBlobAndMetadata("", "", fx.sourceSlotDir(t, slot), slot.archive, gitrepo.Kind, slot.url,
			slot.version, slot.digest(), gitrepo.MediatypeTarGz, "", nil); err != nil {
			t.Fatalf("write the slot for %s: %v", slot.url, err)
		}
	}
}

// sourceSlotWhere is the first seeded slot whose repository was read from this
// host, or the first that was not, as asked.
func (fx *hostLocationFixture) sourceSlotWhere(t *testing.T, onHost bool) hostLocationSourceSlot {
	t.Helper()
	for _, slot := range fx.sourceSlots() {
		if slot.onHost == onHost {
			return slot
		}
	}
	t.Fatalf("precondition: the fixture seeds no source slot with onHost=%v", onHost)
	return hostLocationSourceSlot{}
}

// reset puts the workspace back as every call finds it.
func (fx *hostLocationFixture) reset(t *testing.T) {
	t.Helper()
	if err := os.RemoveAll(fx.root); err != nil {
		t.Fatalf("clear the workspace: %v", err)
	}
	if err := os.CopyFS(fx.root, os.DirFS(fx.pristine)); err != nil {
		t.Fatalf("restore the workspace: %v", err)
	}
}

// bounds is what the comparison knows about where this fixture's places are.
func (fx *hostLocationFixture) bounds() hostLocationBounds {
	return hostLocationBounds{root: fx.root, base: fx.base}
}

// searched is every location the listener's frames are searched for as bytes:
// the root, each location the workspace is configured with, the directory holding
// the repositories its source-artifact cache was read from, and the directory
// holding them all, which is also where a climb out of the root lands.
func (fx *hostLocationFixture) searched() []string {
	return []string{fx.root, fx.outsideCatalog, fx.outsideEntity, fx.keyPath, fx.pluginPath, fx.machineWide,
		fx.authoringRelease, fx.localRepos, fx.base + string(filepath.Separator)}
}

// pluginListing is the enumeration the plugin reading is handed: a built-in, and
// a binding whose reference is the path outside the root the configuration
// names, failing with a text that names it too.
//
// The enumeration is supplied because the one the command layer performs is not
// reachable from this package; what is under test here is what the reading tells
// each caller about the enumeration it was handed.
func (fx *hostLocationFixture) pluginListing() pluginops.PluginListing {
	return pluginops.PluginListing{
		Embedded: []pluginops.ListedPlugin{{
			Source:         "embedded",
			URI:            "provider/helm",
			ResolvedDigest: "sha256:embedded-fixture",
			Descriptor: wasm.PluginDescriptor{
				PluginType:   string(wasm.PluginTypeProvider),
				Capabilities: []string{"helm"},
			},
		}},
		Configured: []pluginops.ListedPlugin{{
			Source: "configured",
			URI:    fx.pluginPath,
			Origin: "plugins.importer[0]",
			Err:    fmt.Errorf("open %s: not a WebAssembly module", fx.pluginPath),
		}},
	}
}

// generateCatalog stands in for catalog generation over the embedded guests,
// which has no overlays to read here, and runs the real generation for a single
// module, whose refusal is what the calls below read.
//
// The listing it writes names each file where the real generation names it: at
// its absolute path under the directory it was asked to write into.
func (fx *hostLocationFixture) generateCatalog(ctx context.Context, opts gencatalog.Options, out io.Writer) error {
	if !opts.FromEmbedded {
		return gencatalog.Run(ctx, opts, out)
	}
	_, err := fmt.Fprintf(out, "generated %s\n",
		filepath.Join(opts.OutputDir, "provider", "helm", "0.0.0-fixture", "plugin.yaml"))
	return err
}

// equip gives a session the seams the command layer composes it with, and the
// fixture's own machine-wide caches in place of this process's.
func (fx *hostLocationFixture) equip(s *server) {
	s.startDir = fx.root
	s.listPlugins = staticLister(fx.pluginListing())
	s.resolveImportPlugin = importToolResolver
	s.runGenCatalog = fx.generateCatalog
	s.cacheLocations = fx.cacheLocations
}

// overTheProcessStreams is the session a client that launched this process
// reaches. It is built the way the listener's session is built, so the two differ
// by transport alone.
func (fx *hostLocationFixture) overTheProcessStreams(t *testing.T) *server {
	t.Helper()
	s := newCommandToolServer(t, nil)
	// A session reached over a listener carries no command surface, so this one
	// carries none either: the capability index would otherwise differ between
	// the two by the command tools alone, which is not a location.
	s.commandTools = nil
	s.transport = transportStdio
	fx.equip(s)
	return s
}

// overAListener starts a real listener over the workspace and mints a credential
// admitting every tool it offers.
func (fx *hostLocationFixture) overAListener(t *testing.T) (*scopeGridFixture, string) {
	t.Helper()
	f := listenerOverWorkspace(t, fx.root)
	fx.equip(f.srv)
	return f, f.grantOf(t, scopeRead, scopeWorkspaceWrite, scopeRegistryWrite)
}

// hostLocationAnswer is one transport's answer to one call, decoded as far as the
// comparison reads it.
type hostLocationAnswer struct {
	// protocolError is the message of an error envelope, empty for a tool result.
	protocolError string
	isError       bool
	texts         []string

	// structured is the structured payload decoded without a type, so the walk
	// reads every key the answer carries rather than the keys a type declares. It
	// is nil where the answer carries none; structuredBytes is the same payload
	// as it arrived.
	structured      any
	structuredBytes json.RawMessage

	// raw is the whole frame as it crossed the wire, for the byte search.
	raw []byte
}

// decodedToolResult reads a tool result's JSON into an answer.
func decodedToolResult(t *testing.T, what string, result []byte) hostLocationAnswer {
	t.Helper()
	var decoded struct {
		Content           []toolTextContent `json:"content"`
		StructuredContent json.RawMessage   `json:"structuredContent"`
		IsError           bool              `json:"isError"`
	}
	if err := json.Unmarshal(result, &decoded); err != nil {
		t.Fatalf("%s: the result is not a tool result: %v (%s)", what, err, result)
	}
	out := hostLocationAnswer{isError: decoded.IsError, structuredBytes: decoded.StructuredContent}
	for _, block := range decoded.Content {
		out.texts = append(out.texts, block.Text)
	}
	if len(decoded.StructuredContent) > 0 {
		if err := json.Unmarshal(decoded.StructuredContent, &out.structured); err != nil {
			t.Fatalf("%s: the structured payload does not decode: %v (%s)", what, err, decoded.StructuredContent)
		}
	}
	return out
}

// answerOverTheProcessStreams sends one call through the dispatch a client that
// launched this process reaches, and reads the result as that client receives it.
func answerOverTheProcessStreams(t *testing.T, s *server, tool string, args map[string]any) hostLocationAnswer {
	t.Helper()
	params, err := json.Marshal(map[string]any{"name": tool, "arguments": args})
	if err != nil {
		t.Fatalf("marshal the call: %v", err)
	}
	res, rpcErr := s.handleToolsCall(context.Background(), params)
	if rpcErr != nil {
		return hostLocationAnswer{protocolError: rpcErr.Message}
	}
	result, ok := res.(*callToolResult)
	if !ok {
		t.Fatalf("tools/call %s returned %T, want a tool result", tool, res)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("encode the result of %s: %v", tool, err)
	}
	out := decodedToolResult(t, tool, encoded)
	out.raw = encoded
	return out
}

// answerOverTheListener sends one call over a real listener, and keeps the whole
// frame it was answered with.
func answerOverTheListener(t *testing.T, f *scopeGridFixture, credential, tool string,
	args map[string]any) hostLocationAnswer {

	t.Helper()
	got := f.callTool(t, credential, tool, args)
	if got.frame.Error != nil {
		return hostLocationAnswer{protocolError: got.frame.Error.Message, raw: []byte(got.raw)}
	}
	if got.status != http.StatusOK {
		t.Fatalf("%s was answered %d over the listener (body %q)", tool, got.status, got.raw)
	}
	out := decodedToolResult(t, tool, got.frame.Result)
	out.raw = []byte(got.raw)
	return out
}

// hostLocationBounds is what the comparison knows about where the fixture's
// places are, and nothing it learned from the package under test.
type hostLocationBounds struct {
	root string
	base string
}

// namesAHostLocation reports whether v names a place on this host: it carries
// the directory every fixture location sits under, or it is, as a whole, an
// absolute path on this host.
func (b hostLocationBounds) namesAHostLocation(v string) bool {
	return v == b.base || strings.Contains(v, b.base+string(filepath.Separator)) || isAHostPath(v)
}

// isAHostPath reports whether v is, as a whole, an absolute path on this host:
// absolute, and beginning with a directory this host's root holds.
//
// The second half keeps a slash-led value that is not a path — a pointer into a
// document, say — from reading as one.
func isAHostPath(v string) bool {
	if !filepath.IsAbs(v) {
		return false
	}
	sep := string(filepath.Separator)
	first, _, _ := strings.Cut(strings.TrimPrefix(v, sep), sep)
	if first == "" {
		return false
	}
	info, err := os.Stat(sep + first)
	return err == nil && info.IsDir()
}

// toldAs is what a caller reaching a listener may be told about loc: its name
// relative to the workspace root where it lies inside the workspace, and nothing
// where it is the root or lies outside it.
//
// 🔴 DELIBERATE DUPLICATE. It reads containment over the known root itself rather
// than asking the function the server answers with, because a comparison whose
// expectation called the rule under test would move with that rule and see
// nothing.
func (b hostLocationBounds) toldAs(loc string) (string, bool) {
	if !filepath.IsAbs(loc) {
		return "", false
	}
	rel, err := filepath.Rel(b.root, loc)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// proseToldAs is what a caller reaching a listener may be told of text naming
// the fixture's places: each one named relative to the workspace root where it
// lies inside the workspace, and read as the kept-back phrase where it does not.
//
// A place starts where the fixture's directory begins at the start of the text or
// after a character that cannot continue a path as prose writes one, and runs to
// the next such character, less the full stops closing a sentence it ends. That
// is the extent the reference documentation's promise is about, restated rather
// than read from the package for the reason toldAs is.
func (b hostLocationBounds) proseToldAs(text string) string {
	var out strings.Builder
	copied := 0
	for from := 0; from < len(text); {
		i := strings.Index(text[from:], b.base)
		if i < 0 {
			break
		}
		start := from + i
		if start > 0 {
			if r, _ := utf8.DecodeLastRuneInString(text[:start]); !delimitsAProseLocation(r) {
				from = start + 1
				continue
			}
		}
		end := start
		for end < len(text) {
			r, size := utf8.DecodeRuneInString(text[end:])
			if delimitsAProseLocation(r) {
				break
			}
			end += size
		}
		for end > start+1 && text[end-1] == '.' {
			end--
		}
		words, told := b.toldAs(text[start:end])
		if !told {
			words = hostLocationKeptBackWords
		}
		out.WriteString(text[copied:start])
		out.WriteString(words)
		copied, from = end, end
	}
	out.WriteString(text[copied:])
	return out.String()
}

// delimitsAProseLocation reports whether r is a character prose writes around a
// place rather than inside one.
func delimitsAProseLocation(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune("\"'`,;:()[]{}<>=|", r)
}

// hostLocationWalk reads two structured answers to one call together.
type hostLocationWalk struct {
	b          hostLocationBounds
	complaints []string

	// located counts the values in the keyboard's answer naming a place on this
	// host, so a comparison that judged some can be told from one that had
	// nothing to judge.
	located int
}

func (w *hostLocationWalk) say(format string, args ...any) {
	w.complaints = append(w.complaints, fmt.Sprintf(format, args...))
}

// hostLocationComplaints reads the listener's answer against the keyboard's for
// one call, and returns one complaint per difference that is not a location told
// as a caller reaching a listener may be told it, one per location that did not
// differ, and how many locations the keyboard's answer named.
//
// It returns complaints rather than failing, so a deliberately damaged answer can
// be driven through it and the comparison watched rejecting something.
func hostLocationComplaints(b hostLocationBounds, atTheKeyboard, overTheListener hostLocationAnswer) ([]string, int) {
	w := &hostLocationWalk{b: b}
	if atTheKeyboard.protocolError != overTheListener.protocolError {
		w.say("the call was answered with the error envelope %q at the keyboard and %q over the listener",
			atTheKeyboard.protocolError, overTheListener.protocolError)
	}
	if atTheKeyboard.isError != overTheListener.isError {
		w.say("the call was refused on one transport and not on the other (refused at the keyboard: %v, over "+
			"the listener: %v)", atTheKeyboard.isError, overTheListener.isError)
	}
	switch {
	case atTheKeyboard.structured == nil && overTheListener.structured == nil:
	case atTheKeyboard.structured == nil || overTheListener.structured == nil:
		w.say("one transport answered with a structured payload and the other without one (keyboard %s, "+
			"listener %s)", shapeOf(atTheKeyboard.structured), shapeOf(overTheListener.structured))
		w.countIn(atTheKeyboard.structured)
	default:
		w.value("structuredContent", atTheKeyboard.structured, overTheListener.structured)
	}
	for _, text := range atTheKeyboard.texts {
		if b.namesAHostLocation(text) {
			w.located++
		}
	}
	return w.complaints, w.located
}

// value reads one value of each answer at the same place.
func (w *hostLocationWalk) value(at string, keyboard, listener any) {
	switch k := keyboard.(type) {
	case map[string]any:
		l, ok := listener.(map[string]any)
		if !ok {
			w.say("%s is an object at the keyboard and %s over the listener", at, shapeOf(listener))
			w.countIn(k)
			return
		}
		w.object(at, k, l)
	case []any:
		l, ok := listener.([]any)
		if !ok || len(l) != len(k) {
			w.say("%s is %s at the keyboard and %s over the listener, so an entry was added or dropped",
				at, shapeOf(keyboard), shapeOf(listener))
			w.countIn(k)
			return
		}
		for i := range k {
			w.value(fmt.Sprintf("%s[%d]", at, i), k[i], l[i])
		}
	case string:
		w.text(at, k, listener)
	default:
		if !reflect.DeepEqual(keyboard, listener) {
			w.say("%s is %s at the keyboard and %s over the listener, and a value that is not text names no place",
				at, shapeOf(keyboard), shapeOf(listener))
		}
	}
}

// object reads the keys of two objects at the same place, in a fixed order.
func (w *hostLocationWalk) object(at string, keyboard, listener map[string]any) {
	for _, key := range unionOfKeys(keyboard, listener) {
		here := at + "." + key
		kv, atKeyboard := keyboard[key]
		lv, overListener := listener[key]
		if isAKeptBackMark(key) {
			w.mark(here, key, kv, atKeyboard, lv, overListener, keyboard)
			continue
		}
		marked := marksKeptBack(listener, key)
		switch {
		case atKeyboard && !overListener:
			if !marked {
				w.say("%s is answered at the keyboard and absent over the listener, with no mark saying it was "+
					"kept back", here)
				w.countIn(kv)
				continue
			}
			w.keptBack(here, kv)
		case !atKeyboard && overListener:
			w.say("%s is answered over the listener only, as %s, and it is not a mark saying something was kept "+
				"back", here, shapeOf(lv))
		case marked:
			if entries, isList := kv.([]any); isList && lv != nil {
				w.keptBackEntries(here, entries, lv)
				continue
			}
			if lv != nil && lv != "" {
				w.say("%s is marked as kept back over the listener and still carries %s", here, shapeOf(lv))
			}
			w.keptBack(here, kv)
		default:
			w.value(here, kv, lv)
		}
	}
}

// keptBack judges a value the listener's answer marks as kept back: the keyboard's
// value must be one that names a place on this host, or the listener was kept
// from something it could have been told.
func (w *hostLocationWalk) keptBack(at string, keyboard any) {
	s, isText := keyboard.(string)
	switch {
	case !isText:
		w.say("%s is kept back from the listener, and at the keyboard it is %s rather than a value naming a place",
			at, shapeOf(keyboard))
	case !w.b.namesAHostLocation(s):
		w.say("%s is kept back from the listener, and the keyboard's %q names no place on this host", at, s)
	default:
		w.located++
	}
}

// keptBackEntries judges a list the listener's answer marks as keeping some of
// its entries back. Every entry keeps its place, so the list is as long as the
// keyboard's; an entry kept back is null there, and the keyboard's entry in that
// place must name a place on this host; every other entry is read as any value
// is, so one carried through unchanged while it names a place is still caught. A
// mark over a list that keeps nothing back marks nothing.
func (w *hostLocationWalk) keptBackEntries(at string, keyboard []any, listener any) {
	l, ok := listener.([]any)
	if !ok || len(l) != len(keyboard) {
		w.say("%s is %s at the keyboard and %s over the listener, so an entry was added or dropped",
			at, shapeOf(keyboard), shapeOf(listener))
		w.countIn(keyboard)
		return
	}
	keptBack := 0
	for i := range keyboard {
		here := fmt.Sprintf("%s[%d]", at, i)
		if l[i] == nil {
			keptBack++
			w.keptBack(here, keyboard[i])
			continue
		}
		w.value(here, keyboard[i], l[i])
	}
	if keptBack == 0 {
		w.say("%s is marked as kept back over the listener and still carries every entry", at)
	}
}

// mark judges a mark saying a value was kept back. The keyboard's answer keeps
// nothing back, and the listener's may mark only a value the keyboard's carries.
func (w *hostLocationWalk) mark(at, key string, keyboard any, atKeyboard bool, listener any, overListener bool,
	keyboardSide map[string]any) {

	if atKeyboard && keyboard != false {
		w.say("%s is %s at the keyboard, and a caller that launched this process is told every location",
			at, shapeOf(keyboard))
	}
	if !overListener || listener == false {
		return
	}
	if listener != true {
		w.say("%s is %s over the listener, and a mark is true or absent", at, shapeOf(listener))
		return
	}
	if key == hostLocationsWithheldKey {
		return
	}
	if marked := markedKey(key); keyboardSide[marked] == nil {
		w.say("%s marks %q as kept back, and the keyboard's answer carries no %q to keep back", at, marked, marked)
	}
}

// text reads one text value of each answer at the same place.
func (w *hostLocationWalk) text(at, keyboard string, listener any) {
	names := w.b.namesAHostLocation(keyboard)
	if names {
		w.located++
	}
	heard, isText := listener.(string)
	switch {
	case !isText:
		w.say("%s is the text %q at the keyboard and %s over the listener, with no mark saying it was kept back",
			at, keyboard, shapeOf(listener))
		return
	case heard == keyboard:
		if names {
			w.say("%s carries %q to the listener unchanged, and it names a place on this host", at, keyboard)
		}
		return
	case !names:
		w.say("%s is %q at the keyboard and %q over the listener, and the keyboard's value names no place on this "+
			"host, so the difference is not a location", at, keyboard, heard)
		return
	case filepath.IsAbs(keyboard) && strings.IndexFunc(keyboard, delimitsAProseLocation) < 0:
		want, ok := w.b.toldAs(keyboard)
		switch {
		case !ok:
			w.say("%s is %q at the keyboard, which is the workspace root or lies outside the workspace, and it "+
				"reached the listener as %q rather than being kept back and marked", at, keyboard, heard)
		case heard != want:
			w.say("%s is %q at the keyboard and %q over the listener, want %q: its name relative to the workspace "+
				"root", at, keyboard, heard, want)
		}
	default:
		if want := w.b.proseToldAs(keyboard); heard != want {
			w.say("%s reads %q at the keyboard and %q over the listener, want %q: each place named relative to "+
				"the workspace root, or %s", at, keyboard, heard, want, hostLocationKeptBackWords)
		}
	}
	if w.b.namesAHostLocation(heard) {
		w.say("%s still names a place on this host over the listener: %q", at, heard)
	}
}

// countIn counts the values naming a place on this host anywhere in v, for a
// value the walk could not pair with the listener's.
func (w *hostLocationWalk) countIn(v any) {
	switch x := v.(type) {
	case map[string]any:
		for _, e := range x {
			w.countIn(e)
		}
	case []any:
		for _, e := range x {
			w.countIn(e)
		}
	case string:
		if w.b.namesAHostLocation(x) {
			w.located++
		}
	}
}

// isAKeptBackMark reports whether key is a mark saying a value was kept back.
// Every such mark is its value's key followed by "_withheld", except the mark on
// a configuration layer's name, which is "withheld" alone.
func isAKeptBackMark(key string) bool {
	return key == "withheld" || strings.HasSuffix(key, "_withheld")
}

// markedKey is the key a mark says was kept back.
func markedKey(mark string) string {
	if mark == "withheld" {
		return "name"
	}
	return strings.TrimSuffix(mark, "_withheld")
}

// marksKeptBack reports whether side marks key's value as kept back.
func marksKeptBack(side map[string]any, key string) bool {
	if side[key+"_withheld"] == true {
		return true
	}
	return key == "name" && side["withheld"] == true
}

// unionOfKeys is every key either object carries, sorted, so complaints come out
// in the same order on every run.
func unionOfKeys(a, b map[string]any) []string {
	keys := make([]string, 0, len(a)+len(b))
	for k := range a {
		keys = append(keys, k)
	}
	for k := range b {
		if _, both := a[k]; !both {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// shapeOf renders a decoded value for a complaint.
func shapeOf(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "an object"
	case []any:
		return fmt.Sprintf("a list of %d", len(x))
	case string:
		return fmt.Sprintf("the text %q", x)
	default:
		return fmt.Sprintf("%v", x)
	}
}

// hostLocationsInBytes returns one complaint per location found anywhere in a
// frame's bytes, quoting the bytes around it.
func hostLocationsInBytes(frame []byte, locations []string) []string {
	var out []string
	for _, loc := range locations {
		i := bytes.Index(frame, []byte(loc))
		if i < 0 {
			continue
		}
		from, to := max(0, i-80), min(len(frame), i+len(loc)+80)
		out = append(out, fmt.Sprintf("the listener's frame carries %q: …%s…", loc, frame[from:to]))
	}
	return out
}

// hostLocationCall is one call a tool is asked on both transports.
type hostLocationCall struct {
	scenario string
	args     map[string]any

	// bytesOnly says why this call's two answers are searched as bytes and not
	// walked together. It is empty for every call whose two answers must agree
	// everywhere but at a location.
	bytesOnly string
}

// hostLocationTool is one tool and the calls it is asked.
type hostLocationTool struct {
	tool string

	// namesALocation says some call below answers with a place on this host at
	// the keyboard, so a comparison that saw none has measured nothing for it.
	namesALocation bool

	// prepare installs what the tool needs before its calls, where it needs more
	// than the session's own seams.
	prepare func(t *testing.T)

	calls []hostLocationCall
}

// hostLocationEveryTool is every tool this server offers a caller reaching a
// listener, with a call that succeeds and, where one exists, calls that are
// refused, each naming its paths relative to the workspace root so that it is a
// call either transport admits.
func hostLocationEveryTool() []hostLocationTool {
	const (
		release      = "release.yaml"
		entity       = "catalog/base/components/raw-manifest.yaml"
		recipe       = "catalog/recipes/helm-sourced-with-workload/api-server.yaml"
		unpinned     = "catalog/recipes/helm-synthesized-with-artifacts/api-server.yaml"
		lintSubject  = "lint-subjects-q7x3/bad-component.yaml"
		dangling     = "release-dangling-component-mapping.yaml"
		missing      = "missing-release-q7x3.yaml"
		inRootModule = "plugins-q7x3/not-a-module-q7x3.wasm"
		component    = "core/compute/api-server"
		outsideName  = "fixture/misc/outside-widget-k9m2"
		reference    = hostLocationRegistry + "/component/core/raw/manifest:1.0.0"
	)
	detailed := func(extra map[string]any) map[string]any {
		out := map[string]any{"response_format": "detailed", "limit": 500}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	const byCredential = "a signing call spends the caller's own credential over a listener and the workspace's " +
		"configured key at the keyboard, so the two answers differ by which credential was read"
	const byDefinition = "the answer carries each tool's advertised definition, which the two transports advertise " +
		"differently by design"

	return []hostLocationTool{
		{tool: "list-entities", namesALocation: true, calls: []hostLocationCall{
			{scenario: "the summary", args: map[string]any{}},
			{scenario: "the records", args: detailed(nil)},
			{scenario: "an unknown response format is refused", args: map[string]any{"response_format": "bogus-q7x3"}},
		}},
		{tool: "list-releases", namesALocation: true, calls: []hostLocationCall{
			{scenario: "the summary", args: map[string]any{}},
			{scenario: "the paths", args: detailed(nil)},
			{scenario: "an unknown response format is refused", args: map[string]any{"response_format": "bogus-q7x3"}},
		}},
		{tool: "cache-status", namesALocation: true, calls: []hostLocationCall{
			{scenario: "the caches", args: map[string]any{}},
		}},
		{tool: "registry-list", namesALocation: true, calls: []hostLocationCall{
			{scenario: "the registries", args: map[string]any{}},
		}},
		{tool: "search", namesALocation: true, calls: []hostLocationCall{
			{scenario: "a query with no index to search", args: map[string]any{"query": "api"}},
			{scenario: "an unknown registry is refused", args: map[string]any{"query": "api", "registry": "no-such-registry-q7x3"}},
		}},
		{tool: "config-show", namesALocation: true, calls: []hostLocationCall{
			{scenario: "the configuration in force", args: map[string]any{}},
		}},
		{tool: "plugin-list", namesALocation: true, calls: []hostLocationCall{
			{scenario: "the summary", args: map[string]any{}},
			{scenario: "the records", args: detailed(nil)},
			{scenario: "an unknown response format is refused", args: map[string]any{"response_format": "bogus-q7x3"}},
		}},
		{tool: "validate", namesALocation: true, calls: []hostLocationCall{
			{scenario: "a Release", args: map[string]any{"release": release}},
			{scenario: "an entity", args: map[string]any{"entity": entity}},
			{scenario: "the whole catalog", args: map[string]any{"catalog": true}},
			{scenario: "a file inside the root that is not a module", args: map[string]any{"plugin": inRootModule, "type": "importer"}},
			{scenario: "a Release whose mapping dangles", args: map[string]any{"release": dangling}},
			{scenario: "a missing Release is refused", args: map[string]any{"release": missing}},
			{scenario: "no scope is refused", args: map[string]any{}},
			{scenario: "two scopes are refused", args: map[string]any{"release": release, "entity": entity}},
		}},
		{tool: "describe", namesALocation: true, calls: []hostLocationCall{
			{scenario: "an entity inside the root", args: map[string]any{"name": component}},
			{scenario: "an entity from the catalog directory outside the root", args: map[string]any{"name": outsideName}},
			{scenario: "a Release", args: map[string]any{"release": release}},
			{scenario: "a Release in detail", args: map[string]any{"release": release, "response_format": "detailed"}},
			{scenario: "an entity nobody declares is refused", args: map[string]any{"name": "fixture/misc/no-such-entity-q7x3"}},
			{scenario: "a registry reference is refused offline", args: map[string]any{"name": "oci://" + reference}},
			{scenario: "no subject is refused", args: map[string]any{}},
		}},
		{tool: "lint", namesALocation: true, calls: []hostLocationCall{
			{scenario: "an entity breaking a convention", args: map[string]any{"entity": lintSubject}},
			{scenario: "an entity keeping the conventions", args: map[string]any{"entity": entity}},
			{scenario: "a Release", args: map[string]any{"release": release}},
			{scenario: "a missing entity is refused", args: map[string]any{"entity": "catalog/missing-q7x3.yaml"}},
			{scenario: "a pre-publication check is refused offline", args: map[string]any{"entity": entity, "pre_publish": true}},
			{scenario: "no subject is refused", args: map[string]any{}},
		}},
		{tool: "attach-candidates", namesALocation: true, calls: []hostLocationCall{
			{scenario: "a catalog node", args: map[string]any{"node": component}},
			{scenario: "a node on a Release's graph", args: map[string]any{"graph_node": "backend", "release": release}},
			{scenario: "a label set", args: map[string]any{"labels": map[string]any{}}},
			{scenario: "a node nobody declares is refused", args: map[string]any{"node": "fixture/misc/no-such-q7x3"}},
			{scenario: "a graph node without its Release is refused", args: map[string]any{"graph_node": "backend"}},
		}},
		{tool: "connection-candidates", namesALocation: true, calls: []hostLocationCall{
			{scenario: "a catalog node", args: map[string]any{"node": component}},
			{scenario: "a node nobody declares is refused", args: map[string]any{"node": "fixture/misc/no-such-q7x3"}},
			{scenario: "no node is refused", args: map[string]any{}},
		}},
		{tool: "insertion-point", namesALocation: true, calls: []hostLocationCall{
			{scenario: "a container the document holds", args: map[string]any{"file": "aggregate.yaml", "container": []string{"spec", "components"}}},
			{scenario: "a container the document lacks", args: map[string]any{"file": "aggregate.yaml", "container": []string{"spec", "no-such-q7x3"}}},
			{scenario: "a missing document is refused", args: map[string]any{"file": "missing-q7x3.yaml", "container": []string{"spec"}}},
			{scenario: "no document is refused", args: map[string]any{}},
		}},
		{tool: "attachment-container", namesALocation: true, calls: []hostLocationCall{
			{scenario: "a node on a Release's graph", args: map[string]any{"graph_node": "frontend", "release": release}},
			{scenario: "a node the graph lacks is refused", args: map[string]any{"graph_node": "no-such-q7x3", "release": release}},
			{scenario: "no node is refused", args: map[string]any{}},
		}},
		{tool: connectionAtPositionToolName, namesALocation: true, calls: []hostLocationCall{
			{scenario: "a place in the Aggregate", args: map[string]any{"file": "aggregate.yaml",
				"position": map[string]any{"line": 0, "character": 0}, "release": release}},
			{scenario: "a place in the Release", args: map[string]any{"file": release,
				"position": map[string]any{"line": 0, "character": 0}, "release": release}},
			{scenario: "a missing document is refused", args: map[string]any{"file": "missing-q7x3.yaml",
				"position": map[string]any{"line": 0, "character": 0}, "release": release}},
			{scenario: "no document is refused", args: map[string]any{}},
		}},
		{tool: "build", namesALocation: true, calls: []hostLocationCall{
			{scenario: "a Release", args: map[string]any{"release": release, "response_format": "concise"}},
			{scenario: "a Release in detail", args: map[string]any{"release": release, "response_format": "detailed"}},
			{scenario: "a Release resolving its dependencies first", args: map[string]any{"release": release, "response_format": "concise", "auto_pull": true}},
			{scenario: "a Release whose mapping dangles", args: map[string]any{"release": dangling}},
			{scenario: "a missing Release is refused", args: map[string]any{"release": missing}},
			{scenario: "no Release is refused", args: map[string]any{}},
		}},
		{tool: "pull", namesALocation: true, calls: []hostLocationCall{
			{scenario: "a comparison against the lockfile", args: map[string]any{"release": release, "verify": true}},
			{scenario: "a resolution that writes the lockfile", args: map[string]any{"release": release}},
			{scenario: "a Release whose mapping dangles", args: map[string]any{"release": dangling}},
			{scenario: "a missing Release is refused", args: map[string]any{"release": missing}},
			{scenario: "an upgrade is refused offline", args: map[string]any{"release": release, "upgrade_targets": []string{component}}},
			{scenario: "comparing and upgrading together is refused", args: map[string]any{"release": release, "verify": true, "upgrade_targets": []string{component}}},
			{scenario: "comparing and pinning together is refused", args: map[string]any{"release": release, "verify": true, "pin_sources": true}},
		}},
		{tool: "pin", namesALocation: true, calls: []hostLocationCall{
			{scenario: "a Recipe, without writing", args: map[string]any{"entity": recipe, "dry_run": true}},
			{scenario: "a Recipe", args: map[string]any{"entity": recipe}},
			{scenario: "an entity that reads no input a lock records", args: map[string]any{"entity": "aggregate.yaml"}},
			{scenario: "a missing entity is refused", args: map[string]any{"entity": "catalog/missing-q7x3.yaml"}},
			{scenario: "no entity is refused", args: map[string]any{}},
		}},
		{tool: "edit-document", namesALocation: true, calls: []hostLocationCall{
			{scenario: "an edit, without writing", args: map[string]any{"edit": "set-value", "file": "aggregate.yaml",
				"node": []string{"spec", "components", "backend"}, "name": "fixture_marker", "value": "q7x3", "dry_run": true}},
			{scenario: "an edit", args: map[string]any{"edit": "set-value", "file": "aggregate.yaml",
				"node": []string{"spec", "components", "backend"}, "name": "fixture_marker", "value": "q7x3"}},
			{scenario: "a missing document is refused", args: map[string]any{"edit": "set-value", "file": "missing-q7x3.yaml",
				"node": []string{"spec"}, "name": "x", "value": "y"}},
			{scenario: "an edge with no socket is refused", args: map[string]any{"edit": "wire-edge", "file": "aggregate.yaml",
				"node": []string{"spec", "components", "backend"}, "name": "x", "value": "frontend"}},
			{scenario: "no edit is refused", args: map[string]any{}},
		}},
		{tool: "gen-catalog", namesALocation: true, calls: []hostLocationCall{
			{scenario: "every embedded guest", args: map[string]any{"from_embedded": true, "output_dir": "gen-out-q7x3", "overlay": "overlays-q7x3"}},
			{scenario: "a missing module is refused", args: map[string]any{"plugin": "missing-q7x3.wasm", "output_dir": "gen-out-q7x3",
				"overlay": "overlays-q7x3/provider-helm-overlay-q7x3.yaml"}},
			{scenario: "no selection is refused", args: map[string]any{}},
		}},
		{tool: "publish-entity", namesALocation: true, calls: []hostLocationCall{
			{scenario: "an entity sharing its directory, without publishing", args: map[string]any{"entity": entity, "dry_run": true}},
			{scenario: "a Recipe whose lock records no pins, without publishing", args: map[string]any{"entity": unpinned, "dry_run": true}},
			{scenario: "a publication is refused offline", args: map[string]any{"entity": entity}},
			{scenario: "no entity is refused", args: map[string]any{}},
		}},
		{tool: "publish-artifact", namesALocation: true, calls: []hostLocationCall{
			{scenario: "an artifact, without publishing", args: map[string]any{"file": "docs-bundle-q7x3.txt", "name": "fixture-doc", "tag": "0.0.1", "dry_run": true}},
			{scenario: "a publication is refused offline", args: map[string]any{"file": "docs-bundle-q7x3.txt", "name": "fixture-doc", "tag": "0.0.1"}},
			{scenario: "a missing file is refused", args: map[string]any{"file": "missing-q7x3.txt", "name": "fixture-doc", "tag": "0.0.1", "dry_run": true}},
			{scenario: "no file is refused", args: map[string]any{}},
		}},
		{tool: "import", namesALocation: true, prepare: pointImportSeamAtTheDemoGuest, calls: []hostLocationCall{
			{scenario: "a source, without writing", args: map[string]any{"source": "imports/flow-q7x3.json", "dry_run": true}},
			{scenario: "a source", args: map[string]any{"source": "imports/flow-q7x3.json"}},
			{scenario: "a missing source is refused", args: map[string]any{"source": "imports/missing-q7x3.json"}},
			{scenario: "no source is refused", args: map[string]any{}},
		}},
		{tool: "get-schema", calls: []hostLocationCall{
			{scenario: "a kind", args: map[string]any{"kind": "component"}},
			{scenario: "the kinds", args: map[string]any{"list": true}},
			{scenario: "an unknown kind is refused", args: map[string]any{"kind": "bogus-q7x3"}},
		}},
		{tool: "explain-error", calls: []hostLocationCall{
			{scenario: "a message about a kind", args: map[string]any{"message": "unknown field 'bogus' in spec", "kind": "release"}},
			{scenario: "no message is refused", args: map[string]any{}},
		}},
		{tool: "scaffold", calls: []hostLocationCall{
			{scenario: "a kind", args: map[string]any{"kind": "component", "name": "fixture/misc/scaffolded-q7x3"}},
			{scenario: "an unknown kind is refused", args: map[string]any{"kind": "bogus-q7x3"}},
		}},
		{tool: "describe-capabilities", calls: []hostLocationCall{
			{scenario: "the index", args: map[string]any{}, bytesOnly: byDefinition},
			{scenario: "a kind", args: map[string]any{"kind": "release"}, bytesOnly: byDefinition},
			{scenario: "named tools", args: map[string]any{"tools": []string{"pull", "list-releases", "import"}}, bytesOnly: byDefinition},
		}},
		{tool: "sign-artifacts", calls: []hostLocationCall{
			{scenario: "a reference", args: map[string]any{"references": []string{reference}}, bytesOnly: byCredential},
			{scenario: "no reference is refused", args: map[string]any{}, bytesOnly: byCredential},
		}},
		{tool: "deprecate-artifact", calls: []hostLocationCall{
			{scenario: "a reference", args: map[string]any{"reference": reference, "severity": "warning", "reason": "withdrawn"}, bytesOnly: byCredential},
			{scenario: "a batch file", args: map[string]any{"from": "deprecations-q7x3.yaml"}, bytesOnly: byCredential},
			{scenario: "no marking is refused", args: map[string]any{}, bytesOnly: byCredential},
		}},
	}
}

// TestEveryToolAnswersAListenerWithoutNamingAPlaceOnTheHost drives every tool
// over both transports against one workspace, and reads each pair of answers
// together and the listener's frame as bytes.
func TestEveryToolAnswersAListenerWithoutNamingAPlaceOnTheHost(t *testing.T) {
	fx := newHostLocationFixture(t)
	streams := fx.overTheProcessStreams(t)
	listener, credential := fx.overAListener(t)
	bounds := fx.bounds()

	offered := append(listener.listed(t, credential), listener.surfaced(t, credential)...)
	var driven []string
	for _, group := range hostLocationEveryTool() {
		driven = append(driven, group.tool)
	}
	for _, name := range offered {
		if !slices.Contains(driven, name) {
			t.Errorf("the listener offers %q and nothing here asks it anything, so what it tells a caller about "+
				"this host is judged by nothing", name)
		}
	}

	for _, group := range hostLocationEveryTool() {
		t.Run(group.tool, func(t *testing.T) {
			if group.prepare != nil {
				group.prepare(t)
			}
			located := 0
			for _, call := range group.calls {
				t.Run(call.scenario, func(t *testing.T) {
					fx.reset(t)
					atTheKeyboard := answerOverTheProcessStreams(t, streams, group.tool, call.args)
					fx.reset(t)
					overTheListener := answerOverTheListener(t, listener, credential, group.tool, call.args)

					for _, complaint := range hostLocationsInBytes(overTheListener.raw, fx.searched()) {
						t.Errorf("%s, %s: %s", group.tool, call.scenario, complaint)
					}
					if call.bytesOnly != "" {
						return
					}
					complaints, n := hostLocationComplaints(bounds, atTheKeyboard, overTheListener)
					located += n
					for _, complaint := range complaints {
						t.Errorf("%s, %s: %s", group.tool, call.scenario, complaint)
					}
				})
			}
			if group.namesALocation && located == 0 {
				t.Errorf("no answer %s gave at the keyboard named a place on this host, so the comparison measured "+
					"nothing for it: the fixture no longer reaches the answers that name one", group.tool)
			}
			t.Logf("%s: %d value(s) naming a place on this host at the keyboard were judged", group.tool, located)
		})
	}
}

// hostLocationPathArgument is one argument naming a path, on a tool that takes
// one, with the rest of a call that argument is valid in.
type hostLocationPathArgument struct {
	tool, argument string
	args           func(path string) map[string]any
}

// TestAListenerRefusesAPathOutsideTheWorkspaceWithoutNamingAPlaceOnTheHost sends
// every path-naming tool a path the listener refuses — a climb out of the root, a
// climb back into it, an absolute path inside it and one outside it — and reads
// each refusal for what it tells the caller.
//
// These are listener calls alone. At the keyboard the same calls are admitted and
// answered, so there is no pair to compare; what a refusal must not do is name
// the root or the place a value resolves to.
func TestAListenerRefusesAPathOutsideTheWorkspaceWithoutNamingAPlaceOnTheHost(t *testing.T) {
	fx := newHostLocationFixture(t)
	listener, credential := fx.overAListener(t)
	bounds := fx.bounds()

	insideName, inside := bounds.toldAs(fx.authoringRelease)
	if !inside {
		t.Fatalf("precondition: %s does not lie inside %s", fx.authoringRelease, fx.root)
	}
	climbBackIn := filepath.Join("..", hostLocationRootName, "release.yaml")

	for _, arg := range []hostLocationPathArgument{
		{"build", "release", func(p string) map[string]any { return map[string]any{"release": p} }},
		{"pull", "release", func(p string) map[string]any { return map[string]any{"release": p} }},
		{"describe", "release", func(p string) map[string]any { return map[string]any{"release": p} }},
		{"validate", "release", func(p string) map[string]any { return map[string]any{"release": p} }},
		{"lint", "entity", func(p string) map[string]any { return map[string]any{"entity": p} }},
		{"attach-candidates", "release", func(p string) map[string]any {
			return map[string]any{"graph_node": "backend", "release": p}
		}},
		{"attachment-container", "release", func(p string) map[string]any {
			return map[string]any{"graph_node": "frontend", "release": p}
		}},
		{connectionAtPositionToolName, "release", func(p string) map[string]any {
			return map[string]any{"file": "aggregate.yaml", "position": map[string]any{"line": 0, "character": 0},
				"release": p}
		}},
		{connectionAtPositionToolName, "file", func(p string) map[string]any {
			return map[string]any{"file": p, "position": map[string]any{"line": 0, "character": 0},
				"release": "release.yaml"}
		}},
		{"publish-entity", "entity", func(p string) map[string]any { return map[string]any{"entity": p, "dry_run": true} }},
		{"publish-artifact", "file", func(p string) map[string]any {
			return map[string]any{"file": p, "name": "fixture-doc", "tag": "0.0.1", "dry_run": true}
		}},
		{"deprecate-artifact", "from", func(p string) map[string]any { return map[string]any{"from": p} }},
		{"insertion-point", "file", func(p string) map[string]any {
			return map[string]any{"file": p, "container": []string{"spec"}}
		}},
		{"edit-document", "file", func(p string) map[string]any {
			return map[string]any{"edit": "set-value", "file": p, "node": []string{"spec"}, "name": "x", "value": "y"}
		}},
		{"import", "source", func(p string) map[string]any { return map[string]any{"source": p} }},
		{"gen-catalog", "output_dir", func(p string) map[string]any {
			return map[string]any{"from_embedded": true, "output_dir": p, "overlay": "overlays-q7x3"}
		}},
		{"pin", "entity", func(p string) map[string]any { return map[string]any{"entity": p} }},
	} {
		for _, spelling := range []struct {
			scenario string
			path     string
			// quoted is the caller's own spelling the refusal quotes back, and
			// instead the spelling it offers in its place; each is empty where
			// the refusal says neither.
			quoted, instead string
		}{
			{scenario: "a climb out of the root", path: hostLocationClimb, quoted: hostLocationClimb},
			{scenario: "a climb back into the root", path: climbBackIn, quoted: climbBackIn},
			{scenario: "an absolute path inside the root", path: fx.authoringRelease, instead: insideName},
			{scenario: "an absolute path outside the root", path: fx.outsideEntity},
		} {
			t.Run(arg.tool+" "+arg.argument+", "+spelling.scenario, func(t *testing.T) {
				got := answerOverTheListener(t, listener, credential, arg.tool, arg.args(spelling.path))
				text := strings.Join(got.texts, "\n")
				if !got.isError {
					t.Errorf("the listener admitted %q in '%s': %s", spelling.path, arg.argument, text)
				}
				if !strings.Contains(text, "'"+arg.argument+"'") {
					t.Errorf("the refusal does not name the argument '%s': %s", arg.argument, text)
				}
				if spelling.quoted != "" && !strings.Contains(text, fmt.Sprintf("%q", spelling.quoted)) {
					t.Errorf("the refusal does not quote back the caller's own %q: %s", spelling.quoted, text)
				}
				if spelling.instead != "" && !strings.Contains(text, fmt.Sprintf("send %q instead", spelling.instead)) {
					t.Errorf("the refusal does not offer %q, the name the caller may send instead: %s", spelling.instead, text)
				}
				for _, complaint := range hostLocationsInBytes(got.raw, fx.searched()) {
					t.Error(complaint)
				}
			})
		}
	}
}

// TestListReleasesAndPullNameRootRelativePathsOverAListenerAndAbsoluteOnesAtTheKeyboard
// shows the two answers the walk that found this rule's reach first read: the
// Release listing and the dependency resolution, each read field by field on both
// transports.
func TestListReleasesAndPullNameRootRelativePathsOverAListenerAndAbsoluteOnesAtTheKeyboard(t *testing.T) {
	fx := newHostLocationFixture(t)
	streams := fx.overTheProcessStreams(t)
	listener, credential := fx.overAListener(t)

	listing := map[string]any{"response_format": "detailed", "limit": 500}
	var keyboardListing, listenerListing listReleasesResult
	fx.reset(t)
	decodeAnswerPayload(t, answerOverTheProcessStreams(t, streams, "list-releases", listing), &keyboardListing)
	fx.reset(t)
	decodeAnswerPayload(t, answerOverTheListener(t, listener, credential, "list-releases", listing), &listenerListing)

	if told(keyboardListing.WorkspaceRoot) != fx.root || keyboardListing.WorkspaceRootWithheld {
		t.Errorf("at the keyboard the listing names the root %q (withheld=%v), want %q",
			told(keyboardListing.WorkspaceRoot), keyboardListing.WorkspaceRootWithheld, fx.root)
	}
	if listenerListing.WorkspaceRoot != nil || !listenerListing.WorkspaceRootWithheld {
		t.Errorf("over the listener the listing names the root %q (withheld=%v), want it absent and marked",
			told(listenerListing.WorkspaceRoot), listenerListing.WorkspaceRootWithheld)
	}
	for _, listed := range []struct {
		transport string
		listing   listReleasesResult
	}{{"at the keyboard", keyboardListing}, {"over the listener", listenerListing}} {
		if !slices.Contains(listed.listing.Releases, "release.yaml") {
			t.Errorf("%s the listing does not carry release.yaml relative to the root: %v", listed.transport,
				listed.listing.Releases)
		}
		if got := listed.listing.Lockfiles["release.yaml"]; got != "release.lock" {
			t.Errorf("%s the listing names release.yaml's lockfile %q, want release.lock", listed.transport, got)
		}
		for _, path := range listed.listing.Releases {
			if filepath.IsAbs(path) {
				t.Errorf("%s the listing carries the absolute path %q", listed.transport, path)
			}
		}
	}

	resolution := map[string]any{"release": "release.yaml"}
	var keyboardPull, listenerPull pullResultPayload
	fx.reset(t)
	decodeAnswerPayload(t, answerOverTheProcessStreams(t, streams, "pull", resolution), &keyboardPull)
	fx.reset(t)
	decodeAnswerPayload(t, answerOverTheListener(t, listener, credential, "pull", resolution), &listenerPull)

	for _, field := range []struct {
		name              string
		keyboard, over    *string
		keyboardWithheld  bool
		listenerWithheld  bool
		atTheKeyboard, to string
	}{
		{"release", keyboardPull.Release, listenerPull.Release, keyboardPull.ReleaseWithheld,
			listenerPull.ReleaseWithheld, filepath.Join(fx.root, "release.yaml"), "release.yaml"},
		{"lockfile", keyboardPull.Lockfile, listenerPull.Lockfile, keyboardPull.LockfileWithheld,
			listenerPull.LockfileWithheld, filepath.Join(fx.root, "release.lock"), "release.lock"},
	} {
		if told(field.keyboard) != field.atTheKeyboard || field.keyboardWithheld {
			t.Errorf("at the keyboard pull names its %s %q (withheld=%v), want %q", field.name, told(field.keyboard),
				field.keyboardWithheld, field.atTheKeyboard)
		}
		if told(field.over) != field.to || field.listenerWithheld {
			t.Errorf("over the listener pull names its %s %q (withheld=%v), want %q", field.name, told(field.over),
				field.listenerWithheld, field.to)
		}
	}
}

// TestTheCacheInventoryKeepsBackAPluginLoadedFromAFileAndAnswersARegistryReferenceWhole
// reads the cache inventory over the fixture's seeded machine-wide caches on both
// transports, module by module and identity by identity.
//
// A caller that launched this process is told every identity as it was
// recorded. A caller reaching a listener is told a registry reference's identity
// whole and nothing of a plugin loaded from a file: that identity is null in its
// place with its entry marked, and a compiled module's note naming it is kept back
// whole and marked, while the module's digest, which names no place, is answered.
func TestTheCacheInventoryKeepsBackAPluginLoadedFromAFileAndAnswersARegistryReferenceWhole(t *testing.T) {
	fx := newHostLocationFixture(t)
	streams := fx.overTheProcessStreams(t)
	listener, credential := fx.overAListener(t)

	fx.reset(t)
	keyboardAnswer := answerOverTheProcessStreams(t, streams, "cache-status", map[string]any{})
	fx.reset(t)
	listenerAnswer := answerOverTheListener(t, listener, credential, "cache-status", map[string]any{})
	var keyboard, overListener cacheStatusResult
	decodeAnswerPayload(t, keyboardAnswer, &keyboard)
	decodeAnswerPayload(t, listenerAnswer, &overListener)

	// Each module is looked up by its label on both transports. The compiled-module
	// class must hold exactly the seeded ones, which is what says the inventory read
	// the fixture's caches rather than this process's.
	modules := fx.cachedModules()
	compiledModules := func(transport string, p cacheStatusResult) map[string]cacheStatusElement {
		t.Helper()
		out := map[string]cacheStatusElement{}
		for _, class := range p.Classes {
			if class.Name != cacheops.CompiledModuleClassName {
				continue
			}
			for _, el := range class.Elements {
				out[told(el.Label)] = el
			}
		}
		if len(out) != len(modules) {
			t.Fatalf("%s the compiled-module class holds %d module(s), want the %d seeded: %v", transport, len(out),
				len(modules), out)
		}
		return out
	}
	keyboardModules := compiledModules("at the keyboard", keyboard)
	listenerModules := compiledModules("over the listener", overListener)

	keptBack, whole := 0, 0
	for _, module := range modules {
		label := hostLocationModuleDir + "/" + module.leaf
		names := make([]string, 0, len(module.identities))
		fromAFile := false
		for _, identity := range module.identities {
			names = append(names, identity.URI+"@"+identity.Version)
			fromAFile = fromAFile || identity.URI == fx.pluginPath
		}
		atKeyboard, known := keyboardModules[label]
		if !known {
			t.Errorf("at the keyboard the inventory does not list %s", label)
			continue
		}
		overIt := listenerModules[label]
		if want := strings.Join(names, ", "); atKeyboard.Note != want || atKeyboard.NoteWithheld {
			t.Errorf("at the keyboard %s reads %q (withheld=%v), want %q", label, atKeyboard.Note,
				atKeyboard.NoteWithheld, want)
		}
		switch {
		case fromAFile && (overIt.Note != "" || !overIt.NoteWithheld):
			t.Errorf("over the listener %s reads %q (withheld=%v), want its note kept back whole and marked", label,
				overIt.Note, overIt.NoteWithheld)
		case !fromAFile && (overIt.Note != atKeyboard.Note || overIt.NoteWithheld):
			t.Errorf("over the listener %s reads %q (withheld=%v), want %q whole", label, overIt.Note,
				overIt.NoteWithheld, atKeyboard.Note)
		}
		if overIt.Digest == "" || overIt.Digest != atKeyboard.Digest {
			t.Errorf("over the listener %s carries the digest %q, want %q: it names no place, so it is answered whole",
				label, overIt.Digest, atKeyboard.Digest)
		}
	}

	if len(overListener.Provenance) != len(keyboard.Provenance) || len(keyboard.Provenance) != len(modules) {
		t.Fatalf("the provenance lists %d entr(y/ies) at the keyboard and %d over the listener, want the %d seeded",
			len(keyboard.Provenance), len(overListener.Provenance), len(modules))
	}
	for i, atKeyboard := range keyboard.Provenance {
		overIt := overListener.Provenance[i]
		if atKeyboard.IdentitiesWithheld || len(overIt.Identities) != len(atKeyboard.Identities) {
			t.Errorf("provenance[%d] names %d identities over the listener and %d at the keyboard (keyboard "+
				"withheld=%v), want each in its place", i, len(overIt.Identities), len(atKeyboard.Identities),
				atKeyboard.IdentitiesWithheld)
			continue
		}
		entryKeepsBack := false
		for j, identity := range atKeyboard.Identities {
			name := told(identity)
			fromAFile := strings.HasPrefix(name, fx.pluginPath+"@")
			switch got := overIt.Identities[j]; {
			case identity == nil:
				t.Errorf("at the keyboard provenance[%d].identities[%d] is null", i, j)
			case fromAFile && got != nil:
				t.Errorf("over the listener provenance[%d].identities[%d] is %q, want null: it names a plugin "+
					"loaded from a file", i, j, *got)
			case fromAFile:
				keptBack++
				entryKeepsBack = true
			case told(got) != name:
				t.Errorf("over the listener provenance[%d].identities[%d] is %q, want %q whole", i, j, told(got), name)
			default:
				whole++
			}
		}
		if overIt.IdentitiesWithheld != entryKeepsBack {
			t.Errorf("over the listener provenance[%d] is marked withheld=%v, want %v", i, overIt.IdentitiesWithheld,
				entryKeepsBack)
		}
	}
	if keptBack == 0 || whole == 0 {
		t.Errorf("the listener's provenance kept back %d identit(y/ies) and answered %d whole, and the seeded "+
			"caches hold both kinds, so one half of the rule went unread", keptBack, whole)
	}

	keyboardText, listenerText := strings.Join(keyboardAnswer.texts, "\n"), strings.Join(listenerAnswer.texts, "\n")
	if !strings.Contains(keyboardText, fx.pluginPath+"@") {
		t.Errorf("at the keyboard the text does not name the plugin loaded from a file:\n%s", keyboardText)
	}
	for _, want := range []string{hostLocationKeptBackWords, "oci://" + hostLocationRegistry + "/plugin/importer/registry-q7x3@1.0.0"} {
		if !strings.Contains(listenerText, want) {
			t.Errorf("over the listener the text does not read %q:\n%s", want, listenerText)
		}
	}
	for _, complaint := range hostLocationsInBytes(listenerAnswer.raw, fx.searched()) {
		t.Error(complaint)
	}
}

// TestTheCacheInventoryKeepsBackTheLabelOfASourceReadFromThisHostAndAnswersARemoteOneWhole
// reads the cache inventory over the fixture's seeded source-artifact slots on
// both transports, slot by slot.
//
// A source-artifact slot's label is its location under the cache, composed from
// the URL its repository was resolved from and read back out of the names the
// cache spells it with. A caller that launched this process is told every label.
// A caller reaching a listener is told the label of a slot cloned over the
// network whole, and nothing of the label of a slot read from a repository on
// this host, whether through a file URL or a plain path: that label is absent and
// marked, while the slot's size and digest, which name no place, are answered.
func TestTheCacheInventoryKeepsBackTheLabelOfASourceReadFromThisHostAndAnswersARemoteOneWhole(t *testing.T) {
	fx := newHostLocationFixture(t)
	streams := fx.overTheProcessStreams(t)
	listener, credential := fx.overAListener(t)

	fx.reset(t)
	keyboardAnswer := answerOverTheProcessStreams(t, streams, "cache-status", map[string]any{})
	fx.reset(t)
	listenerAnswer := answerOverTheListener(t, listener, credential, "cache-status", map[string]any{})
	var keyboard, overListener cacheStatusResult
	decodeAnswerPayload(t, keyboardAnswer, &keyboard)
	decodeAnswerPayload(t, listenerAnswer, &overListener)

	// Each slot is looked up by its digest, which both transports answer, since the
	// listener's label may be absent. The class holding them must hold exactly the
	// seeded slots, which is what says the inventory read the fixture's cache.
	slots := fx.sourceSlots()
	seeded := map[string]bool{}
	for _, slot := range slots {
		seeded[slot.digestHex()] = true
	}
	sourceSlots := func(transport string, p cacheStatusResult) map[string]cacheStatusElement {
		t.Helper()
		out := map[string]cacheStatusElement{}
		for _, class := range p.Classes {
			holdsASeededSlot := false
			for _, el := range class.Elements {
				holdsASeededSlot = holdsASeededSlot || seeded[el.Digest]
			}
			if !holdsASeededSlot {
				continue
			}
			if len(class.Elements) != len(slots) {
				t.Fatalf("%s the class %q holds %d element(s), want the %d seeded slots", transport, class.Name,
					len(class.Elements), len(slots))
			}
			for _, el := range class.Elements {
				out[el.Digest] = el
			}
		}
		if len(out) != len(slots) {
			t.Fatalf("%s the inventory lists %d of the %d seeded slots: %v", transport, len(out), len(slots), out)
		}
		return out
	}
	keyboardSlots := sourceSlots("at the keyboard", keyboard)
	listenerSlots := sourceSlots("over the listener", overListener)

	keptBack, whole := 0, 0
	for _, slot := range slots {
		label := fx.sourceSlotLabel(t, slot)
		atKeyboard, overIt := keyboardSlots[slot.digestHex()], listenerSlots[slot.digestHex()]
		if told(atKeyboard.Label) != label || atKeyboard.LabelWithheld {
			t.Errorf("at the keyboard the slot for %s is labelled %q (withheld=%v), want %q", slot.url,
				told(atKeyboard.Label), atKeyboard.LabelWithheld, label)
		}
		switch {
		case slot.onHost && (overIt.Label != nil || !overIt.LabelWithheld):
			t.Errorf("over the listener the slot for %s is labelled %q (withheld=%v), want its label kept back and "+
				"marked: it spells a repository on this host", slot.url, told(overIt.Label), overIt.LabelWithheld)
		case slot.onHost:
			keptBack++
		case told(overIt.Label) != label || overIt.LabelWithheld:
			t.Errorf("over the listener the slot for %s is labelled %q (withheld=%v), want %q whole", slot.url,
				told(overIt.Label), overIt.LabelWithheld, label)
		default:
			whole++
		}
		if overIt.Size != atKeyboard.Size || overIt.SizeText != atKeyboard.SizeText || overIt.Digest != atKeyboard.Digest {
			t.Errorf("over the listener the slot for %s carries size %d (%q) and digest %q, want %d (%q) and %q: "+
				"neither names a place, so both are answered whole", slot.url, overIt.Size, overIt.SizeText,
				overIt.Digest, atKeyboard.Size, atKeyboard.SizeText, atKeyboard.Digest)
		}
	}
	if keptBack == 0 || whole == 0 {
		t.Errorf("the listener kept back %d slot label(s) and answered %d whole, and the seeded cache holds both "+
			"kinds, so one half of the rule went unread", keptBack, whole)
	}

	keyboardText, listenerText := strings.Join(keyboardAnswer.texts, "\n"), strings.Join(listenerAnswer.texts, "\n")
	for _, slot := range slots {
		label := fx.sourceSlotLabel(t, slot)
		if !strings.Contains(keyboardText, label) {
			t.Errorf("at the keyboard the text does not list %q:\n%s", label, keyboardText)
		}
		want := label + "  " + listenerSlots[slot.digestHex()].SizeText
		if slot.onHost {
			want = hostLocationKeptBackWords + "  " + listenerSlots[slot.digestHex()].SizeText
		}
		if !strings.Contains(listenerText, want) {
			t.Errorf("over the listener the text does not read %q:\n%s", want, listenerText)
		}
	}
	for _, complaint := range hostLocationsInBytes(listenerAnswer.raw, fx.searched()) {
		t.Error(complaint)
	}
}

// decodeAnswerPayload decodes an answer's structured payload into a typed value,
// failing where the call was refused or carried none.
func decodeAnswerPayload(t *testing.T, a hostLocationAnswer, into any) {
	t.Helper()
	if a.protocolError != "" || a.isError {
		t.Fatalf("the call was refused: %s %v", a.protocolError, a.texts)
	}
	if len(a.structuredBytes) == 0 {
		t.Fatalf("the answer carries no structured payload: %v", a.texts)
	}
	if err := json.Unmarshal(a.structuredBytes, into); err != nil {
		t.Fatalf("decode the structured payload: %v (%s)", err, a.structuredBytes)
	}
}

// TestTheEveryToolComparisonRejectsADamagedListenerAnswer is the negative control
// under the check above, and it is what makes that check worth running.
//
// Each row damages a real listener answer in one way a wrong implementation would
// produce, and requires the comparison or the byte search to name it. An
// assertion nobody has watched reject anything would be silent over the leak it
// exists to catch.
func TestTheEveryToolComparisonRejectsADamagedListenerAnswer(t *testing.T) {
	fx := newHostLocationFixture(t)
	streams := fx.overTheProcessStreams(t)
	listener, credential := fx.overAListener(t)
	bounds := fx.bounds()

	pair := func(tool string, args map[string]any) (hostLocationAnswer, hostLocationAnswer) {
		t.Helper()
		fx.reset(t)
		atTheKeyboard := answerOverTheProcessStreams(t, streams, tool, args)
		fx.reset(t)
		overTheListener := answerOverTheListener(t, listener, credential, tool, args)
		complaints, located := hostLocationComplaints(bounds, atTheKeyboard, overTheListener)
		complaints = append(complaints, hostLocationsInBytes(overTheListener.raw, fx.searched())...)
		if len(complaints) != 0 || located == 0 {
			t.Fatalf("the undamaged %s answers draw %v and judged %d location(s), so nothing below is attributable",
				tool, complaints, located)
		}
		return atTheKeyboard, overTheListener
	}
	listingAtTheKeyboard, listingOverTheListener := pair("list-releases",
		map[string]any{"response_format": "detailed", "limit": 500})
	pullAtTheKeyboard, pullOverTheListener := pair("pull", map[string]any{"release": "release.yaml"})
	keyboardRelease, _ := pullAtTheKeyboard.structured.(map[string]any)["release"].(string)
	cacheAtTheKeyboard, cacheOverTheListener := pair("cache-status", map[string]any{})
	keptEntry, keptAt := cacheProvenanceIdentityWhere(t, cacheOverTheListener.structured, true)
	wholeEntry, wholeAt := cacheProvenanceIdentityWhere(t, cacheOverTheListener.structured, false)
	unmarkedEntry := cacheProvenanceEntryKeepingNothingBack(t, cacheOverTheListener.structured)
	keptName := cacheProvenanceIdentitiesOf(cacheAtTheKeyboard.structured, keptEntry)[keptAt]
	onHostSlot, remoteSlot := fx.sourceSlotWhere(t, true), fx.sourceSlotWhere(t, false)
	keptLabel, _ := cacheElementWithDigest(cacheAtTheKeyboard.structured, onHostSlot.digestHex())["label"].(string)
	onHostElement := cacheElementWithDigest(cacheOverTheListener.structured, onHostSlot.digestHex())
	remoteElement := cacheElementWithDigest(cacheOverTheListener.structured, remoteSlot.digestHex())
	if keptLabel == "" || onHostElement == nil || onHostElement["label_withheld"] != true ||
		remoteElement == nil || remoteElement["label"] == nil {
		t.Fatalf("precondition: the cache inventory lists the slot for %s labelled %q at the keyboard and %v over "+
			"the listener, and the slot for %s as %v over the listener, so the rows damaging them would damage "+
			"nothing", onHostSlot.url, keptLabel, onHostElement, remoteSlot.url, remoteElement)
	}

	for _, tc := range []struct {
		// scenario says what a wrong implementation would have done.
		scenario string
		keyboard hostLocationAnswer
		listener hostLocationAnswer
		damage   func(structured map[string]any)
		// damageFrame damages the frame's bytes instead, for the search that reads
		// them.
		damageFrame func(frame []byte) []byte
		// names is what a complaint has to say.
		names string
	}{
		{
			scenario: "an absolute path left in a field",
			keyboard: pullAtTheKeyboard, listener: pullOverTheListener,
			damage: func(s map[string]any) { s["release"] = keyboardRelease },
			names:  "unchanged",
		},
		{
			scenario: "a kept-back value with its mark missing",
			keyboard: listingAtTheKeyboard, listener: listingOverTheListener,
			damage: func(s map[string]any) { delete(s, "workspace_root_withheld") },
			names:  "no mark",
		},
		{
			scenario: "a mark with its value still present",
			keyboard: pullAtTheKeyboard, listener: pullOverTheListener,
			damage: func(s map[string]any) { s["lockfile_withheld"] = true },
			names:  "still carries",
		},
		{
			scenario: "a wrong relative spelling",
			keyboard: pullAtTheKeyboard, listener: pullOverTheListener,
			damage: func(s map[string]any) { s["lockfile"] = "./release.lock" },
			names:  `want "release.lock"`,
		},
		{
			scenario: "a location in the raw bytes of a text block",
			keyboard: listingAtTheKeyboard, listener: listingOverTheListener,
			damageFrame: func(frame []byte) []byte {
				return bytes.Replace(frame, []byte(`"text":"`), []byte(`"text":"`+fx.root+` `), 1)
			},
			names: "the listener's frame carries",
		},
		{
			scenario: "an entry dropped from a list",
			keyboard: listingAtTheKeyboard, listener: listingOverTheListener,
			damage: func(s map[string]any) {
				releases, _ := s["releases"].([]any)
				s["releases"] = releases[1:]
			},
			names: "added or dropped",
		},
		{
			scenario: "a key only the listener answers that is no mark",
			keyboard: listingAtTheKeyboard, listener: listingOverTheListener,
			damage: func(s map[string]any) { s["workspace_path"] = "." },
			names:  "answered over the listener only",
		},
		{
			scenario: "a value kept back that names no place",
			keyboard: listingAtTheKeyboard, listener: listingOverTheListener,
			damage: func(s map[string]any) {
				delete(s, "response_format")
				s["response_format_withheld"] = true
			},
			names: "names no place",
		},
		{
			scenario: "a kept-back entry of a list carried whole",
			keyboard: cacheAtTheKeyboard, listener: cacheOverTheListener,
			damage: func(s map[string]any) { cacheProvenanceIdentitiesOf(s, keptEntry)[keptAt] = keptName },
			names:  "unchanged",
		},
		{
			scenario: "a kept-back entry of a list with its mark missing",
			keyboard: cacheAtTheKeyboard, listener: cacheOverTheListener,
			damage: func(s map[string]any) { delete(cacheProvenanceEntryOf(s, keptEntry), "identities_withheld") },
			names:  "no mark saying it was kept back",
		},
		{
			scenario: "an entry of a list kept back that names no place",
			keyboard: cacheAtTheKeyboard, listener: cacheOverTheListener,
			damage: func(s map[string]any) {
				cacheProvenanceIdentitiesOf(s, wholeEntry)[wholeAt] = nil
				cacheProvenanceEntryOf(s, wholeEntry)["identities_withheld"] = true
			},
			names: "names no place",
		},
		{
			scenario: "a list marked as keeping entries back that keeps none",
			keyboard: cacheAtTheKeyboard, listener: cacheOverTheListener,
			damage: func(s map[string]any) { cacheProvenanceEntryOf(s, unmarkedEntry)["identities_withheld"] = true },
			names:  "still carries every entry",
		},
		{
			scenario: "a source slot's label spelling a repository on this host carried through",
			keyboard: cacheAtTheKeyboard, listener: cacheOverTheListener,
			damage: func(s map[string]any) {
				element := cacheElementWithDigest(s, onHostSlot.digestHex())
				element["label"] = keptLabel
				delete(element, "label_withheld")
			},
			names: "unchanged",
		},
		{
			scenario: "a source slot's label naming only a network host kept back",
			keyboard: cacheAtTheKeyboard, listener: cacheOverTheListener,
			damage: func(s map[string]any) {
				element := cacheElementWithDigest(s, remoteSlot.digestHex())
				delete(element, "label")
				element["label_withheld"] = true
			},
			names: "names no place",
		},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			damaged := tc.listener
			var complaints []string
			if tc.damageFrame != nil {
				damaged.raw = tc.damageFrame(append([]byte(nil), tc.listener.raw...))
				if bytes.Equal(damaged.raw, tc.listener.raw) {
					t.Fatal("precondition: the damage left the frame as it was, so this row would pass on nothing")
				}
				complaints = hostLocationsInBytes(damaged.raw, fx.searched())
			} else {
				var structured map[string]any
				if err := json.Unmarshal(tc.listener.structuredBytes, &structured); err != nil {
					t.Fatalf("copy the listener's answer: %v", err)
				}
				tc.damage(structured)
				damaged.structured = structured
				complaints, _ = hostLocationComplaints(bounds, tc.keyboard, damaged)
			}
			if len(complaints) == 0 {
				t.Fatalf("%s drew no complaint, so the check would not notice it", tc.scenario)
			}
			named := false
			for _, complaint := range complaints {
				if strings.Contains(complaint, tc.names) {
					named = true
					break
				}
			}
			if !named {
				t.Errorf("%s drew complaints that do not say %q: %v", tc.scenario, tc.names, complaints)
			}
			t.Logf("%s produced: %v", tc.scenario, complaints)
		})
	}
}

// cacheElementWithDigest is the element of a decoded cache-status answer whose
// digest is the one given, whichever class holds it, or nil where there is none.
// It shares its keys with the answer, so a row damaging it damages the answer.
func cacheElementWithDigest(structured any, digestHex string) map[string]any {
	s, _ := structured.(map[string]any)
	classes, _ := s["classes"].([]any)
	for _, rawClass := range classes {
		class, _ := rawClass.(map[string]any)
		elements, _ := class["elements"].([]any)
		for _, rawElement := range elements {
			if element, _ := rawElement.(map[string]any); element["digest"] == digestHex {
				return element
			}
		}
	}
	return nil
}

// cacheProvenanceEntryOf is entry i of a decoded cache-status answer's
// provenance, or nil where it has none.
func cacheProvenanceEntryOf(structured any, i int) map[string]any {
	s, _ := structured.(map[string]any)
	entries, _ := s["provenance"].([]any)
	if i < 0 || i >= len(entries) {
		return nil
	}
	entry, _ := entries[i].(map[string]any)
	return entry
}

// cacheProvenanceIdentitiesOf is the identity list of entry i of a decoded
// cache-status answer's provenance. It shares its entries with the answer, so a
// row damaging one damages the answer.
func cacheProvenanceIdentitiesOf(structured any, i int) []any {
	identities, _ := cacheProvenanceEntryOf(structured, i)["identities"].([]any)
	return identities
}

// cacheProvenanceIdentityWhere finds the first identity of a decoded cache-status
// answer's provenance that is kept back, or the first answered whole, as asked. It
// fails where there is none, since a row damaging it would then damage nothing.
func cacheProvenanceIdentityWhere(t *testing.T, structured any, keptBack bool) (entry, at int) {
	t.Helper()
	for i := 0; cacheProvenanceEntryOf(structured, i) != nil; i++ {
		for j, identity := range cacheProvenanceIdentitiesOf(structured, i) {
			if (identity == nil) == keptBack {
				return i, j
			}
		}
	}
	what := "answered whole"
	if keptBack {
		what = "kept back"
	}
	t.Fatalf("precondition: the listener's cache inventory holds no identity %s, and the fixture's caches are "+
		"seeded with one", what)
	return 0, 0
}

// cacheProvenanceEntryKeepingNothingBack finds the first entry of a decoded
// cache-status answer's provenance that answers every identity whole and carries
// no mark, failing where there is none.
func cacheProvenanceEntryKeepingNothingBack(t *testing.T, structured any) int {
	t.Helper()
	for i := 0; cacheProvenanceEntryOf(structured, i) != nil; i++ {
		if cacheProvenanceEntryOf(structured, i)["identities_withheld"] == true {
			continue
		}
		if !slices.Contains(cacheProvenanceIdentitiesOf(structured, i), nil) {
			return i
		}
	}
	t.Fatal("precondition: the listener's cache inventory holds no provenance entry answered whole, and the " +
		"fixture's caches are seeded with one")
	return 0
}
