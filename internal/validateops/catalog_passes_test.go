// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validateops

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// passProbeFeature is a Feature whole-catalog validation accepts; with extends
// naming an identity no catalog holds it is one the extends gate refuses.
func passProbeFeature(extends string) string {
	doc := `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/secret-value
  version: 1.0.0
  description: A single secret value delivered to the attached component.
  labels:
    feature.aggrega.ai/kind: secret
spec:
`
	if extends != "" {
		doc += "  extends: " + extends + "\n"
	}
	return doc + `  schema:
    values:
      key:
        type: string
        required: true
        description: Environment variable name the credential is delivered as.
`
}

// passProbeUnloadable is an Aggrega document whose YAML does not parse, which
// halts a whole-catalog pass over the directory holding it before anything loads.
const passProbeUnloadable = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/compute/broken
  version: "1.0.0"
spec:
  schema: values: [
`

// TestValidateCatalogPassesWalksEveryPassInOrder drives the one walk of a
// workspace's catalog passes over one pass of each shape — a clean catalog, a
// pass whose pins are refused, a catalog that cannot be loaded, a catalog with a
// finding, a pass for named Releases reading nothing, the conventional catalog
// directory as a link leading out of the workspace both named and unnamed, the
// single pass of a workspace naming no Release over no directory, and a clean
// catalog last — and holds each outcome to its pass: one outcome per pass in pass
// order, a pins refusal and a load failure each reported on its own pass while
// every later pass still runs, a pass for named Releases reading nothing checked
// for nothing with its pins never asked, the single workspace pass checked
// whatever it reads, and the part of a pass's directories somebody named reaching
// the validation (the named link is followed, the unnamed one refused).
//
// It also holds the walk to handing each outcome over as soon as it is decided:
// the pins of a pass are never asked before the outcome of the pass ahead of it
// has been handed over.
//
// The last clean pass is the positive control for "later passes still run": it
// sits behind every failure and must still validate.
func TestValidateCatalogPassesWalksEveryPassInOrder(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "feature"), 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "feature", "secret-value.yaml"), []byte(passProbeFeature("")), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	linked := filepath.Join(ws, "catalog")
	if err := os.Symlink(outside, linked); err != nil {
		t.Fatalf("link the conventional catalog directory out of the workspace: %v", err)
	}
	put := func(rel, body string) string {
		t.Helper()
		p := filepath.Join(ws, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
		return filepath.Dir(filepath.Dir(p))
	}
	clean := put("clean/feature/secret-value.yaml", passProbeFeature(""))
	broken := put("broken/feature/secret-value.yaml", passProbeFeature("core/no-such-parent"))
	unloadable := put("unloadable/component/broken.yaml", passProbeUnloadable)
	const cacheDir = ".aggrega/cache"

	pinsRefusal := errors.New("probe: the lockfile of this pass's Release cannot be read")

	rows := []struct {
		name string
		pass workspaceconfig.CatalogPass
		// checked, pinsErr, loadErr, valid and extends are what the outcome must
		// carry; loadNames is a text the load failure must name.
		checked   bool
		pinsErr   bool
		loadErr   bool
		loadNames string
		valid     bool
		extends   int
	}{
		{name: "a clean catalog", pass: workspaceconfig.CatalogPass{Releases: []string{"release-clean.yaml"}, Dirs: []string{clean}},
			checked: true, valid: true},
		{name: "a pass whose pins are refused", pass: workspaceconfig.CatalogPass{Releases: []string{"release-pins.yaml"}, Dirs: []string{clean}},
			checked: true, pinsErr: true},
		{name: "a catalog that cannot be loaded", pass: workspaceconfig.CatalogPass{Releases: []string{"release-unloadable.yaml"}, Dirs: []string{unloadable}},
			checked: true, loadErr: true, loadNames: filepath.Join("component", "broken.yaml")},
		{name: "a catalog with an extends finding", pass: workspaceconfig.CatalogPass{Releases: []string{"release-broken.yaml"}, Dirs: []string{broken}},
			checked: true, extends: 1},
		{name: "a pass for named Releases reading nothing", pass: workspaceconfig.CatalogPass{Releases: []string{"release-off.yaml"}}},
		{name: "the conventional directory, a link out of the workspace, named", pass: workspaceconfig.CatalogPass{Releases: []string{"release-named.yaml"}, Dirs: []string{linked}, Named: []string{linked}},
			checked: true, valid: true},
		{name: "the same directory, unnamed", pass: workspaceconfig.CatalogPass{Releases: []string{"release-unnamed.yaml"}, Dirs: []string{linked}},
			checked: true, loadErr: true, loadNames: linked},
		{name: "the single pass of a workspace naming no Release, over no directory", pass: workspaceconfig.CatalogPass{},
			checked: true, valid: true},
		{name: "a clean catalog after every failure", pass: workspaceconfig.CatalogPass{Releases: []string{"release-last.yaml", "release-also.yaml"}, Dirs: []string{clean}},
			checked: true, valid: true},
	}
	passes := make([]workspaceconfig.CatalogPass, 0, len(rows))
	for _, r := range rows {
		passes = append(passes, r.pass)
	}
	key := func(pass workspaceconfig.CatalogPass) string {
		if !pass.NamesReleases() {
			return "(workspace)"
		}
		return strings.Join(pass.Releases, ",")
	}

	// events records, in order, each pins question the walk asks and each outcome
	// it hands over.
	var events []string
	pinsFor := func(pass workspaceconfig.CatalogPass) (catalog.Pins, error) {
		events = append(events, "pins "+key(pass))
		if slices.Contains(pass.Releases, "release-pins.yaml") {
			return nil, pinsRefusal
		}
		return catalog.Pins{}, nil
	}
	var handed []CatalogPassOutcome
	each := func(outcome CatalogPassOutcome) {
		events = append(events, "outcome "+key(outcome.Pass))
		handed = append(handed, outcome)
	}

	var outcomes []CatalogPassOutcome
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("the walk panicked: %v", r)
			}
		}()
		outcomes = ValidateCatalogPasses(t.Context(), ws, cacheDir, passes, pinsFor, each)
	}()

	if len(outcomes) != len(rows) {
		t.Fatalf("the walk returned %d outcome(s) for %d pass(es)", len(outcomes), len(rows))
	}
	for i, r := range rows {
		got := outcomes[i]
		t.Run(r.name, func(t *testing.T) {
			if !slices.Equal(got.Pass.Releases, r.pass.Releases) || !slices.Equal(got.Pass.Dirs, r.pass.Dirs) ||
				!slices.Equal(got.Pass.Named, r.pass.Named) {
				t.Errorf("outcome %d is for the pass of %v over %v named %v; want the pass of %v over %v named %v",
					i, got.Pass.Releases, got.Pass.Dirs, got.Pass.Named, r.pass.Releases, r.pass.Dirs, r.pass.Named)
			}
			if got.Checked != r.checked {
				t.Errorf("checked = %v; want %v", got.Checked, r.checked)
			}
			if r.pinsErr != (got.PinsErr != nil) || (r.pinsErr && !errors.Is(got.PinsErr, pinsRefusal)) {
				t.Errorf("pins error = %v; want the pins refusal: %v", got.PinsErr, r.pinsErr)
			}
			if r.loadErr != (got.LoadErr != nil) {
				t.Errorf("load error = %v; want one: %v", got.LoadErr, r.loadErr)
			}
			if r.loadNames != "" && got.LoadErr != nil && !strings.Contains(got.LoadErr.Error(), r.loadNames) {
				t.Errorf("the load failure does not name %s: %v", r.loadNames, got.LoadErr)
			}
			if got.Result.Valid != r.valid {
				t.Errorf("result valid = %v; want %v (result %+v)", got.Result.Valid, r.valid, got.Result)
			}
			if len(got.Result.ExtendsErrs) != r.extends {
				t.Errorf("extends findings = %v; want %d", got.Result.ExtendsErrs, r.extends)
			}
			if r.extends > 0 && !strings.Contains(got.Result.ExtendsErrs[0].Error(), "core/no-such-parent") {
				t.Errorf("the extends finding does not name the missing parent: %v", got.Result.ExtendsErrs[0])
			}
		})
	}

	// Every checked pass has its pins asked, and no other; each outcome is handed
	// over before the next pass's pins are asked.
	var wantEvents []string
	for _, r := range rows {
		if r.checked {
			wantEvents = append(wantEvents, "pins "+key(r.pass))
		}
		wantEvents = append(wantEvents, "outcome "+key(r.pass))
	}
	if !slices.Equal(events, wantEvents) {
		t.Errorf("the walk ran as\n  %v\nwant\n  %v", events, wantEvents)
	}
	if len(handed) != len(outcomes) {
		t.Fatalf("the walk handed over %d outcome(s) and returned %d", len(handed), len(outcomes))
	}
	for i := range handed {
		if handed[i].Checked != outcomes[i].Checked || key(handed[i].Pass) != key(outcomes[i].Pass) ||
			handed[i].PinsErr != outcomes[i].PinsErr || handed[i].LoadErr != outcomes[i].LoadErr ||
			handed[i].Result.Valid != outcomes[i].Result.Valid {
			t.Errorf("outcome %d was handed over as %+v and returned as %+v", i, handed[i], outcomes[i])
		}
	}
}

// TestValidateCatalogPassesOverNoPassReturnsNoOutcome is the empty edge: no pass,
// no outcome, and the pins never asked.
func TestValidateCatalogPassesOverNoPassReturnsNoOutcome(t *testing.T) {
	t.Parallel()
	pinsFor := func(workspaceconfig.CatalogPass) (catalog.Pins, error) {
		t.Errorf("the pins were asked for with no pass to run")
		return nil, nil
	}
	each := func(CatalogPassOutcome) {
		t.Errorf("an outcome was handed over with no pass to run")
	}
	if got := ValidateCatalogPasses(t.Context(), t.TempDir(), ".aggrega/cache", nil, pinsFor, each); len(got) != 0 {
		t.Errorf("the walk over no pass returned %d outcome(s): %+v", len(got), got)
	}
}
