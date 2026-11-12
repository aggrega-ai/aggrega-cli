// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The layer names an attribution row expects. A row names the layer rather than
// a path, because the paths are the test's own temporary files and only the
// row's layout says which of them is which.
const (
	wantGlobalFile  = "the global file"
	wantProjectFile = "the project file"
	wantCIOverlay   = "the CI-owned overlay"
	wantDefaults    = "(built-in defaults)"
)

// attributionRow is one scenario of the attribution rule: the layers a
// workspace is given, the setting read back, and the layer that must be named
// for it.
type attributionRow struct {
	name string

	// global, project and ci are the bodies of the three files a row can
	// write. An empty body writes no file at all.
	global  string
	project string
	ci      string
	env     map[string]string
	offline *bool

	field     string
	wantValue string
	// want is one of the layer names above, or a label spelled as the report
	// spells it — an environment override or a flag.
	want string
	// holdsTheDefault marks a row whose whole point is that the layer writes
	// the value the built-in defaults already give. The row checks that it
	// does, so a default moving later cannot quietly turn it into a row about
	// a changed value.
	holdsTheDefault bool
	// wantNoPosition marks a file-attributed row whose key the document walk
	// cannot address.
	wantNoPosition bool
}

// TestValueOriginsNamesTheLayerThatSetsASetting drives the attribution rule row
// by row: a setting is attributed to the highest-precedence layer that sets it,
// whatever value that layer sets.
//
// The rows that matter are the ones where the value a layer writes is the value
// already in force — the built-in default, or what a layer beneath says. Named
// by a change of value, those rows report the built-in defaults or the lower
// layer, and a reader looking for where the value comes from is sent somewhere
// removing the line from would change nothing. Two controls sit beside them: a
// setting no layer mentions, which must still read as the built-in defaults, and
// a layer writing a value different from the one beneath, which every reading of
// the rule names the same way. They are what keeps the rows above from being
// satisfied by a report that names the nearest file for everything.
func TestValueOriginsNamesTheLayerThatSetsASetting(t *testing.T) {
	t.Parallel()
	off := false
	clauseOne := "default_registry: hub.aggrega.ai\nauto_pull: false\noffline: true\n"

	for _, tc := range []attributionRow{
		{
			name:    "a project file writing the default registry",
			project: clauseOne, field: "default_registry", wantValue: "hub.aggrega.ai",
			want: wantProjectFile, holdsTheDefault: true,
		},
		{
			name:    "a project file writing auto_pull as the default has it",
			project: clauseOne, field: "auto_pull", wantValue: "false",
			want: wantProjectFile, holdsTheDefault: true,
		},
		{
			name:    "the same file, on a setting whose value differs from the default",
			project: clauseOne, field: workspaceconfig.OfflineSettingKey, wantValue: "true",
			want: wantProjectFile,
		},
		{
			name:    "a project file restating the value the global file sets",
			global:  "default_registry: registry.example.test\n",
			project: "default_registry: registry.example.test\n",
			field:   "default_registry", wantValue: "registry.example.test",
			want: wantProjectFile,
		},
		{
			name:  "a variable exported with the default's value",
			env:   map[string]string{"AGGREGA_DEFAULT_REGISTRY": "hub.aggrega.ai"},
			field: "default_registry", wantValue: "hub.aggrega.ai",
			want: "env:AGGREGA_DEFAULT_REGISTRY", holdsTheDefault: true,
		},
		{
			name:    "a variable restating the value the project file sets",
			project: "default_registry: registry.example.test\n",
			env:     map[string]string{"AGGREGA_DEFAULT_REGISTRY": "registry.example.test"},
			field:   "default_registry", wantValue: "registry.example.test",
			want: "env:AGGREGA_DEFAULT_REGISTRY",
		},
		{
			name:    "--offline=false where no file carries the posture",
			project: "default_registry: registry.example.test\n", offline: &off,
			field: workspaceconfig.OfflineSettingKey, wantValue: "false",
			want: "--" + workspaceconfig.OfflineSettingKey, holdsTheDefault: true,
		},
		{
			name:    "an authored empty catalog-directory list",
			project: "workspace:\n  catalog_dirs: []\n",
			field:   "workspace.catalog_dirs", wantValue: "[]",
			want: wantProjectFile, holdsTheDefault: true,
		},
		{
			name:    "the control: a setting no layer mentions",
			project: clauseOne, field: "parse_cache.max_entries", wantValue: "1000",
			want: wantDefaults, holdsTheDefault: true,
		},
		{
			name:    "the control: a layer writing a value different from the one beneath",
			global:  "default_registry: global.example.test\n",
			project: "default_registry: project.example.test\n",
			field:   "default_registry", wantValue: "project.example.test",
			want: wantProjectFile,
		},
		{
			name:    "the CI-owned overlay carrying a key it is not read for",
			project: "mcp:\n  allowed_origins: [\"https://base.example.test\"]\n",
			ci:      "mcp:\n  allowed_origins: [\"https://ci.example.test\"]\n",
			field:   "mcp.allowed_origins", wantValue: "[https://base.example.test]",
			want: wantProjectFile,
		},
		{
			name:    "the CI-owned overlay adding through the append companion",
			project: "workspace:\n  catalog_dirs: [./base]\n",
			ci:      "workspace:\n  catalog_dirs_append: [./extra]\n",
			field:   "workspace.catalog_dirs",
			want:    wantCIOverlay,
		},
		{
			name:    "a setting supplied through a merge key",
			project: "parse_cache: &caps\n  max_bytes: 100000000\ncompiled_module_cache:\n  <<: *caps\n",
			field:   "compiled_module_cache.max_bytes", wantValue: "100000000",
			want: wantProjectFile, wantNoPosition: true,
		},
		{
			name:    "the signing requirement written false by every layer that mentions it",
			global:  "signing:\n  required: false\n",
			project: "signing:\n  required: false\n",
			field:   "signing.required", wantValue: "false",
			want: wantDefaults, holdsTheDefault: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertAttribution(t, tc)
		})
	}
}

