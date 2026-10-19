// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1 && !light

package embedded

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestContentDigestsMatchGuestDerivation asserts the embedded-identity accessor
// reports exactly the content digest each guest derives to — the same derivation
// every content-addressing consumer (the loader, the content-addressed caches)
// performs on the decompressed guest bytes. Covering the full embedded set
// (URIs), not only the entity-carrying subset, because a describe-cache entry is
// keyed by the guest's content digest whether or not that guest ships an entity.
// It is order-aligned with URIs so the set is deterministic.
func TestContentDigestsMatchGuestDerivation(t *testing.T) {
	uris := URIs()
	got := ContentDigests()
	if len(got) != len(uris) {
		t.Fatalf("ContentDigests count = %d; want %d (one per embedded URI)", len(got), len(uris))
	}
	for i, u := range uris {
		b, ok := Lookup(u)
		if !ok {
			t.Fatalf("Lookup miss for embedded URI %q", u)
		}
		want := digest.ContentDigest(b)
		if got[i] != want {
			t.Errorf("ContentDigests[%d] for %q = %q; want %q", i, u, got[i], want)
		}
	}
}

// TestContentDigestsDeterministic asserts repeated calls return byte-identical,
// order-stable output (Deterministic Output invariant).
func TestContentDigestsDeterministic(t *testing.T) {
	a, b := ContentDigests(), ContentDigests()
	if len(a) != len(b) {
		t.Fatalf("length drift: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("order/content drift at %d: %q vs %q", i, a[i], b[i])
		}
	}
}

// TestManifestDerivedURIsAreStable pins the exact bare identities the embedded
// set registers under. The registry keys are load-bearing: the plugin-resolution
// cascade's embed tier and every Plugin entity's bare identity (metadata.name)
// resolve against these exact strings, so a change here is a resolution-behavior
// change, not a refactor. The list is asserted verbatim rather than derived so a
// manifest edit that alters a key fails loudly here.
func TestManifestDerivedURIsAreStable(t *testing.T) {
	want := []string{
		"provider/helm",
		"provider/compose",
		"provider/helm-chart",
		"provider/terraform",
		"provider/cloudformation",
		"linter/publishing",
		"parser/hcl",
		"parser/toml",
		"parser/yaml",
		"parser/json",
		"parser/jsonc",
		"governance/label-injector",
		"governance/selector-tagger",
		"governance/rego",
		"governance/cel",
	}
	got := embeddedArtifacts()
	if len(got) != len(want) {
		t.Fatalf("embedded artifact count = %d; want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].uri != w {
			t.Errorf("artifact[%d].uri = %q; want %q", i, got[i].uri, w)
		}
	}
}

