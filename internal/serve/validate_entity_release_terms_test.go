// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// entityCheckWorkspace lays down a workspace keeping catalog variants side by
// side, one per Release, and opens it the way the daemon opens any workspace a
// request names, through the session's own resolution — the one place a
// workspace's per-Release catalog directories are read.
//
// It copies the shared fixture whose variant-sized/ declares demo/datastore/db
// with its storage drawn from the SchemaLibrary Sizing, which variant-sized/
// alone carries, and whose variant-plain/ declares the same Component referring
// to no library. Beside them it copies the command's validate fixture catalog as
// variant-recipe/ — a helm Recipe and the Component it implements — and gives
// that Component a required attachment slot, which a check of the Recipe on its
// own cannot fill. The configuration gives release-sized.yaml, release-plain.yaml
// and release-recipe.yaml one variant each, with no workspace list and no
// conventional catalog directory.
//
// The workspace holds an Aggregate and no Manifest, so every Release's closure
// fails to resolve. That failure is what only the closure path reports, which is
// what tells the two answers apart.
func entityCheckWorkspace(t *testing.T) (*Server, string) {
	t.Helper()
	src, err := filepath.Abs(filepath.Join("..", "compiler", "testdata", "release-entity-check"))
	if err != nil {
		t.Fatalf("locate the fixture: %v", err)
	}
	recipeCatalog, err := filepath.Abs(filepath.Join("..", "..", "cmd", "aggrega", "testdata", "validate", "catalog"))
	if err != nil {
		t.Fatalf("locate the validate fixture catalog: %v", err)
	}
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatalf("copy the fixture workspace: %v", err)
	}
	if err := os.CopyFS(filepath.Join(root, "variant-recipe"), os.DirFS(recipeCatalog)); err != nil {
		t.Fatalf("copy the validate fixture catalog: %v", err)
	}
	component := filepath.Join(root, "variant-recipe", "component", "example", "compute", "service", "1.0.0", "component.yaml")
	authored, err := os.ReadFile(component)
	if err != nil {
		t.Fatalf("read the Recipe's Component: %v", err)
	}
	slotted := strings.Replace(string(authored), "spec:\n  schema:",
		"spec:\n  featuresSchema:\n    credential:\n      required: true\n      inject: [env]\n  schema:", 1)
	if slotted == string(authored) {
		t.Fatal("the validate fixture's Component no longer has the shape this test splices a required slot into")
	}
	recipeRelease := strings.Replace(mustRead(t, filepath.Join(root, "release-sized.yaml")), "name: variant-sized", "name: variant-recipe", 1)
	aggregate := "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\nmetadata:\n  name: entity-check\n  version: \"1.0.0\"\n" +
		"spec:\n  components:\n    store:\n      type: demo/datastore/db\n"
	config := "workspace:\n  releases:\n" +
		"    release-sized.yaml:\n      catalog_dirs:\n        - variant-sized\n" +
		"    release-plain.yaml:\n      catalog_dirs:\n        - variant-plain\n" +
		"    release-recipe.yaml:\n      catalog_dirs:\n        - variant-recipe\n"
	for path, content := range map[string]string{
		component: slotted,
		filepath.Join(root, "release-recipe.yaml"):          recipeRelease,
		filepath.Join(root, "aggregate.yaml"):               aggregate,
		filepath.Join(root, workspaceconfig.ConfigFilename): config,
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	sess := newTestSession(t, root, bytes.NewReader(nil), &bytes.Buffer{})
	srv, err := sess.workspaceFor(root)
	if err != nil {
		t.Fatalf("open the workspace at %s: %v", root, err)
	}
	return srv, root
}

