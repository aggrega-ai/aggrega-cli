// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/entitypin"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
)

// What this file proves.
//
// An entity of a kind that has a directory of its own is refused when another
// entity sits beside it, at the two moments something is produced from its
// directory — the pin step, reached from `aggrega pin`, `pull --pin-sources` and
// `push --pin-sources`, and a publication — and at no other. The refusal comes
// from one predicate, so every moment gives the same diagnostic, and the admitted
// half is driven beside it: a pull that does not pin, an entity alone in a
// directory named for nothing, and a workspace root holding Aggregates beside a
// Release and a Manifest.

// neighbourRecipe is a second Recipe authored beside the fixture's own, reading
// no remote source, so a moment that reached for the network before judging the
// directory would be caught by the fixture's source rather than by this one.
const neighbourRecipe = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: neighbour
  version: "1.0.0"
spec:
  capability: helm
  implements: neighbour
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: demo:1
`

// directoryRuleDiagnostic returns the refusal's own composed diagnostic from an
// error a command returned, beneath whatever frame that command put around it.
func directoryRuleDiagnostic(t *testing.T, moment string, err error) string {
	t.Helper()
	if !errors.Is(err, entitypin.ErrEntitySharesItsDirectory()) {
		t.Fatalf("%s must refuse under the shared-directory condition; got %v", moment, err)
	}
	if code, _ := cerrors.ShortCodeOf(err); code != "E0303" {
		t.Errorf("%s: the refusal carries short code %q; want E0303", moment, code)
	}
	var ce *cerrors.CompilerError
	if !errors.As(err, &ce) {
		t.Fatalf("%s: the refusal carries no composed diagnostic: %v", moment, err)
	}
	return ce.Error()
}

// TestTheDirectoryRuleRefusesIdenticallyAtEveryProducingMoment drives every
// surface that pins or publishes over one Recipe that shares its directory with
// another, and holds the five refusals to one diagnostic byte for byte. The
// fixture's Recipe names a remote chart and imports a file, so it is a Recipe
// that needs a lock — and none is written by any of the five.
func TestTheDirectoryRuleRefusesIdenticallyAtEveryProducingMoment(t *testing.T) {
	srv := serveChart(t, pinChartTgz(t))
	root := t.TempDir()
	t.Chdir(root)
	recipePath := pinLifecycleWorkspace(t, root, srv.URL+"/pinchart.tgz", "", true)
	writeFixture(t, filepath.Join(filepath.Dir(recipePath), "neighbour.yaml"), neighbourRecipe)
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	push := func(dryRun bool, pinSources *bool) error {
		return pushRun(context.Background(), pushConfig{
			Workspace: ws, EntityPath: recipePath, Registry: "example.com", PushTarget: memory.New(),
			DryRun: dryRun, PinSources: pinSources, Out: io.Discard,
		})
	}

	moments := []struct {
		name string
		run  func() error
	}{
		{"aggrega pin", func() error { return pinRun(context.Background(), io.Discard, ws, recipePath, false) }},
		{"aggrega pin --dry-run", func() error { return pinRun(context.Background(), io.Discard, ws, recipePath, true) }},
		{"aggrega pull --pin-sources", func() error { return pinLifecyclePull(t, root, boolPtr(true)) }},
		{"aggrega push --dry-run", func() error { return push(true, nil) }},
		{"aggrega push", func() error { return push(false, nil) }},
		{"aggrega push --pin-sources", func() error { return push(false, boolPtr(true)) }},
	}
	var first string
	for _, m := range moments {
		got := directoryRuleDiagnostic(t, m.name, m.run())
		if first == "" {
			first = got
			for _, want := range []string{`Recipe "server"`, `beside it: Recipe "neighbour" in neighbour.yaml`,
				"give each entity a directory of its own", "move neighbour.yaml into a directory of its own"} {
				if !strings.Contains(got, want) {
					t.Errorf("the refusal must carry %q:\n%s", want, got)
				}
			}
			t.Logf("the diagnostic every moment gives:\n%s", got)
			continue
		}
		if got != first {
			t.Errorf("%s gave a different diagnostic from the first moment;\n--- first ---\n%s\n--- %s ---\n%s", m.name, first, m.name, got)
		}
	}
	if _, err := os.Stat(recipepin.LockPath(recipePath)); !os.IsNotExist(err) {
		t.Errorf("a refused moment wrote the lock; stat err = %v", err)
	}

	// A pull that does not pin writes into no directory, so the rule does not
	// reach it: it goes on to hold the lock to the pins, as it would anywhere.
	if err := pinLifecyclePull(t, root, nil); !errors.Is(err, recipepin.ErrEntityNotPinned()) ||
		errors.Is(err, entitypin.ErrEntitySharesItsDirectory()) {
		t.Errorf("a pull that does not pin must not judge the directory, and holds the lock instead; got %v", err)
	}

	// The positive control: the same Recipe with its neighbour moved out pins.
	if err := os.Rename(filepath.Join(filepath.Dir(recipePath), "neighbour.yaml"), filepath.Join(root, "catalog", "neighbour.yaml")); err != nil {
		t.Fatalf("move the neighbour out: %v", err)
	}
	if err := pinRun(context.Background(), io.Discard, ws, recipePath, false); err != nil {
		t.Fatalf("the Recipe alone in its directory must pin; got %v", err)
	}
}

// TestTheDirectoryRuleAdmitsWhatItDoesNotGovern is the negative half: layouts
// that pin and publish untouched. An entity alone in a directory named for no
// version publishes at the version it declares, and Aggregates sitting beside a
// Release and a Manifest at a workspace root each publish.
func TestTheDirectoryRuleAdmitsWhatItDoesNotGovern(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\nauto_pull: false\n")
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})

	flat := filepath.Join(root, "catalog", "recipes", "neighbour.yaml")
	writeFixture(t, flat, neighbourRecipe)
	if err := pinRun(context.Background(), io.Discard, ws, flat, false); err != nil {
		t.Errorf("a Recipe alone in a flat directory must pin; got %v", err)
	}
	store := memory.New()
	if err := pushRun(context.Background(), pushConfig{Workspace: ws, EntityPath: flat, Registry: "example.com",
		PushTarget: store, Out: io.Discard}); err != nil {
		t.Fatalf("a Recipe alone in a flat directory must publish; got %v", err)
	}
	if _, err := oci.Pull(context.Background(), store, "1.0.0"); err != nil {
		t.Errorf("the flat Recipe must be published at the version it declares: %v", err)
	}

	for _, name := range []string{"app", "other"} {
		writeFixture(t, filepath.Join(root, name+".yaml"), "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: "+
			name+"\n  version: \"1.0.0\"\nspec:\n  components: {}\n")
	}
	writeFixture(t, filepath.Join(root, "release.yaml"), "apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: r\n  version: \"1.0.0\"\n")
	writeFixture(t, filepath.Join(root, "manifest.yaml"), "apiVersion: aggrega.ai/v1alpha1\nkind: Manifest\nmetadata:\n  name: m\n  version: \"1.0.0\"\n")
	for _, name := range []string{"app", "other"} {
		path := filepath.Join(root, name+".yaml")
		if err := pushRun(context.Background(), pushConfig{Workspace: ws, EntityPath: path, Registry: "example.com",
			PushTarget: memory.New(), DryRun: true, Out: io.Discard}); err != nil {
			t.Errorf("an Aggregate beside another Aggregate, a Release and a Manifest must publish; %s: %v", name, err)
		}
		if err := pinRun(context.Background(), io.Discard, ws, path, false); err != nil {
			t.Errorf("pinning an Aggregate at a workspace root must not be refused; %s: %v", name, err)
		}
	}
}