// TestTheBinaryEmbedsNoImporter pins that no core importer is embedded: the core
// importers are built and published with each compiler release, and the binary
// carries none of their bytes. A row added to the core-plugin list for an
// importer without its embed field set to "no" fails here.
//
// The binary does carry an entity and a guest digest of every core plugin it
// does not embed, from a second embedded directory, so this also holds that
// directory and the first to carrying no module of such a plugin: a module
// written into either — or a plugin of the list's own "no" rows that the embed
// tier answers for — fails here as well.
func TestTheBinaryEmbedsNoImporter(t *testing.T) {
	prefix := string(plugintype.Importer) + "/"
	for _, a := range embeddedArtifacts() {
		if strings.HasPrefix(a.uri, prefix) {
			t.Errorf("the binary embeds the importer %q; a core importer is published, never embedded", a.uri)
		}
	}

	for _, a := range publishedArtifacts() {
		if _, ok := Lookup(a.uri); ok {
			t.Errorf("the binary embeds the module of %q, a core plugin the core-plugin list says it does not embed", a.uri)
		}
	}

	embeddedModules := map[string]bool{}
	for _, a := range embeddedArtifacts() {
		embeddedModules[a.wasmGz] = true
	}
	for _, tree := range []struct {
		name string
		fsys fs.FS
		root string
	}{
		{name: "the embedded plugins' directory", fsys: pluginFS, root: "plugins"},
		{name: "the directory of what is carried of the plugins not embedded", fsys: publishedFS, root: publishedDir},
	} {
		err := fs.WalkDir(tree.fsys, tree.root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if (strings.HasSuffix(p, ".wasm") || strings.HasSuffix(p, ".wasm.gz")) && !embeddedModules[p] {
				t.Errorf("%s embeds the module %s, which belongs to no plugin the binary embeds", tree.name, p)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", tree.name, err)
		}
	}
}

// TestEveryCorePluginTheBinaryDoesNotEmbedCarriesItsEntityAndGuestDigest holds
// what the binary carries of each core plugin it does not embed to what the
// scripts build and publish for it. For every row of the core-plugin list whose
// embed field is "no", in list order and with no other plugin among them:
//
//   - PublishedPlugins carries it;
//   - its entity is a Plugin entity of the row's own identity, byte-identical to
//     the entity the generator wrote beside its guest — the entity the release
//     publishes;
//   - its digest is the binary's own content digest of that guest decompressed,
//     so the digest the guest build wrote with sha256sum and the derivation every
//     content-addressing consumer compares agree, and a guest rebuilt without its
//     digest is caught.
//
// The guest and the entity are found where the scripts' reader of the list says
// the row's artifacts are, and the directory the binary embeds is held to the
// one the scripts write into, so neither side's spelling of a place can drift
// from the other's.
func TestEveryCorePluginTheBinaryDoesNotEmbedCarriesItsEntityAndGuestDigest(t *testing.T) {
	lib := coreListLibrary(t)
	moduleRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve the module root: %v", err)
	}
	here, err := os.Getwd()
	if err != nil {
		t.Fatalf("read the test's working directory: %v", err)
	}
	wantSelectionDir, err := filepath.Rel(moduleRoot, filepath.Join(here, publishedDir))
	if err != nil {
		t.Fatalf("relate the embedded directory to the module root: %v", err)
	}
	if got := scriptsAnswer(t, lib, `printf '%s' "$CORE_PLUGIN_SELECTION_DIR"`); got != filepath.ToSlash(wantSelectionDir) {
		t.Fatalf("the scripts write what the binary carries into %q; the binary embeds %q", got, filepath.ToSlash(wantSelectionDir))
	}

	rows := publishedArtifacts()
	if len(rows) == 0 {
		t.Fatal("the core-plugin list names no plugin the binary does not embed, so nothing here is proven")
	}
	carried := PublishedPlugins()
	var got, want []string
	for _, p := range carried {
		got = append(got, p.URI)
	}
	for _, a := range rows {
		want = append(want, a.uri)
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("the binary carries [%s]; want every plugin the core-plugin list does not embed, in list order: [%s]",
			strings.Join(got, " "), strings.Join(want, " "))
	}

	for _, p := range carried {
		t.Run(p.URI, func(t *testing.T) {
			socket, name, _ := strings.Cut(p.URI, "/")
			dir := scriptsAnswer(t, lib, `core_plugin_dir_of "$2" "$3"`, socket, name)

			var entity types.Plugin
			if err := yaml.Unmarshal(p.Entity, &entity); err != nil {
				t.Fatalf("the carried entity does not decode as a Plugin: %v", err)
			}
			if entity.Kind != "Plugin" || entity.Coordinate() != p.URI {
				t.Errorf("the carried entity is %s %q; want the Plugin %q", entity.Kind, entity.Coordinate(), p.URI)
			}
			besideGuest, err := os.ReadFile(filepath.Join(moduleRoot, dir, name+".plugin.yaml"))
			if err != nil {
				t.Fatalf("read the entity generated beside the guest: %v", err)
			}
			if !bytes.Equal(p.Entity, besideGuest) {
				t.Errorf("the carried entity differs from the one generated beside the guest in %s", dir)
			}

			guest, err := os.ReadFile(filepath.Join(moduleRoot, dir, name+".wasm.gz"))
			if err != nil {
				t.Fatalf("read the guest the release publishes: %v", err)
			}
			zr, err := gzip.NewReader(bytes.NewReader(guest))
			if err != nil {
				t.Fatalf("open the guest: %v", err)
			}
			defer func() { _ = zr.Close() }()
			module, err := io.ReadAll(zr)
			if err != nil {
				t.Fatalf("decompress the guest: %v", err)
			}
			if derived := digest.ContentDigest(module); p.Digest != derived {
				t.Errorf("the carried digest is %q; the guest in %s digests to %q", p.Digest, dir, derived)
			}
		})
	}
}

// scriptsAnswer runs command after sourcing the scripts' reader of the
// core-plugin list at lib, with args as its positional parameters from $2 on,
// and returns what it printed, failing the test on any refusal.
func scriptsAnswer(t *testing.T, lib, command string, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("bash", append([]string{"-c", `. "$1" && ` + command, "bash", lib}, args...)...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("run %q over the scripts' reader: %v\n%s", command, err, stderr.String())
	}
	return strings.TrimSuffix(stdout.String(), "\n")
}

// coreListCase is one core-plugin list both readers are run over, and what both
// must answer: the embedded identities in list order, or a refusal.
type coreListCase struct {
	name     string
	list     string
	embedded []string
	refused  bool
}