// assertAttribution resolves the row's workspace and checks the one setting it
// is about.
func assertAttribution(t *testing.T, tc attributionRow) {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "workspace")
	paths := map[string]string{
		wantGlobalFile:  filepath.Join(dir, "global", "config.yaml"),
		wantProjectFile: filepath.Join(root, workspaceconfig.ConfigFilename),
		wantCIOverlay:   filepath.Join(root, workspaceconfig.AppendCIConfigFilename),
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	vars := map[string]string{}
	for k, v := range tc.env {
		vars[k] = v
	}
	if tc.global != "" {
		write(t, paths[wantGlobalFile], tc.global)
		vars[workspaceconfig.EnvConfigOverride] = paths[wantGlobalFile]
	}
	if tc.project != "" {
		write(t, paths[wantProjectFile], tc.project)
	}
	if tc.ci != "" {
		write(t, paths[wantCIOverlay], tc.ci)
	}

	rw, err := workspaceconfig.Resolve(root, root, frozenLookup(vars))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if tc.offline != nil {
		rw = rw.WithFlags(workspaceconfig.FlagOverlay{Offline: tc.offline})
	}
	got, ok := originsByField(t, rw)[tc.field]
	if !ok {
		t.Fatalf("the report carries no %q setting", tc.field)
	}

	if tc.wantValue != "" && got.Value != tc.wantValue {
		t.Fatalf("precondition: %s resolved to %q; the row writes %q, so it is not configuring what it claims",
			tc.field, got.Value, tc.wantValue)
	}
	if tc.holdsTheDefault {
		if def := builtInDefaultOf(t, tc.field); got.Value != def {
			t.Fatalf("precondition: %s is %q and the built-in defaults give %q, so this row is about a changed value "+
				"rather than one restating the default", tc.field, got.Value, def)
		}
	}

	want := tc.want
	if path, isFile := paths[tc.want]; isFile {
		want = path
	}
	if got.Source != want {
		t.Errorf("%s is attributed to %q; want %s (%q)", tc.field, got.Source, tc.want, want)
	}

	_, isFile := paths[tc.want]
	switch {
	case isFile && tc.wantNoPosition && got.Position != nil:
		t.Errorf("%s was set through a key the document walk cannot address, yet carries position %+v",
			tc.field, *got.Position)
	case isFile && !tc.wantNoPosition && got.Position == nil:
		t.Errorf("%s is attributed to a config file and carries no position in it", tc.field)
	case !isFile && got.Position != nil:
		t.Errorf("%s is attributed to %q, which has no document, and carries position %+v", tc.field, got.Source, *got.Position)
	}
}

// builtInDefaultOf returns the value the report shows for field in a
// workspace no layer configures — the built-in default, rendered the way every
// other value is.
func builtInDefaultOf(t *testing.T, field string) string {
	t.Helper()
	root := t.TempDir()
	rw, err := workspaceconfig.Resolve(root, root, frozenLookup(nil))
	if err != nil {
		t.Fatalf("Resolve an unconfigured workspace: %v", err)
	}
	o, ok := originsByField(t, rw)[field]
	if !ok {
		t.Fatalf("the report carries no %q setting", field)
	}
	if o.Source != wantDefaults {
		t.Fatalf("precondition: in a workspace nothing configures, %s is attributed to %q", field, o.Source)
	}
	return o.Value
}
