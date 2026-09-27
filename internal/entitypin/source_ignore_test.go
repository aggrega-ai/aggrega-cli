// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package entitypin

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestThePinStepRecordsItsRulesAndTheHoldAppliesThem holds the pin step to the
// split every identity-bearing site rests on: a plan for writing asks the serving
// plugin for its rules, composes them with the author's .aggregaignore, pins the
// source under them and records both the plugin and the rules; a plan computed
// only to be checked asks no plugin and applies what the lock records.
//
// The second half plants the fault that split exists to prevent: the plugin the
// workspace would serve now declares other rules. A hold that composed its rules
// afresh would ask that plugin — the call count says so — and would then find the
// lock's pins stale; the hold here asks nothing and finds the lock in order.
func TestThePinStepRecordsItsRulesAndTheHoldAppliesThem(t *testing.T) {
	ws := t.TempDir()
	cat := filepath.Join(ws, "catalog")
	dir := filepath.Join(cat, "local")
	recipe := filepath.Join(dir, "recipe.yaml")
	writeFile(t, recipe, recipeYAML("local", "  source: ./chart\n", false))
	writeFile(t, filepath.Join(dir, "chart", "Chart.yaml"), "apiVersion: v2\nname: local\nversion: 1.0.0\n")
	writeFile(t, filepath.Join(dir, "chart", ".git", "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(dir, "chart", "notes.bak"), "old\n")
	writeFile(t, filepath.Join(dir, ".aggregaignore"), "*.bak\n")

	served := catalog.ServingProvider{Ref: "provider/example:1.0.0",
		Digest: "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		Ignore: &types.PluginSourceIgnore{Patterns: []string{".git/"}}}
	asked := 0
	serving := func(capability string) (catalog.ServingProvider, bool, error) {
		asked++
		return served, true, nil
	}
	plan := func(forWriting bool) Plan {
		t.Helper()
		p, err := Compute(PlanInputs{
			View:            catalog.NewLazyLookup(ws, "", []string{cat}, nil),
			URIs:            []string{"helm/local:1.0.0"},
			Resolver:        &recordingResolver{},
			WorkspaceRoot:   ws,
			ForWriting:      forWriting,
			ServingProvider: serving,
		})
		if err != nil {
			t.Fatalf("Compute: %v", err)
		}
		return p
	}

	if _, err := plan(true).Write(); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if asked != 1 {
		t.Fatalf("a plan for writing asked the serving plugin %d times; want once", asked)
	}
	lock, present, err := recipepin.ReadLock(recipe)
	if err != nil || !present {
		t.Fatalf("ReadLock: present %v, %v", present, err)
	}
	var pinned []string
	for _, p := range lock.LocalSourceDigests {
		pinned = append(pinned, p.Path)
	}
	if want := []string{"chart/Chart.yaml"}; !reflect.DeepEqual(pinned, want) {
		t.Errorf("pinned %q; want %q — the plugin's .git/ and the author's *.bak leave the rest out", pinned, want)
	}
	wantIgnore := &types.IgnoreRules{
		Plugin: &types.IgnorePlugin{Ref: served.Ref, Digest: served.Digest, Patterns: []string{".git/"}},
		Rules: []types.IgnoreRule{
			{Pattern: ".git/", Within: "chart", From: types.IgnoreRuleFromPlugin},
			{Pattern: "*.bak", Within: ".", From: ".aggregaignore"},
		},
	}
	if !reflect.DeepEqual(lock.Ignore, wantIgnore) {
		t.Errorf("recorded ignore section = %+v; want %+v", lock.Ignore, wantIgnore)
	}

	// The workspace's plugin changes its mind; the lock is not re-pinned.
	served.Ignore = &types.PluginSourceIgnore{Patterns: []string{"only-this/"}}
	asked = 0
	if err := plan(false).Unpinned(testRemedy); err != nil {
		t.Fatalf("the hold refused a lock the step had just written: %v", err)
	}
	if asked != 0 {
		t.Errorf("a plan only checked asked the serving plugin %d times; it must apply the recorded rules alone", asked)
	}
}
