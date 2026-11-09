// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// terraformModuleRecipe writes a Terraform Recipe whose local source is a module
// directory beside it, and returns the path of its document.
func terraformModuleRecipe(t *testing.T, dir string) string {
	t.Helper()
	for rel, body := range map[string]string{
		"recipe.yaml": "apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: demo\n  version: 1.0.0\n" +
			"spec:\n  capability: terraform\n  implements: core/demo:1.0.0\n  source: ./module\n",
		"module/main.tf":           "variable \"name\" {}\n",
		"module/.terraform/cached": "a working directory the module plugin leaves out\n",
	} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "recipe.yaml")
}

// terraformAt is a catalog holding one provider plugin, provider/terraform at
// version, serving the terraform capability and declaring the module plugin's
// ignore rules. Two such catalogs at different versions stand for two releases
// of the binary whose built-in plugin declares the same rules.
func terraformAt(version string) *Catalog {
	return &Catalog{Plugins: map[string]types.Plugin{
		"provider/terraform:" + version: {
			APIVersion: "aggrega.ai/v1beta1",
			Kind:       "Plugin",
			Metadata:   types.PluginMetadata{Name: "terraform", Version: version, PluginType: "provider"},
			Spec: types.PluginSpec{
				AbiVersion: "v1alpha1",
				Provider: types.PluginProvider{
					Capabilities: []string{"terraform"},
					Contract: types.PluginSpecProviderContract{SourceIgnore: &types.PluginSourceIgnore{
						Patterns: []string{".git/", ".terraform/"},
						Files:    []string{".terraformignore"},
					}},
				},
			},
		},
	}}
}

// pinnedLock composes the ignore section for doc from the plugin cat serves for
// terraform, as the pin step does, and returns the lock bytes the step would
// write beside doc.
func pinnedLock(t *testing.T, cat *Catalog, doc string) (ServingProvider, []byte) {
	t.Helper()
	served, ok, err := cat.servingProvider("terraform", nil)
	if err != nil || !ok {
		t.Fatalf("servingProvider: ok %v, %v", ok, err)
	}
	ignore, err := ComposeIgnore(doc, func() (*ServingProvider, error) { return &served, nil })
	if err != nil {
		t.Fatalf("ComposeIgnore: %v", err)
	}
	out, _, err := recipepin.PreviewLock(doc, recipepin.Pins{Ignore: ignore})
	if err != nil {
		t.Fatalf("PreviewLock: %v", err)
	}
	return served, out
}

// TestThePinRecordsTheServingPluginByItsNameAlone holds the plugin block the pin
// step writes for a Terraform Recipe with a module directory: it names the plugin
// as `provider/terraform` and carries neither its version nor a digest, while the
// serving plugin itself is still known by its full reference for a reader to be
// shown. A step recording the reference, or a digest of the plugin's entity,
// would put the version back into the lock.
func TestThePinRecordsTheServingPluginByItsNameAlone(t *testing.T) {
	doc := terraformModuleRecipe(t, t.TempDir())
	served, lock := pinnedLock(t, terraformAt("1.2.3"), doc)
	if served.Name != "provider/terraform" || served.Ref != "provider/terraform:1.2.3" {
		t.Fatalf("serving plugin name %q, reference %q; want provider/terraform and provider/terraform:1.2.3",
			served.Name, served.Ref)
	}
	text := string(lock)
	if !strings.Contains(text, "\n  plugin:\n    name: provider/terraform\n") {
		t.Errorf("the lock does not name the plugin as provider/terraform:\n%s", text)
	}
	for _, absent := range []string{"1.2.3", "ref:", "digest:"} {
		if strings.Contains(text, absent) {
			t.Errorf("the lock carries %q, which names a release of the plugin:\n%s", absent, text)
		}
	}
	if _, err := recipepin.DecodeLock(lock, recipepin.LockPath(doc)); err != nil {
		t.Errorf("the lock the step writes does not read back: %v", err)
	}
}

// TestALockPinnedUnderTwoReleasesOfOnePluginIsByteIdentical pins one Recipe under
// two releases of the same plugin declaring the same rules — the shape of pinning
// again with a newer aggrega — and holds the two locks to the same bytes. Only the
// rules decide what a reading of the directory keeps, so a lock that moved here
// would be churn no reader could act on.
func TestALockPinnedUnderTwoReleasesOfOnePluginIsByteIdentical(t *testing.T) {
	doc := terraformModuleRecipe(t, t.TempDir())
	_, older := pinnedLock(t, terraformAt("1.0.0"), doc)
	_, newer := pinnedLock(t, terraformAt("2.0.0-7-gabcdef0"), doc)
	if !bytes.Equal(older, newer) {
		t.Fatalf("the lock moved between two releases of one plugin declaring the same rules:\n--- 1.0.0\n%s\n--- 2.0.0-7-gabcdef0\n%s",
			older, newer)
	}
	if !bytes.Contains(older, []byte("pattern: .terraform/")) {
		t.Errorf("the lock does not record the plugin's rules:\n%s", older)
	}
}
