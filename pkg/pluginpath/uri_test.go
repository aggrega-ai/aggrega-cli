// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pluginpath

import (
	"bufio"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/namesegmenttest"
)

// TestGuestFilenameCarriesTheSocket: two plugins sharing a bare name in
// different sockets reach two filenames.
//
// A plugin's bare name is unique only within its socket, so the socket is the
// only thing telling these two apart. A derivation keeping just the last segment
// of the reference reaches one filename for both, and a file two plugins are
// staged under holds whichever was written last.
func TestGuestFilenameCarriesTheSocket(t *testing.T) {
	governance := PluginLeaf("governance/tag-injector")
	linter := PluginLeaf("linter/tag-injector")
	if governance == linter {
		t.Fatalf("governance/tag-injector and linter/tag-injector both reach %q; the socket is lost", governance)
	}
	for _, want := range []struct{ name, socket string }{
		{governance, "governance"},
		{linter, "linter"},
	} {
		if !strings.HasPrefix(want.name, want.socket) {
			t.Errorf("filename %q does not carry its socket %q", want.name, want.socket)
		}
	}
}

// TestGuestFilenameCarriesThePinnedVersion: two versions of one plugin reach two
// filenames, and a reference pinning no version reaches a third.
//
// The third case is the one a version-stripping derivation and a
// version-appending one both get wrong: stripping folds every pin onto the
// unpinned name, while appending an empty version folds an empty pin onto the
// unpinned name. Only carrying the pin's presence separates all three.
func TestGuestFilenameCarriesThePinnedVersion(t *testing.T) {
	seen := map[string]string{}
	for _, ref := range []string{
		"governance/tag-injector",
		"governance/tag-injector:",
		"governance/tag-injector:1.0.0",
		"governance/tag-injector:2.0.0",
	} {
		name := PluginLeaf(ref)
		if other, clash := seen[name]; clash {
			t.Errorf("%q and %q both reach %q", other, ref, name)
			continue
		}
		seen[name] = ref
	}
}

// TestGuestFilenameCollidesOnlyOnEqualParts is the injectivity property, stated
// against the reference rather than against the parts.
//
// Two references reach one filename exactly when they split into the same parts.
// That is the precise contract: the derivation is injective on what it reads,
// and what it reads is the reference after its two documented normalisations, so
// an assertion of blanket injectivity over raw reference strings would be false
// and an assertion over part lists would only be re-testing the encoding.
//
// The corpus is the shared segment corpus, crossed with itself so a hyphen can
// move across the join between two parts, and crossed with a pin set carrying
// the absent pin, the empty pin and a pin spelling a separator. A corpus that
// merely looked varied would leave the join itself untested.
func TestGuestFilenameCollidesOnlyOnEqualParts(t *testing.T) {
	refs := guestFilenameReferenceCorpus()
	if len(refs) < 100 {
		t.Fatalf("reference corpus is %d entries, too small to establish the property", len(refs))
	}
	byName := map[string]string{}
	for _, ref := range refs {
		name := PluginLeaf(ref)
		other, clash := byName[name]
		if !clash {
			byName[name] = ref
			continue
		}
		if !reflect.DeepEqual(pluginLeafParts(other), pluginLeafParts(ref)) {
			t.Errorf("%q (parts %q) and %q (parts %q) both reach %q, but their parts differ",
				other, pluginLeafParts(other), ref, pluginLeafParts(ref), name)
		}
	}
}

// TestGuestFilenameReadsBackIntoItsParts: a derived filename divides back into
// exactly the parts it was built from.
//
// Distinctness alone says no two references in the corpus collided; recovering
// the parts says the filename carries them rather than merely differing, which
// is what makes a distinctness result about the derivation instead of about the
// corpus. The reader is the shared one, written against the encoding's stated
// contract and sharing no code with it.
func TestGuestFilenameReadsBackIntoItsParts(t *testing.T) {
	for _, ref := range guestFilenameReferenceCorpus() {
		name := strings.TrimSuffix(PluginLeaf(ref), guestFileSuffix)
		got := namesegmenttest.ReadName(t, '-', name)
		if want := pluginLeafParts(ref); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: filename reads back as %q, built from %q", ref, got, want)
		}
	}
}