// TestValidateChecksACatalogEntityInTheTermsOfTheReleaseNamed is the invocation
// proof that aggrega/validate naming a catalog entity file and a Release runs the
// single-entity check the command line and the tools run, in that Release's
// terms: one daemon, every request over the real framing, every answer read
// whole.
//
// The subjects decide by which catalog they are read against and by nothing
// else: a Component whose pillar refers to a library only one variant carries,
// and a Recipe whose Component only one variant declares. Each is checked in the
// terms of the Release that reads it — clean — and of one that does not —
// failing, and naming what that Release's directories lack. The Recipe's
// Component declares a required attachment slot, so its clean row also holds that
// a check of one Recipe does not report a slot nothing it builds could fill.
//
// The unsaved row sends a buffer that differs from the file on disk, and the
// answer follows the buffer. The refusal row names a Release that is not there,
// and is answered with the sentence the other two surfaces refuse it with.
//
// The last three rows are the answers that do not move: naming no Release answers
// for the document alone, and the Release's own Aggregate and a library — kinds
// the single-entity check does not take — are answered by the Release's closure,
// whose failure here is what only that path reports.
func TestValidateChecksACatalogEntityInTheTermsOfTheReleaseNamed(t *testing.T) {
	t.Parallel()
	srv, root := entityCheckWorkspace(t)
	at := func(rel string) string { return filepath.Join(root, rel) }
	sized := at("variant-sized/component/db.yaml")
	recipe := at("variant-recipe/recipe/helm/service/1.0.0/recipe.yaml")
	libraryCode := conditionCodesOf(compactform.ErrUnresolvedSchemaLibraryRef()).Code
	if libraryCode == "" {
		t.Fatal("the unresolved-library condition carries no code, so the rows asserting it would be vacuous")
	}
	const closurePrefix = "Dependencies: "
	const notAnEntityTarget = "single-entity validation target"

	cases := []struct {
		name    string
		file    string
		release string
		// edit, when set, turns the file's bytes into the buffer the request carries;
		// the file on disk is left as it is.
		edit func(string) string
		// clean is a row whose answer carries no entry at all.
		clean bool
		// code is the condition an error entry must carry, or empty.
		code string
		// checked is the checked key the answer must carry.
		checked string
		// carries are phrases some entry must carry, and lacks phrases none may.
		carries []string
		lacks   []string
	}{
		{name: "a Component in the terms of the Release whose directories carry its library is clean",
			file: sized, release: "release-sized.yaml", clean: true},
		{name: "the same Component in the terms of a Release whose directories lack the library fails naming it",
			file: sized, release: "release-plain.yaml", code: libraryCode,
			carries: []string{"SchemaLibrary Sizing not found in catalog"}, lacks: []string{closurePrefix}},
		{name: "the other variant is clean in its own Release's terms",
			file: at("variant-plain/component/db.yaml"), release: "release-plain.yaml", clean: true},
		{name: "the editor's unsaved copy is what is checked",
			file: sized, release: "release-sized.yaml",
			edit: func(s string) string {
				return strings.Replace(s, `"Sizing#/schemas/Storage"`, `"Absent#/schemas/Storage"`, 1)
			},
			code:    libraryCode,
			carries: []string{"SchemaLibrary Absent not found in catalog"}},
		{name: "a Recipe whose Component declares a required slot is clean in its own Release's terms",
			file: recipe, release: "release-recipe.yaml", clean: true},
		{name: "the Recipe in the terms of a Release whose directories hold no Component it implements fails naming it",
			file: recipe, release: "release-sized.yaml",
			carries: []string{`implements target "example/compute/service" not found in catalog`}, lacks: []string{closurePrefix}},
		{name: "a Release that is not there is answered with the sentence the other surfaces refuse it with",
			file: sized, release: "release-gone.yaml",
			carries: []string{"the Release release-gone.yaml does not exist"}, lacks: []string{closurePrefix}},
		{name: "naming no Release answers for the document alone",
			file: sized, clean: true, checked: checkedDocument},
		{name: "the Release's own Aggregate is answered by the Release's closure",
			file: at("aggregate.yaml"), release: "release-sized.yaml",
			carries: []string{closurePrefix}, lacks: []string{notAnEntityTarget}},
		{name: "a library is answered by the Release's closure",
			file: at("variant-sized/schemalibrary/sizing.yaml"), release: "release-sized.yaml",
			carries: []string{closurePrefix}, lacks: []string{notAnEntityTarget}},
	}
	// Not parallel: the rows share one daemon and several name one file, and a
	// file's answer is read back from the store each validation of it writes.
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := mustRead(t, tc.file)
			if tc.edit != nil {
				edited := tc.edit(content)
				if edited == content {
					t.Fatal("the edit changed nothing, so the row would check the file as saved")
				}
				content = edited
			}
			res := validateContentOverTheWire(t, srv, tc.file, content, tc.release)
			if res.Checked != tc.checked {
				t.Errorf("checked = %q; want %q", res.Checked, tc.checked)
			}
			messages := diagMessages(res.Diagnostics)
			if tc.clean && len(res.Diagnostics) != 0 {
				t.Fatalf("want a clean answer; got %q", messages)
			}
			if !tc.clean && len(res.Diagnostics) == 0 {
				t.Fatal("want a finding; the answer carries none")
			}
			if tc.code != "" {
				var sawCode bool
				for _, d := range res.Diagnostics {
					if d.Code == tc.code && d.Severity == editorSeverityError {
						sawCode = true
					}
				}
				if !sawCode {
					t.Errorf("no error entry carries the condition %q: %q", tc.code, messages)
				}
			}
			joined := strings.Join(messages, "\n")
			for _, phrase := range tc.carries {
				if !strings.Contains(joined, phrase) {
					t.Errorf("no entry carries %q: %q", phrase, messages)
				}
			}
			for _, phrase := range tc.lacks {
				if strings.Contains(joined, phrase) {
					t.Errorf("an entry carries %q: %q", phrase, messages)
				}
			}
		})
	}
}
