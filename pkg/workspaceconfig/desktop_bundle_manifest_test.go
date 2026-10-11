// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// desktopBundleManifest is the installable desktop bundle's manifest, from this
// package's directory. The bundle hands the server the folder its user chose
// through the workspace-root variable, and the manifest is JSON, so it spells
// that variable's name as text rather than naming the constant this package
// declares.
const desktopBundleManifest = "../../extensions/mcpb/manifest.json"

// userConfigPlaceholder is the whole of a value a client fills from one of the
// bundle's settings: the setting's key inside the format's placeholder.
var userConfigPlaceholder = regexp.MustCompile(`^\$\{user_config\.([^}]+)\}$`)

// workspaceRootLaunchComplaints reports every way a bundle manifest's launch
// fails to hand the server a required setting through the variable the binary
// reads its workspace root from, one sentence each, or none where it does.
//
// Three things have to hold together, and each fails on its own: the launch's
// environment carries the variable under exactly the spelling the binary reads
// (workspaceconfig.EnvWorkspaceRoot); its value is one placeholder naming a
// setting the manifest declares, since a placeholder naming none reaches the
// server as its own text; and that setting is required, since a client that
// let it be left empty would launch the server with nothing in the variable —
// which the server reads as no root at all and answers from whatever folder the
// client started it in.
func workspaceRootLaunchComplaints(manifest []byte) []string {
	var doc struct {
		Server struct {
			MCPConfig struct {
				Env map[string]string `json:"env"`
			} `json:"mcp_config"`
		} `json:"server"`
		UserConfig map[string]struct {
			Required *bool `json:"required"`
		} `json:"user_config"`
	}
	if err := json.Unmarshal(manifest, &doc); err != nil {
		return []string{fmt.Sprintf("the manifest does not decode in the shape this check reads: %v", err)}
	}

	value, ok := doc.Server.MCPConfig.Env[workspaceconfig.EnvWorkspaceRoot]
	if !ok {
		carried := make([]string, 0, len(doc.Server.MCPConfig.Env))
		for name := range doc.Server.MCPConfig.Env {
			carried = append(carried, name)
		}
		slices.Sort(carried)
		return []string{fmt.Sprintf("server.mcp_config.env carries no %s, the variable the binary reads its "+
			"workspace root from; it carries %q", workspaceconfig.EnvWorkspaceRoot, carried)}
	}
	m := userConfigPlaceholder.FindStringSubmatch(value)
	if m == nil {
		return []string{fmt.Sprintf("server.mcp_config.env.%s is %q, which is not one ${user_config.KEY} "+
			"placeholder naming the setting the user chooses", workspaceconfig.EnvWorkspaceRoot, value)}
	}
	key := m[1]
	setting, declared := doc.UserConfig[key]
	if !declared {
		return []string{fmt.Sprintf("server.mcp_config.env.%s names user_config.%s, which user_config does "+
			"not declare, so the placeholder reaches the server as its own text", workspaceconfig.EnvWorkspaceRoot, key)}
	}
	if setting.Required == nil || !*setting.Required {
		return []string{fmt.Sprintf("user_config.%s, which server.mcp_config.env.%s names, is not required, so "+
			"a client may launch the server with the variable empty", key, workspaceconfig.EnvWorkspaceRoot)}
	}
	return nil
}

