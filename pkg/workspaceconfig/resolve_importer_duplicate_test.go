// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// importerDuplicateLayout is a workspace laid out for one row of the duplicate
// importer tests: a root directory carrying the workspace's own file, a nearer
// directory below it the resolution starts in, and a global file outside both.
// Each file is written only when its row gives it content.
type importerDuplicateLayout struct {
	root, nearer, global string
}

// file names each configuration file a row can write, in the spelling a refusal
// names it by.
func (l importerDuplicateLayout) file(which string) string {
	switch which {
	case "root":
		return filepath.Join(l.root, workspaceconfig.ConfigFilename)
	case "root-ci":
		return filepath.Join(l.root, workspaceconfig.AppendCIConfigFilename)
	case "nearer":
		return filepath.Join(l.nearer, workspaceconfig.ConfigFilename)
	case "global":
		return l.global
	}
	panic("unknown configuration file " + which)
}

// layImporterDuplicateWorkspace writes each file a row gives content to and
// resolves the workspace from the nearer directory, reading the global file only
// where the row writes one, so no row reads the configuration of the machine it
// runs on.
func layImporterDuplicateWorkspace(t *testing.T, files map[string]string) (importerDuplicateLayout, *workspaceconfig.ResolvedWorkspace, error) {
	t.Helper()
	// The scratch directory is read through any link in its own path, so the
	// files a refusal names are spelled as the resolution spells them.
	scratch, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("read the scratch directory: %v", err)
	}
	l := importerDuplicateLayout{
		root:   filepath.Join(scratch, "ws"),
		nearer: filepath.Join(scratch, "ws", "svc"),
		global: filepath.Join(scratch, "machine", "config.yaml"),
	}
	write(t, l.file("root"), "workspace: {}\n"+files["root"])
	for _, which := range []string{"root-ci", "nearer", "global"} {
		if content, ok := files[which]; ok {
			write(t, l.file(which), content)
		}
	}
	if _, ok := files["nearer"]; !ok {
		write(t, filepath.Join(l.nearer, ".keep"), "")
	}
	env := map[string]string{}
	if _, ok := files["global"]; ok {
		env[workspaceconfig.EnvConfigOverride] = l.global
	}
	rw, err := workspaceconfig.Resolve(l.nearer, "", frozenLookup(env))
	return l, rw, err
}

// importerEntryWant is one entry a refusal must name: the key it is listed
// under, its position there, which of the layout's files wrote it, and its uri
// as written.
type importerEntryWant struct {
	key   string
	index int
	file  string
	uri   string
}

// words is how the refusal names the entry: the key and position a schema
// refusal names, the file it was written in, and its uri as written.
func (w importerEntryWant) words(l importerDuplicateLayout) string {
	return fmt.Sprintf("%s[%d] in %s (uri %q)", w.key, w.index, l.file(w.file), w.uri)
}

// TestResolveRefusesTwoImporterEntriesNamingOneImporter proves the one rule of
// the importer list: two entries naming one importer — their uris equal once a
// version is split off — halt resolution, wherever in the cascade each was
// written, and the refusal names the importer and both entries, in list order,
// each by its key, its position, the file it came from and its uri as written.
// One row per place a second entry can come from: the same list, the same file's
// append companion, a nearer file's append companion, the CI-owned overlay, and
// the workspace file's companion over the global file's list; between them the
// rows name one importer at one version twice, at two versions, and by a range.
func TestResolveRefusesTwoImporterEntriesNamingOneImporter(t *testing.T) {
	t.Parallel()
	const alpha = "importer/import-alpha"
	for _, tc := range []struct {
		name          string
		files         map[string]string
		first, second importerEntryWant
	}{
		{
			name:   "two entries of one file's list",
			files:  map[string]string{"root": "plugins:\n  importer:\n    - uri: " + alpha + ":1.0.0\n    - uri: " + alpha + ":1.0.0\n"},
			first:  importerEntryWant{"plugins.importer", 0, "root", alpha + ":1.0.0"},
			second: importerEntryWant{"plugins.importer", 1, "root", alpha + ":1.0.0"},
		},
		{
			name:   "a file's list and its own append companion, at two versions",
			files:  map[string]string{"root": "plugins:\n  importer:\n    - uri: " + alpha + ":1.0.0\n  importer_append:\n    - uri: " + alpha + ":2.0.0\n"},
			first:  importerEntryWant{"plugins.importer", 0, "root", alpha + ":1.0.0"},
			second: importerEntryWant{"plugins.importer_append", 0, "root", alpha + ":2.0.0"},
		},
		{
			name: "a file's list and a nearer file's append companion, by a range",
			files: map[string]string{
				"root":   "plugins:\n  importer:\n    - uri: importer/import-beta:1.0.0\n    - uri: " + alpha + ":1.0.0\n",
				"nearer": "plugins:\n  importer_append:\n    - uri: " + alpha + ":^1.0.0\n",
			},
			first:  importerEntryWant{"plugins.importer", 1, "root", alpha + ":1.0.0"},
			second: importerEntryWant{"plugins.importer_append", 0, "nearer", alpha + ":^1.0.0"},
		},
		{
			name: "a file's list and the CI-owned overlay beside it",
			files: map[string]string{
				"root":    "plugins:\n  importer:\n    - uri: " + alpha + ":1.0.0\n",
				"root-ci": "plugins:\n  importer:\n    - uri: " + alpha + ":2.0.0\n",
			},
			first:  importerEntryWant{"plugins.importer", 0, "root", alpha + ":1.0.0"},
			second: importerEntryWant{"plugins.importer", 0, "root-ci", alpha + ":2.0.0"},
		},
		{
			name: "the global file's list and the workspace file's append companion",
			files: map[string]string{
				"global": "plugins:\n  importer:\n    - uri: " + alpha + ":1.0.0\n",
				"root":   "plugins:\n  importer_append:\n    - uri: importer/import-beta:1.0.0\n    - uri: " + alpha + ":1.0.0\n",
			},
			first:  importerEntryWant{"plugins.importer", 0, "global", alpha + ":1.0.0"},
			second: importerEntryWant{"plugins.importer_append", 1, "root", alpha + ":1.0.0"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l, _, err := layImporterDuplicateWorkspace(t, tc.files)
			if err == nil {
				t.Fatal("a configuration attaching one importer by two entries resolved; want a loud halt")
			}
			if !errors.Is(err, workspaceconfig.ErrDuplicateImporterAttachment()) {
				t.Fatalf("the halt is not ErrDuplicateImporterAttachment: %v", err)
			}
			msg := err.Error()
			if want := fmt.Sprintf("importer %q", alpha); !strings.Contains(msg, want) {
				t.Errorf("the refusal does not name the importer, %s: %v", want, err)
			}
			first, second := strings.Index(msg, tc.first.words(l)), strings.Index(msg, tc.second.words(l))
			if first < 0 {
				t.Errorf("the refusal does not name the earlier entry, %s: %v", tc.first.words(l), err)
			}
			if second < 0 {
				t.Errorf("the refusal does not name the later entry, %s: %v", tc.second.words(l), err)
			}
			if first >= 0 && second >= 0 && second < first {
				t.Errorf("the refusal names the later entry first: %v", err)
			}
		})
	}
}

