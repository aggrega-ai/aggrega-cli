// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestTheIgnoreAdvisoryNamesBothPluginsWhereTheirRulesDiffer drives the pull's
// advisory over the locks it can meet beside a Recipe whose local source is a
// directory: no ignore section, the serving plugin's own rules, and a record of
// another plugin's. Only the last advises, and it names both plugins; it never
// halts. The serving plugin is the binary's own helm provider, so its rules are
// read from the resolution under test rather than written here: a row built from
// them agrees by construction whatever that provider declares.
//
// An advisory that compared nothing would stay silent in the third row; one that
// read an absent section as an empty record would speak in the first.
func TestTheIgnoreAdvisoryNamesBothPluginsWhereTheirRulesDiffer(t *testing.T) {
	ws := t.TempDir()
	cat := filepath.Join(ws, "catalog")
	dir := filepath.Join(cat, "recipe", "helm", "demo", "1.0.0")
	doc := filepath.Join(dir, "recipe.yaml")
	for rel, body := range map[string]string{
		"recipe.yaml": "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: demo\n  version: 1.0.0\n" +
			"spec:\n  capability: helm\n  implements: core/demo:1.0.0\n  source: ./chart\n",
		"chart/Chart.yaml": "apiVersion: v2\nname: demo\nversion: 0.1.0\n",
	} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	view := catalog.NewLazyLookup(ws, "", []string{cat}, nil)
	served, ok, err := view.ServingProvider("helm", nil)
	if err != nil {
		t.Fatalf("ServingProvider: %v", err)
	}
	if !ok {
		t.Skip("this binary carries no provider serving helm, so there is no plugin to compare with")
	}
	var own types.IgnorePlugin
	own.Ref, own.Digest = served.Ref, served.Digest
	if served.Ignore != nil {
		own.Patterns, own.Files = served.Ignore.Patterns, served.Ignore.Files
	}
	other := types.IgnorePlugin{Ref: "provider/other-helm:1.0.0", Digest: served.Digest, Patterns: []string{"only-this/"}}

	rows := []struct {
		name   string
		ignore *types.IgnoreRules
		advise bool
	}{
		{name: "a lock recording no ignore section made no choice and is not compared", ignore: nil},
		{name: "a lock recording the serving plugin's own rules agrees", ignore: &types.IgnoreRules{Plugin: &own}},
		{name: "a lock recording another plugin's rules is advised on", ignore: &types.IgnoreRules{Plugin: &other}, advise: true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			pins := recipepin.Pins{LocalSourceDigests: []types.LocalSourceDigest{{Path: "chart/Chart.yaml",
				Digest: "sha256:0000000000000000000000000000000000000000000000000000000000000000"}}, Ignore: row.ignore}
			if _, err := recipepin.WriteLock(doc, pins); err != nil {
				t.Fatalf("WriteLock: %v", err)
			}
			line, differ := ignoreRuleDifference(doc, "Recipe", "demo", "helm", true, view, nil)
			if differ != row.advise {
				t.Fatalf("advised = %v (%q); want %v", differ, line, row.advise)
			}
			if !row.advise {
				return
			}
			for _, want := range []string{"warning:", other.Ref, served.Ref, "only-this/", "aggrega pin"} {
				if !strings.Contains(line, want) {
					t.Errorf("the advisory does not name %q: %s", want, line)
				}
			}
		})
	}
}