// TestTheDesktopBundleHandsItsRequiredWorkspaceFolderToTheVariableTheBinaryReads
// holds the installable bundle's manifest to the binary across the language
// boundary between them: the manifest, read from the repository, must pass the
// folder its user chooses through workspaceconfig.EnvWorkspaceRoot, from a
// setting it declares and requires.
//
// The rows after the first are that same manifest with one fault planted in it
// each, and every one must be named: the variable renamed, the setting renamed
// out from under the placeholder, and the setting no longer required, dropped
// or set false. A check that read nothing would pass the first row and fail
// every other, so the rows are what show it reads the three things it claims.
//
// 🔴 PLANTED DEFECT, for the file itself: rename the env key in
// extensions/mcpb/manifest.json, rename its workspace_root setting, or drop that
// setting's "required", and the first row fails naming the fault.
func TestTheDesktopBundleHandsItsRequiredWorkspaceFolderToTheVariableTheBinaryReads(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(desktopBundleManifest)
	if err != nil {
		t.Fatalf("read the bundle manifest: %v", err)
	}

	for _, tc := range []struct {
		name  string
		plant func(t *testing.T, doc map[string]any)
		// want is the phrase the one complaint must carry; empty wants none.
		want string
	}{
		{name: "the manifest as it is in the repository"},
		{
			name: "the variable renamed",
			plant: func(t *testing.T, doc map[string]any) {
				env := objectAt(t, doc, "server", "mcp_config", "env")
				env[workspaceconfig.EnvWorkspaceRoot+"_DIR"] = env[workspaceconfig.EnvWorkspaceRoot]
				delete(env, workspaceconfig.EnvWorkspaceRoot)
			},
			want: "carries no " + workspaceconfig.EnvWorkspaceRoot,
		},
		{
			name: "the setting renamed out from under the placeholder",
			plant: func(t *testing.T, doc map[string]any) {
				settings := objectAt(t, doc, "user_config")
				key := placeholderKey(t, doc)
				settings[key+"_folder"] = settings[key]
				delete(settings, key)
			},
			want: "which user_config does not declare",
		},
		{
			name: "the setting no longer required",
			plant: func(t *testing.T, doc map[string]any) {
				delete(objectAt(t, doc, "user_config", placeholderKey(t, doc)), "required")
			},
			want: "is not required",
		},
		{
			name: "the setting declared optional",
			plant: func(t *testing.T, doc map[string]any) {
				objectAt(t, doc, "user_config", placeholderKey(t, doc))["required"] = false
			},
			want: "is not required",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			manifest := raw
			if tc.plant != nil {
				var doc map[string]any
				if err := json.Unmarshal(raw, &doc); err != nil {
					t.Fatalf("decode the bundle manifest: %v", err)
				}
				tc.plant(t, doc)
				planted, err := json.Marshal(doc)
				if err != nil {
					t.Fatalf("encode the planted manifest: %v", err)
				}
				manifest = planted
			}
			got := workspaceRootLaunchComplaints(manifest)
			if tc.want == "" {
				for _, complaint := range got {
					t.Errorf("%s: %s", desktopBundleManifest, complaint)
				}
				return
			}
			if len(got) != 1 || !strings.Contains(got[0], tc.want) {
				t.Errorf("the planted fault was reported as %q; want one complaint carrying %q", got, tc.want)
			}
		})
	}
}

// objectAt returns the JSON object at path inside doc, failing where the
// manifest holds no object there: a plant that could not be made would leave a
// row asserting over the unplanted manifest.
func objectAt(t *testing.T, doc map[string]any, path ...string) map[string]any {
	t.Helper()
	cur := doc
	for _, key := range path {
		next, ok := cur[key].(map[string]any)
		if !ok {
			t.Fatalf("the bundle manifest holds no object at %s", strings.Join(path, "."))
		}
		cur = next
	}
	return cur
}

// placeholderKey is the setting the manifest's workspace-root variable names,
// read from the manifest rather than spelled here, so a plant moves the setting
// the manifest actually uses.
func placeholderKey(t *testing.T, doc map[string]any) string {
	t.Helper()
	value, _ := objectAt(t, doc, "server", "mcp_config", "env")[workspaceconfig.EnvWorkspaceRoot].(string)
	m := userConfigPlaceholder.FindStringSubmatch(value)
	if m == nil {
		t.Fatalf("the bundle manifest's %s is %q, which names no setting to plant a fault in",
			workspaceconfig.EnvWorkspaceRoot, value)
	}
	return m[1]
}
