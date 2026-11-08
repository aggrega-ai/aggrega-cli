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
// fails to hand the server its workspace-folder setting through the variable the
// binary reads its workspace root from, one sentence each, or none where it does.
//
// These have to hold together, and each fails on its own: the launch's
// environment carries the variable under exactly the spelling the binary reads
// (workspaceconfig.EnvWorkspaceRoot); its value is one placeholder naming a
// setting the manifest declares, since a placeholder naming none reaches the
// server as its own text — the precondition for reading the setting at all, so it
// deliberately overlaps the bundle build script's wider check of every
// placeholder; that setting is not required, since the format's
// reference resolver builds no launch at all while a required setting is empty,
// so the server's refusal naming the empty setting would never be served; that
// setting defaults to the empty string, since a setting with no default and no
// value leaves its placeholder in the variable as its own text, which the server
// refuses as a folder that is not there rather than naming the empty setting,
// while an empty default hands an unset setting over exactly as an emptied one;
// and that setting carries the title the binary's refusals name it by
// (workspaceconfig.DesktopWorkspaceFolderSetting), since those refusals tell the
// person reading them which setting to change.
//
// The resolver's handling of required and default settings is the one the
// packer version scripts/build-mcp-bundle.sh pins publishes, read at
// @anthropic-ai/mcpb 2.1.2; a raise of that pin re-reads it.
func workspaceRootLaunchComplaints(manifest []byte) []string {
	var doc struct {
		Server struct {
			MCPConfig struct {
				Env map[string]string `json:"env"`
			} `json:"mcp_config"`
		} `json:"server"`
		UserConfig map[string]struct {
			Required *bool `json:"required"`
			// Default stays raw, so an absent default (nil) reads apart from an
			// empty string and from a value of another type.
			Default json.RawMessage `json:"default"`
			Title   string          `json:"title"`
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
	var complaints []string
	if setting.Required != nil && *setting.Required {
		complaints = append(complaints, fmt.Sprintf("user_config.%s, which server.mcp_config.env.%s names, is "+
			"required, and the format's reference resolver builds no launch while a required setting is empty, so "+
			"the server's refusal naming the empty setting is never served", key, workspaceconfig.EnvWorkspaceRoot))
	}
	var defaultValue any
	switch {
	case setting.Default == nil:
		complaints = append(complaints, fmt.Sprintf("user_config.%s, which server.mcp_config.env.%s names, declares "+
			"no default, so a client holding no value for it leaves ${user_config.%s} in the variable as its own "+
			"text, which the server refuses as a folder that is not there rather than naming the empty setting",
			key, workspaceconfig.EnvWorkspaceRoot, key))
	case json.Unmarshal(setting.Default, &defaultValue) != nil || defaultValue != "":
		complaints = append(complaints, fmt.Sprintf("user_config.%s, which server.mcp_config.env.%s names, defaults "+
			"to %s rather than the empty string, so a setting left unset hands the server that value instead of the "+
			"empty variable the server refuses naming the setting", key, workspaceconfig.EnvWorkspaceRoot,
			setting.Default))
	}
	if setting.Title != workspaceconfig.DesktopWorkspaceFolderSetting {
		complaints = append(complaints, fmt.Sprintf("user_config.%s, which server.mcp_config.env.%s names, is titled "+
			"%q, and the binary's refusal of a root that variable supplied names the setting %q", key,
			workspaceconfig.EnvWorkspaceRoot, setting.Title, workspaceconfig.DesktopWorkspaceFolderSetting))
	}
	return complaints
}

// TestTheDesktopBundleHandsItsWorkspaceFolderToTheVariableTheBinaryReads holds
// the installable bundle's manifest to the binary across the language boundary
// between them: the manifest, read from the repository, must pass the folder its
// user chooses through workspaceconfig.EnvWorkspaceRoot, from a setting it
// declares, does not require and defaults to the empty string, titled as the
// binary's refusals name it (workspaceconfig.DesktopWorkspaceFolderSetting).
//
// The rows after the first are that same manifest with one fault planted in it
// each, and every one must be named: the variable renamed, the setting renamed
// out from under the placeholder, the setting required again, its default
// dropped, its default not empty, written as a string or as another type, and
// the setting retitled or left untitled. A check that read nothing would pass the
// first row and fail every other, so the rows are what show it reads each thing
// it claims.
//
// 🔴 PLANTED DEFECT, for the file itself: rename the env key in
// extensions/mcpb/manifest.json, rename its workspace_root setting, set that
// setting's "required" to true, drop its "default" or give it a non-empty one,
// or change its "title", and the first row fails naming the fault. 🔴 And for
// the binary's side: change DesktopWorkspaceFolderSetting, the title the
// refusals name the setting by, and the first row fails the same way.
func TestTheDesktopBundleHandsItsWorkspaceFolderToTheVariableTheBinaryReads(t *testing.T) {
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
			name: "the setting required again",
			plant: func(t *testing.T, doc map[string]any) {
				objectAt(t, doc, "user_config", placeholderKey(t, doc))["required"] = true
			},
			want: "is required",
		},
		{
			name: "the setting's default dropped",
			plant: func(t *testing.T, doc map[string]any) {
				delete(objectAt(t, doc, "user_config", placeholderKey(t, doc)), "default")
			},
			want: "declares no default",
		},
		{
			name: "the setting's default a folder rather than empty",
			plant: func(t *testing.T, doc map[string]any) {
				objectAt(t, doc, "user_config", placeholderKey(t, doc))["default"] = "workspace"
			},
			want: "rather than the empty string",
		},
		{
			name: "the setting's default null",
			plant: func(t *testing.T, doc map[string]any) {
				objectAt(t, doc, "user_config", placeholderKey(t, doc))["default"] = nil
			},
			want: "rather than the empty string",
		},
		{
			name: "the setting retitled",
			plant: func(t *testing.T, doc map[string]any) {
				objectAt(t, doc, "user_config", placeholderKey(t, doc))["title"] =
					workspaceconfig.DesktopWorkspaceFolderSetting + " path"
			},
			want: "is titled",
		},
		{
			name: "the setting left untitled",
			plant: func(t *testing.T, doc map[string]any) {
				delete(objectAt(t, doc, "user_config", placeholderKey(t, doc)), "title")
			},
			want: "is titled",
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

// bundleVersionLaunchComplaints reports every way a bundle manifest's launch
// fails to hand the server the bundle's own version through the variable the
// binary reads it from, one sentence each, or none where it does.
//
// Two things have to hold together. The launch's environment carries the
// variable under exactly the spelling the binary reads
// (workspaceconfig.EnvBundleVersion), since a server reading a variable nobody
// sets reports no bundle at all. And its value is the manifest's own version
// field: the bundle's build writes one stamped version into both, so the
// checked-in entry carries the placeholder the version field carries, and an
// entry holding anything else — a different literal, or a placeholder for a
// setting — is a bundle that would tell the server a version it is not.
func bundleVersionLaunchComplaints(manifest []byte) []string {
	var doc struct {
		Version string `json:"version"`
		Server  struct {
			MCPConfig struct {
				Env map[string]string `json:"env"`
			} `json:"mcp_config"`
		} `json:"server"`
	}
	if err := json.Unmarshal(manifest, &doc); err != nil {
		return []string{fmt.Sprintf("the manifest does not decode in the shape this check reads: %v", err)}
	}
	value, ok := doc.Server.MCPConfig.Env[workspaceconfig.EnvBundleVersion]
	if !ok {
		carried := make([]string, 0, len(doc.Server.MCPConfig.Env))
		for name := range doc.Server.MCPConfig.Env {
			carried = append(carried, name)
		}
		slices.Sort(carried)
		return []string{fmt.Sprintf("server.mcp_config.env carries no %s, the variable the binary reads the "+
			"bundle's own version from; it carries %q", workspaceconfig.EnvBundleVersion, carried)}
	}
	if value != doc.Version {
		return []string{fmt.Sprintf("server.mcp_config.env.%s is %q and the manifest's version is %q: the build "+
			"writes one stamped version into both, so the checked-in entry carries the placeholder the version "+
			"field carries", workspaceconfig.EnvBundleVersion, value, doc.Version)}
	}
	return nil
}

// TestTheDesktopBundleHandsTheServerItsOwnVersion holds the installable
// bundle's manifest to the binary across the language boundary between them,
// for the second variable the launch carries: the manifest, read from the
// repository, must hand the server workspaceconfig.EnvBundleVersion, holding the
// placeholder its version field holds, which the bundle's build replaces in both
// with one stamped version.
//
// The rows after the first are that same manifest with one fault planted in it
// each, and every one must be named: the variable misspelled, the variable
// dropped, the entry holding a version the version field does not, and the entry
// written as a placeholder for a setting. A check that read nothing would pass
// the first row and fail every other.
//
// 🔴 PLANTED DEFECT, for the file itself: misspell the AGGREGA_BUNDLE_VERSION key
// in extensions/mcpb/manifest.json, or change its value away from the version
// field's, and the first row fails naming the fault. 🔴 And for the binary's
// side: change EnvBundleVersion, the spelling the server reads, and the first row
// fails the same way.
func TestTheDesktopBundleHandsTheServerItsOwnVersion(t *testing.T) {
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
			name: "the variable misspelled",
			plant: func(t *testing.T, doc map[string]any) {
				env := objectAt(t, doc, "server", "mcp_config", "env")
				env["AGGREGA_BUNDLE_VERISON"] = env[workspaceconfig.EnvBundleVersion]
				delete(env, workspaceconfig.EnvBundleVersion)
			},
			want: "carries no " + workspaceconfig.EnvBundleVersion,
		},
		{
			name: "the variable dropped",
			plant: func(t *testing.T, doc map[string]any) {
				delete(objectAt(t, doc, "server", "mcp_config", "env"), workspaceconfig.EnvBundleVersion)
			},
			want: "carries no " + workspaceconfig.EnvBundleVersion,
		},
		{
			name: "the entry holding a version the version field does not",
			plant: func(t *testing.T, doc map[string]any) {
				objectAt(t, doc, "server", "mcp_config", "env")[workspaceconfig.EnvBundleVersion] = "0.0.0-synthetic"
			},
			want: "the build writes one stamped version into both",
		},
		{
			name: "the entry written as a placeholder for a setting",
			plant: func(t *testing.T, doc map[string]any) {
				objectAt(t, doc, "server", "mcp_config", "env")[workspaceconfig.EnvBundleVersion] =
					"${user_config.bundle_version}"
			},
			want: "the build writes one stamped version into both",
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
			got := bundleVersionLaunchComplaints(manifest)
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