// TestGuestFilenameIsOneContainedPathSegment proves the containment sentence the
// derivation's godoc states: a derived filename is a single path segment that
// cannot climb out of the directory it is joined to.
//
// The corpus deliberately includes references spelling a relative-path reference
// and a nested path, because those are the inputs that would climb if any part
// were carried through unescaped or if the suffix were ever made conditional.
func TestGuestFilenameIsOneContainedPathSegment(t *testing.T) {
	const dir = "/base/dir"
	for _, ref := range append(guestFilenameReferenceCorpus(),
		"..", "../..", ".", "a/../..", "governance/..", "oci://../..") {
		name := PluginLeaf(ref)
		if name == "." || name == ".." {
			t.Errorf("%q reaches %q, which is a relative-path reference", ref, name)
		}
		if strings.ContainsAny(name, `/\`) {
			t.Errorf("%q reaches %q, which is more than one path segment", ref, name)
		}
		if got := filepath.Join(dir, name); !strings.HasPrefix(got, dir+"/") {
			t.Errorf("%q reaches %q, which joins to %q — outside %q", ref, name, got, dir)
		}
	}
}

// TestGuestFilenameCarriesNoUppercase proves the case sentence the alphabet's
// godoc states. A plugin's identity coordinate stays cased, so a derivation
// carrying an uppercase letter through would be injective as a string and not as
// a filename: a filesystem comparing names case-insensitively would fold two
// distinct coordinates onto one file.
func TestGuestFilenameCarriesNoUppercase(t *testing.T) {
	for _, ref := range append(guestFilenameReferenceCorpus(), "governance/A", "GOVERNANCE/a") {
		name := PluginLeaf(ref)
		if name != strings.ToLower(name) {
			t.Errorf("%q reaches %q, which carries an uppercase letter", ref, name)
		}
	}
	if PluginLeaf("governance/A") == PluginLeaf("governance/a") {
		t.Error("governance/A and governance/a reach one filename; the case distinction is lost")
	}
}

// TestGuestFilenameNormalisesTheTwoSpellings: a leading scheme and a trailing
// suffix name the same guest as the reference without them, so both reach the
// same filename. This is the one place the derivation is deliberately many-to-one,
// and it is stated so a later reader can tell it apart from a lost distinction.
func TestGuestFilenameNormalisesTheTwoSpellings(t *testing.T) {
	for _, pair := range []struct{ spelled, bare string }{
		{"oci://provider/helm", "provider/helm"},
		{"provider/helm.wasm", "provider/helm"},
		{"oci://provider/helm.wasm", "provider/helm"},
	} {
		if got, want := PluginLeaf(pair.spelled), PluginLeaf(pair.bare); got != want {
			t.Errorf("%q reaches %q, %q reaches %q; the two spell one reference", pair.spelled, got, pair.bare, want)
		}
	}
}

// TestGuestFilenameIsDeterministic: repeated derivation of one reference yields
// byte-identical output, which is what lets a writer and a reader in different
// processes agree on the file without exchanging the name.
func TestGuestFilenameIsDeterministic(t *testing.T) {
	for _, ref := range guestFilenameReferenceCorpus() {
		first := PluginLeaf(ref)
		for i := 0; i < 3; i++ {
			if got := PluginLeaf(ref); got != first {
				t.Fatalf("%q derived %q then %q", ref, first, got)
			}
		}
	}
}

// TestColocatedCatalogGeneratorSpellingAgrees checks the one spelling of this
// mapping that cannot import it.
//
// The developer-owned co-located catalog is laid down by a shell script, which
// writes each guest's file directly rather than through this derivation, because
// it runs before any binary exists to ask. That makes it a second spelling, and a
// second spelling agreeing today is exactly what hides tomorrow's divergence. So
// the agreement is asserted here, over the manifest the script reads: for every
// embedded plugin, the flat spelling the script writes has to be the filename
// this derivation reads.
//
// A plugin whose socket or name leaves the admitted alphabet fails here rather
// than silently reaching a file the resolver never looks for — the flat spelling
// is only correct while every part is carried through as authored.
func TestColocatedCatalogGeneratorSpellingAgrees(t *testing.T) {
	rows := embeddedPluginManifestRows(t)
	if len(rows) == 0 {
		t.Fatal("embedded-plugin manifest yielded no rows; the agreement is unproven, not established")
	}
	for _, row := range rows {
		socket, name := row[0], row[1]
		// The spelling scripts/gen-colocated-plugin-catalog.sh writes.
		script := socket + "--" + name + guestFileSuffix
		if got := PluginLeaf(socket + "/" + name); got != script {
			t.Errorf("plugin %s/%s: the co-located catalog generator writes %q, the resolver reads %q",
				socket, name, script, got)
		}
	}
}

// embeddedPluginManifestRows reads the embedded-plugin manifest and returns one
// (socket, name) pair per declared plugin, skipping blank and commented lines.
func embeddedPluginManifestRows(t *testing.T) [][2]string {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "internal", "wasm", "embedded", "embedded-plugins.tsv"))
	if err != nil {
		t.Fatalf("open embedded-plugin manifest: %v", err)
	}
	defer func() { _ = f.Close() }()

	var rows [][2]string
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := scan.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			t.Fatalf("embedded-plugin manifest row %q carries fewer than two fields", line)
		}
		rows = append(rows, [2]string{fields[0], fields[1]})
	}
	if err := scan.Err(); err != nil {
		t.Fatalf("read embedded-plugin manifest: %v", err)
	}
	return rows
}

// guestFilenameReferenceCorpus builds the reference corpus the properties above
// are established over: every ordered pair drawn from the shared segment corpus,
// joined as a two-segment reference, crossed with a pin set.
//
// The pin set carries three shapes rather than one, because each is decided by a
// different branch of the split: no pin at all, an empty pin, and a pin whose
// text spells the separator it was split at.
func guestFilenameReferenceCorpus() []string {
	segments := namesegmenttest.Corpus('-')
	pins := []string{"", ":", ":1.0.0", ":a-b", ":a:b"}
	refs := make([]string, 0, len(segments)*len(segments)*len(pins))
	for _, socket := range segments {
		for _, name := range segments {
			for _, pin := range pins {
				refs = append(refs, socket+"/"+name+pin)
			}
		}
	}
	return refs
}
