// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestResolveRefusesAVerdictAttachmentNamingAFunctionAndNoURI drives the real
// configuration load over an attachment at the verdict tier — a linter entry, or
// a governance entry in a verdict role — that names an entry function and no
// plugin uri. A cross-cutting socket selects its plugin by uri alone, so such an
// entry attaches nothing and the function it names would never be called; it is
// refused as the file carrying it is read. The refusal is asserted against its
// condition's accessor, and it must name the file, the list and the entry's
// position, the function as written and the uri the author is to add.
//
// Every layer an attachment can be written in is a row — the base list, an
// append companion in a nearer file, the CI-owned overlay and the global file —
// because each is read through its own path, and a refusal reached by only some
// of them would let the same entry through from the others.
//
// The controls are the attachments that stay legitimate and a configuration
// with none, which must load as before. Two further rows bound the check: a
// capability-bound entry stays under the cross-cutting socket refusal it already
// met, and a governance entry in a transform role is not at the verdict tier, so
// this check does not judge it.
func TestResolveRefusesAVerdictAttachmentNamingAFunctionAndNoURI(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		// files maps a path under the workspace root to its content; a path under
		// "global/" is the global configuration file instead.
		files map[string]string
		// start is the directory resolution starts from, relative to the root.
		start string
		// refusedBy is the condition the load must halt under; nil means it loads.
		refusedBy error
		// file is the configuration file the refusal must name, relative to the
		// root; names are further fragments the message must carry.
		file  string
		names []string
	}{
		{
			name: "a linter entry naming a function and no uri is refused",
			files: map[string]string{
				".aggrega-config.yaml": "workspace: {}\nplugins:\n  linter:\n    - function: custom_lint\n",
			},
			refusedBy: workspaceconfig.ErrConfigInvalid(),
			file:      ".aggrega-config.yaml",
			names:     []string{"plugins.linter[0]", `"custom_lint"`, "`uri`"},
		},
		{
			name: "the entry in a nearer linter_append is refused naming that file and its position",
			files: map[string]string{
				".aggrega-config.yaml":     "workspace: {}\nplugins:\n  linter:\n    - uri: linter/publishing\n",
				"svc/.aggrega-config.yaml": "plugins:\n  linter_append:\n    - uri: linter/extra\n    - function: lint_custom\n",
			},
			start:     "svc",
			refusedBy: workspaceconfig.ErrConfigInvalid(),
			file:      "svc/.aggrega-config.yaml",
			names:     []string{"plugins.linter_append[1]", `"lint_custom"`},
		},
		{
			name: "the entry in the CI-owned overlay is refused",
			files: map[string]string{
				".aggrega-config.yaml":                 "workspace: {}\n",
				workspaceconfig.AppendCIConfigFilename: "plugins:\n  linter_append:\n    - function: custom_lint\n",
			},
			refusedBy: workspaceconfig.ErrConfigInvalid(),
			file:      workspaceconfig.AppendCIConfigFilename,
			names:     []string{"plugins.linter_append[0]", `"custom_lint"`},
		},
		{
			name: "the entry in the global file is refused",
			files: map[string]string{
				"global/aggrega/config.yaml": "plugins:\n  linter:\n    - function: custom_lint\n",
				".aggrega-config.yaml":       "workspace: {}\n",
			},
			refusedBy: workspaceconfig.ErrConfigInvalid(),
			file:      "global/aggrega/config.yaml",
			names:     []string{"plugins.linter[0]", `"custom_lint"`},
		},
		{
			name: "a governance ir-verdict entry naming a function and no uri is refused",
			files: map[string]string{
				".aggrega-config.yaml": "workspace: {}\nplugins:\n  governance:\n    - uri: governance/redact\n      role: label-transform\n    - role: ir-verdict\n      function: verdict_custom\n",
			},
			refusedBy: workspaceconfig.ErrConfigInvalid(),
			file:      ".aggrega-config.yaml",
			names:     []string{"plugins.governance[1]", `"verdict_custom"`, "`uri`"},
		},
		{
			name: "a governance render-verdict entry in governance_append is refused",
			files: map[string]string{
				".aggrega-config.yaml": "workspace: {}\nplugins:\n  governance_append:\n    - role: render-verdict\n      function: verdict_custom\n",
			},
			refusedBy: workspaceconfig.ErrConfigInvalid(),
			file:      ".aggrega-config.yaml",
			names:     []string{"plugins.governance_append[0]", `"verdict_custom"`},
		},
		{
			name: "a capability-bound linter entry stays under the cross-cutting socket refusal",
			files: map[string]string{
				".aggrega-config.yaml": "workspace: {}\nplugins:\n  linter:\n    - capability: lint\n      function: custom_lint\n",
			},
			refusedBy: workspaceconfig.ErrCapabilityOnCrossCuttingSocket(),
			names:     []string{`"lint"`},
		},
		{
			name: "a governance transform-role entry naming a function and no uri is not judged by this check",
			files: map[string]string{
				".aggrega-config.yaml": "workspace: {}\nplugins:\n  governance:\n    - role: label-transform\n      function: custom_mutate\n",
			},
		},
		{
			name: "control: a linter entry naming a uri only loads",
			files: map[string]string{
				".aggrega-config.yaml": "workspace: {}\nplugins:\n  linter:\n    - uri: linter/publishing:1.0.0\n",
			},
		},
		{
			name: "control: a linter entry naming a uri and a function loads",
			files: map[string]string{
				".aggrega-config.yaml": "workspace: {}\nplugins:\n  linter:\n    - uri: linter/publishing:1.0.0\n      function: custom_lint\n",
			},
		},
		{
			name: "control: a governance verdict entry naming a uri and a function loads",
			files: map[string]string{
				".aggrega-config.yaml": "workspace: {}\nplugins:\n  governance:\n    - uri: governance/redact\n      role: ir-verdict\n      function: verdict_custom\n",
			},
		},
		{
			name: "control: a configuration with no attachments loads",
			files: map[string]string{
				".aggrega-config.yaml": "workspace: {}\n",
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
			if tc.refusedBy == nil {
				if err != nil {
					t.Fatalf("Resolve refused a configuration this check does not refuse: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Resolve accepted the attachment; expected a loud halt")
			}
			if !errors.Is(err, tc.refusedBy) {
				t.Fatalf("halt must be %v; got: %v", tc.refusedBy, err)
			}
			wants := tc.names
			if tc.file != "" {
				// The file is matched by its path beneath the root rather than in full,
				// so the assertion does not depend on how the temporary root is spelled.
				wants = append([]string{filepath.FromSlash(tc.file)}, wants...)
			}
			for _, want := range wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal must name %q; got: %v", want, err)
				}
			}
		})
	}
}