// TestTheScriptsAndTheBinaryReadTheCorePluginListAlike holds the scripts' reader
// of the core-plugin list (scripts/lib/core-plugins.sh, core_plugin_rows) and
// the binary's (parseManifest) to one answer over shared fixtures and over the
// list itself: which rows are embedded, and which lists are refused. The two are
// in different languages, so the agreement is held by running both rather than
// by sharing code. The fixtures carry the shapes the embed field can take,
// including the one a shell `read` with a tab separator misreads — empty extra
// roots before an embed field — and a planted unknown value both must refuse.
func TestTheScriptsAndTheBinaryReadTheCorePluginListAlike(t *testing.T) {
	lib := coreListLibrary(t)
	cases := []coreListCase{
		{name: "a row of four fields is embedded", list: "provider\thelm\t./p\tgo\n", embedded: []string{"provider/helm"}},
		{name: "a row whose extra roots are empty and whose embed field is no is not embedded",
			list: "provider\thelm\t./p\tgo\nimporter\tlangflow\t./q\tgo\t\tno\n", embedded: []string{"provider/helm"}},
		{name: "a row with extra roots and no embed field is embedded",
			list: "linter\tpublishing\tp\trust\t../a,../b\n", embedded: []string{"linter/publishing"}},
		{name: "an empty embed field is embedded", list: "parser\tjson\t./p\tgo\t\t\n", embedded: []string{"parser/json"}},
		{name: "comments, blank lines and a carriage return are skipped",
			list: "# a comment\n\nparser\tjson\t./p\tgo\r\n", embedded: []string{"parser/json"}},
		{name: "an embed value other than empty or no is refused", list: "parser\tjson\t./p\tgo\t\tyes\n", refused: true},
		{name: "a row with no name is refused", list: "parser\n", refused: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "list.tsv")
			if err := os.WriteFile(path, []byte(tc.list), 0o644); err != nil {
				t.Fatalf("write the fixture: %v", err)
			}
			binary, binaryRefused := binaryEmbedded([]byte(tc.list))
			script, scriptRefused := scriptEmbedded(t, lib, path)
			if binaryRefused != tc.refused || scriptRefused != tc.refused {
				t.Fatalf("refused: binary %v, scripts %v; want %v", binaryRefused, scriptRefused, tc.refused)
			}
			if tc.refused {
				return
			}
			if got, want := strings.Join(binary, " "), strings.Join(tc.embedded, " "); got != want {
				t.Errorf("binary embeds [%s]; want [%s]", got, want)
			}
			if got, want := strings.Join(script, " "), strings.Join(tc.embedded, " "); got != want {
				t.Errorf("scripts embed [%s]; want [%s]", got, want)
			}
		})
	}

	t.Run("the core-plugin list itself", func(t *testing.T) {
		script, refused := scriptEmbedded(t, lib, "")
		if refused {
			t.Fatal("the scripts' reader refused the core-plugin list")
		}
		var binary []string
		for _, a := range embeddedArtifacts() {
			binary = append(binary, a.uri)
		}
		if got, want := strings.Join(script, " "), strings.Join(binary, " "); got != want {
			t.Errorf("the scripts embed [%s]; the binary registers [%s]", got, want)
		}
	})
}

// coreListLibrary returns the path of the scripts' reader of the core-plugin
// list, after reading it in this process. The reader runs in a child bash, whose
// reads the test cache does not record; reading the library here is what makes a
// change to it re-run this test rather than replay a cached pass.
func coreListLibrary(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Fatalf("bash is required to run the scripts' reader of the core-plugin list: %v", err)
	}
	lib, err := filepath.Abs(filepath.Join("..", "..", "..", "scripts", "lib", "core-plugins.sh"))
	if err != nil {
		t.Fatalf("resolve the reader's path: %v", err)
	}
	b, err := os.ReadFile(lib)
	if err != nil || len(b) == 0 {
		t.Fatalf("read the reader at %s: %v (%d bytes)", lib, err, len(b))
	}
	return lib
}

// binaryEmbedded runs parseManifest over list and returns the identities it
// registers, or reports that it refused the list.
func binaryEmbedded(list []byte) (uris []string, refused bool) {
	defer func() {
		if recover() != nil {
			uris, refused = nil, true
		}
	}()
	for _, a := range parseManifest(list) {
		uris = append(uris, a.uri)
	}
	return uris, false
}

// scriptEmbedded runs core_plugin_rows over the list at path — the core-plugin
// list itself when path is empty — and returns the identities of the rows it
// reads as embedded, or reports that it refused the list.
func scriptEmbedded(t *testing.T, lib, path string) (uris []string, refused bool) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("bash", "-c", `. "$1" && core_plugin_rows ${2:+"$2"}`, "bash", lib, path)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			t.Fatalf("run the scripts' reader: %v\n%s", err, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Errorf("the scripts' reader refused the list and still printed rows:\n%s", stdout.String())
		}
		return nil, true
	}
	for _, line := range strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n") {
		fields := strings.Split(line, "\x1f")
		if len(fields) != 7 {
			t.Fatalf("the scripts' reader printed %d fields, not seven: %q", len(fields), line)
		}
		switch fields[5] {
		case "yes":
			uris = append(uris, fields[0]+"/"+fields[1])
		case notEmbedded:
		default:
			t.Fatalf("the scripts' reader printed the embed value %q, neither yes nor %s: %q", fields[5], notEmbedded, line)
		}
	}
	return uris, false
}

// TestPublishingLinterURIMatchesManifest guards the exported linter identity
// against the manifest-derived key. PublishingLinterURI is a const shared with
// the light build (which embeds no bytes and cannot read the manifest); the
// default build derives the same reference from the manifest. This test fails if
// the two ever diverge, so the shared identity stays single-sourced.
func TestPublishingLinterURIMatchesManifest(t *testing.T) {
	for _, a := range embeddedArtifacts() {
		if a.uri == PublishingLinterURI {
			return
		}
	}
	t.Fatalf("PublishingLinterURI %q not among manifest-derived embedded URIs", PublishingLinterURI)
}