// TestResolveAcceptsImporterEntriesNamingDistinctImporters is the other side of
// the rule, and the boundary of what it judges: entries naming different
// importers load; a farther file's duplicate a nearer file's list replaces is no
// longer in the composed list, so it loads; two entries naming one module file by
// path name no importer and load; and the governance and linter lists keep
// accepting one plugin attached more than once.
func TestResolveAcceptsImporterEntriesNamingDistinctImporters(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		files map[string]string
		// importers, governance and linter are the lengths each composed list
		// must have.
		importers, governance, linter int
	}{
		{
			name:      "two importers, one entry each",
			files:     map[string]string{"root": "plugins:\n  importer:\n    - uri: importer/import-alpha:1.0.0\n  importer_append:\n    - uri: importer/import-beta:1.0.0\n"},
			importers: 2,
		},
		{
			name: "a farther file's duplicate replaced by a nearer file's list",
			files: map[string]string{
				"root":   "plugins:\n  importer:\n    - uri: importer/import-alpha:1.0.0\n    - uri: importer/import-alpha:2.0.0\n",
				"nearer": "plugins:\n  importer:\n    - uri: importer/import-alpha:2.0.0\n",
			},
			importers: 1,
		},
		{
			name:      "one module file attached twice by path",
			files:     map[string]string{"root": "plugins:\n  importer:\n    - uri: ./import.wasm\n    - uri: ./import.wasm\n"},
			importers: 2,
		},
		{
			name: "one plugin attached twice on the governance list",
			files: map[string]string{"root": "plugins:\n  governance:\n    - uri: oci://h/policy:1\n      role: ir-verdict\n" +
				"    - uri: oci://h/policy:1\n      role: ir-verdict\n  governance_append:\n    - uri: oci://h/policy:2\n      role: render-verdict\n"},
			governance: 3,
		},
		{
			name: "one plugin attached twice on the linter list",
			files: map[string]string{
				"root":   "plugins:\n  linter:\n    - uri: oci://h/lint:1\n",
				"nearer": "plugins:\n  linter_append:\n    - uri: oci://h/lint:1\n",
			},
			linter: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, rw, err := layImporterDuplicateWorkspace(t, tc.files)
			if err != nil {
				t.Fatalf("the configuration was refused: %v", err)
			}
			plugins := rw.Config().Plugins
			if got := len(plugins.Importer); got != tc.importers {
				t.Errorf("composed importer list: %d entries, want %d: %+v", got, tc.importers, plugins.Importer)
			}
			if got := len(plugins.Governance); got != tc.governance {
				t.Errorf("composed governance list: %d entries, want %d: %+v", got, tc.governance, plugins.Governance)
			}
			if got := len(plugins.Linter); got != tc.linter {
				t.Errorf("composed linter list: %d entries, want %d: %+v", got, tc.linter, plugins.Linter)
			}
		})
	}
}
