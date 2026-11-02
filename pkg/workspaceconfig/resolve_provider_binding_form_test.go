// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestResolveRefusesAProviderBindingWrittenAsARegistryLocation drives the real
// configuration load over a provider binding whose uri is written with the oci://
// scheme. A provider binding names its plugin by its identity, provider/<name>
// with an optional :<version>, which is what capability resolution compares it
// against; a registry location names where a plugin is published, so a binding
// written as one can select nothing and is refused as the file is read. The
// refusal is asserted against its condition's accessor, and it must name the
// file, the entry, the capability, the uri as written and the form to write.
//
// Every layer a binding can be written in is a row — the base list, its append
// companion in a nearer file, the CI-owned overlay and the global file — because
// each is read through its own path and a refusal reached by only some of them
// would let the same binding through from the others.
//
// The controls are the forms that stay legitimate: the bare identity with and
// without a version — the second carries a slash and a colon, the shape a broader
// registry-reference predicate would also have refused — and an oci:// uri on a
// list this check does not judge.
func TestResolveRefusesAProviderBindingWrittenAsARegistryLocation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		// files maps a path under the workspace root to its content; a path under
		// "global/" is the global configuration file instead.
		files map[string]string
		// start is the directory resolution starts from, relative to the root.
		start   string
		refused bool
		// file is the configuration file the refusal must name, relative to the
		// root; names are further fragments the message must carry.
		file  string
		names []string
	}{
		{
			name: "the binding plugin add wrote before, in the base list, is refused",
			files: map[string]string{
				".aggrega-config.yaml": "workspace: {}\nplugins:\n  providers:\n    - capability: compose\n      uri: oci://hub.aggrega.ai/plugin/provider/compose\n",
			},
			refused: true,
			file:    ".aggrega-config.yaml",
			names:   []string{"plugins.providers[0]", `"compose"`, `"oci://hub.aggrega.ai/plugin/provider/compose"`, "uri: provider/<name>", ":<version>"},
		},
		{
			name: "a versioned registry location in a nearer providers_append is refused naming that file",
			files: map[string]string{
				".aggrega-config.yaml":     "workspace: {}\nplugins:\n  providers:\n    - capability: helm\n      uri: provider/helm\n",
				"svc/.aggrega-config.yaml": "plugins:\n  providers_append:\n    - capability: compose\n      uri: oci://hub.aggrega.ai/plugin/provider/compose:0.8.6\n",
			},
			start:   "svc",
			refused: true,
			file:    "svc/.aggrega-config.yaml",
			names:   []string{"plugins.providers_append[0]", `"compose"`, `"oci://hub.aggrega.ai/plugin/provider/compose:0.8.6"`, "uri: provider/<name>"},
		},
		{
			name: "a registry location in the CI-owned overlay is refused",
			files: map[string]string{
				".aggrega-config.yaml":                 "workspace: {}\n",
				workspaceconfig.AppendCIConfigFilename: "plugins:\n  providers:\n    - capability: helm\n      uri: oci://registry.example.com/plugin/provider/helm:1.0.0\n",
			},
			refused: true,
			file:    workspaceconfig.AppendCIConfigFilename,
			names:   []string{"plugins.providers[0]", `"helm"`, `"oci://registry.example.com/plugin/provider/helm:1.0.0"`},
		},
		{
			name: "a registry location in the global file is refused",
			files: map[string]string{
				"global/aggrega/config.yaml": "plugins:\n  providers:\n    - capability: helm\n      uri: oci://hub.aggrega.ai/plugin/provider/helm\n",
				".aggrega-config.yaml":       "workspace: {}\n",
			},
			refused: true,
			file:    "global/aggrega/config.yaml",
			names:   []string{"plugins.providers[0]", `"helm"`},
		},
		{
			name: "control: the bare identity with no version loads",
			files: map[string]string{
				".aggrega-config.yaml": "workspace: {}\nplugins:\n  providers:\n    - capability: compose\n      uri: provider/compose\n",
			},
		},
		{
			name: "control: the bare identity with a version loads",
			files: map[string]string{
				".aggrega-config.yaml": "workspace: {}\nplugins:\n  providers:\n    - capability: compose\n      uri: provider/compose:0.8.6\n",
			},
		},
		{
			name: "control: an oci:// uri on a list this check does not judge loads",
			files: map[string]string{
				".aggrega-config.yaml": "workspace: {}\nplugins:\n  importer:\n    - uri: oci://h/imp:1\n",
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			xdg := filepath.Join(root, "global")
			for rel, body := range tc.files {
				write(t, filepath.Join(root, rel), body)
			}
			start := filepath.Join(root, tc.start)

			_, err := workspaceconfig.Resolve(start, "", frozenLookup(map[string]string{
				"XDG_CONFIG_HOME": xdg,
			}))
			if !tc.refused {
				if err != nil {
					t.Fatalf("Resolve refused a legitimate configuration: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Resolve accepted a provider binding written as a registry location; expected a loud halt")
			}
			if !errors.Is(err, workspaceconfig.ErrConfigInvalid()) {
				t.Fatalf("halt must be ErrConfigInvalid; got: %v", err)
			}
			// The file is matched by its path beneath the root rather than in full, so
			// the assertion does not depend on how the temporary root is spelled.
			for _, want := range append([]string{filepath.FromSlash(tc.file)}, tc.names...) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal must name %q; got: %v", want, err)
				}
			}
		})
	}
}

// TestUpsertProviderBindingRefusesARegistryLocation proves the writer holds a
// binding to the same reading the load does: a binding written as a registry
// location is refused before anything reaches disk, so no command can write a
// configuration the next command refuses to read.
func TestUpsertProviderBindingRefusesARegistryLocation(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), workspaceconfig.ConfigFilename)

	err := workspaceconfig.UpsertProviderBinding(path, "compose", "oci://hub.aggrega.ai/plugin/provider/compose")
	if !errors.Is(err, workspaceconfig.ErrConfigInvalid()) {
		t.Fatalf("UpsertProviderBinding of a registry location: want ErrConfigInvalid; got: %v", err)
	}
	if _, serr := os.Stat(path); !os.IsNotExist(serr) {
		t.Fatalf("a refused binding reached disk: stat %s = %v", path, serr)
	}

	// Control: the identity form is written and reads back.
	if err := workspaceconfig.UpsertProviderBinding(path, "compose", "provider/compose"); err != nil {
		t.Fatalf("UpsertProviderBinding of the identity form: %v", err)
	}
	rw, err := workspaceconfig.Resolve(filepath.Dir(path), "")
	if err != nil {
		t.Fatalf("resolve the written binding: %v", err)
	}
	if got := rw.Config().Plugins.ProviderTargetURIs()["compose"]; got != "provider/compose" {
		t.Fatalf("written binding reads back as %q; want provider/compose", got)
	}
}
