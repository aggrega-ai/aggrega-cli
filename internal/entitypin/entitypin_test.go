// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package entitypin

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// recordingResolver answers every resolution at the version the spec names and
// records each spec it was asked for, so a test can say which sources the step
// reached for and which it never asked about.
type recordingResolver struct {
	asked []closure.SourceSpec
}

func (r *recordingResolver) Resolve(spec closure.SourceSpec) (*closure.ResolvedSource, error) {
	r.asked = append(r.asked, spec)
	return &closure.ResolvedSource{
		Kind:          spec.Kind,
		URL:           spec.URL,
		Chart:         spec.Chart,
		ModuleSubdir:  spec.ModuleSubdir,
		PinnedVersion: spec.Version,
		Digest:        digest.ContentDigest([]byte(spec.URL + "@" + spec.Version)),
	}, nil
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// recipeYAML authors a Recipe named name whose source block is source (already
// indented under spec) and which imports one file when withImport is set. A Recipe
// given no source builds a workload instead, so every one it authors is a Recipe
// the catalog loads.
func recipeYAML(name, source string, withImport bool) string {
	body := "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: " + name + "\n  version: \"1.0.0\"\n" +
		"spec:\n  capability: helm\n  implements: " + name + "\n" + source
	if source == "" {
		body += "  workload: |\n    apiVersion: aggrega.ai/v1alpha1\n    kind: Workload\n    containers:\n      main:\n        image: demo:1\n"
	}
	if withImport {
		body += "  artifacts:\n    extra.yaml:\n      $import: ./extra.yaml\n"
	}
	return body
}

// TestComputeRoutesOnlyAnAuthoredIdentityBesideItsEntity drives the step over three
// Recipes, one per route a source takes, each in a directory of its own.
//
// The three disagree, which is what makes the test discriminate: an authored
// identity is resolved and pinned, a templated identity is never even asked about
// — it names an artifact only per Release — and a source the workspace holds is
// neither fetched nor pinned. A step that pinned every source would fail the
// second, and one that pinned none would fail the first.
func TestComputeRoutesOnlyAnAuthoredIdentityBesideItsEntity(t *testing.T) {
	ws := t.TempDir()
	cat := filepath.Join(ws, "catalog")
	writeFile(t, filepath.Join(cat, "authored", "recipe.yaml"), recipeYAML("authored",
		"  source:\n    kind: helm-https-chart\n    url: https://example.invalid/a.tgz\n    version: \"1.0.0\"\n", true))
	writeFile(t, filepath.Join(cat, "authored", "extra.yaml"), "kind: ConfigMap\n")
	writeFile(t, filepath.Join(cat, "templated", "recipe.yaml"), recipeYAML("templated",
		"  source:\n    kind: helm-https-chart\n    url: https://example.invalid/t.tgz\n    version: \"{{ .infra.v }}\"\n", false))
	writeFile(t, filepath.Join(cat, "local", "recipe.yaml"), recipeYAML("local", "  source: ./chart\n", false))
	writeFile(t, filepath.Join(cat, "local", "chart", "Chart.yaml"), "apiVersion: v2\nname: local\nversion: 1.0.0\n")

	view := catalog.NewLazyLookup(ws, "", []string{cat}, nil)
	res := &recordingResolver{}
	plan, err := Compute(Inputs{
		View:          view,
		URIs:          []string{"helm/authored:1.0.0", "helm/templated:1.0.0", "helm/local:1.0.0"},
		Resolver:      res,
		WorkspaceRoot: ws,
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(res.asked) != 1 || res.asked[0].URL != "https://example.invalid/a.tgz" {
		t.Fatalf("the step must resolve the authored-identity source and nothing else; it asked about %+v", res.asked)
	}
	if len(plan.entities) != 3 {
		t.Fatalf("every Recipe with a writable home is planned; got %d", len(plan.entities))
	}
	for _, e := range plan.entities {
		switch filepath.Base(filepath.Dir(e.path)) {
		case "authored":
			if len(e.want.SourceDigests) != 1 || len(e.want.ArtifactDigests) != 1 {
				t.Errorf("the authored-identity Recipe must pin its source and its imported file; wants %+v", e.want)
			}
		default:
			if !e.want.Empty() {
				t.Errorf("%s must pin nothing; wants %+v", e.path, e.want)
			}
		}
	}
}

// TestUnpinnedHaltsUntilTheStepHasWrittenAndThenIsSilent is the halt's own pair:
// the same plan halts while the locks do not record the pins and is silent once
// the step has written them, and the write is what moved it.
func TestUnpinnedHaltsUntilTheStepHasWrittenAndThenIsSilent(t *testing.T) {
	ws := t.TempDir()
	cat := filepath.Join(ws, "catalog")
	recipe := filepath.Join(cat, "server", "recipe.yaml")
	writeFile(t, recipe, recipeYAML("server",
		"  source:\n    kind: helm-https-chart\n    url: https://example.invalid/s.tgz\n    version: \"1.0.0\"\n", true))
	writeFile(t, filepath.Join(cat, "server", "extra.yaml"), "kind: ConfigMap\n")
	compute := func() Plan {
		t.Helper()
		plan, err := Compute(Inputs{
			View:          catalog.NewLazyLookup(ws, "", []string{cat}, nil),
			URIs:          []string{"helm/server:1.0.0"},
			Resolver:      &recordingResolver{},
			WorkspaceRoot: ws,
		})
		if err != nil {
			t.Fatalf("Compute: %v", err)
		}
		return plan
	}

	err := compute().Unpinned(testRemedy)
	if !errors.Is(err, recipepin.ErrEntityNotPinned()) {
		t.Fatalf("an entity with no lock must halt under the unpinned-entity condition; got %v", err)
	}
	for _, want := range []string{"remote source", "imported file", "aggrega pin catalog/server/recipe.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the halt must name %q:\n%v", want, err)
		}
	}

	if _, err := compute().Write(); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := compute().Unpinned(testRemedy); err != nil {
		t.Fatalf("once the step has written the lock the same plan must be silent; got %v", err)
	}
}

// testRemedy is a remediation no production operation uses, so a test that finds
// its words in a halt knows the remedy it passed is the one that reached it.
var testRemedy = recipepin.Remedy{Operation: "exercise", Setting: "exercise_setting", Invocation: "send exercise"}

// TestHoldRefusesWhatTheStepWouldWriteAndAcceptsWhatItWrote drives the hold over
// one entity document through the same named-document path the step takes: it
// refuses an entity whose lock is missing a pin, names the remedy it was handed,
// writes nothing, and accepts the entity once the step has written its lock.
//
// The input is an imported file, so the proof needs no network — and an imported
// file is not a remote source, so a hold that looked at remote sources alone would
// pass this entity and fail the first assertion. The halt's subject is the lock's
// pin classes as a set.
func TestHoldRefusesWhatTheStepWouldWriteAndAcceptsWhatItWrote(t *testing.T) {
	ws := t.TempDir()
	rw := resolvedWorkspace(t, ws)
	recipe := filepath.Join(ws, "catalog", "server", "recipe.yaml")
	writeFile(t, recipe, recipeYAML("server", "", true))
	writeFile(t, filepath.Join(ws, "catalog", "server", "extra.yaml"), "kind: ConfigMap\n")
	opts := Options{Workspace: rw, EntityPath: recipe}

	err := Hold(context.Background(), opts, testRemedy)
	if !errors.Is(err, recipepin.ErrEntityNotPinned()) {
		t.Fatalf("an entity whose lock records no pin must be held under the unpinned-entity condition; got %v", err)
	}
	for _, want := range []string{"imported file extra.yaml: no pin recorded", "aggrega pin catalog/server/recipe.yaml",
		"to have this exercise record the pins itself instead, send exercise", "exercise_setting: true"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the hold's halt must carry %q:\n%v", want, err)
		}
	}
	if _, statErr := os.Stat(recipepin.LockPath(recipe)); !os.IsNotExist(statErr) {
		t.Fatalf("the hold wrote a lock; stat err = %v", statErr)
	}

	if _, err := Pin(context.Background(), opts); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if err := Hold(context.Background(), opts, testRemedy); err != nil {
		t.Fatalf("the lock the step wrote must satisfy the hold; got %v", err)
	}
}

// TestHoldAcceptsAnEntityWithNothingToPin is the hold's negative half: an entity
// that reads nothing a lock records passes untouched, whether it is a kind with no
// pin class at all or a Recipe whose source is filled in per Release. A hold that
// refused either would refuse legal input, which a publication must never do.
func TestHoldAcceptsAnEntityWithNothingToPin(t *testing.T) {
	ws := t.TempDir()
	rw := resolvedWorkspace(t, ws)
	component := filepath.Join(ws, "catalog", "c", "component.yaml")
	writeFile(t, component, "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: c\n  version: \"1.0.0\"\nspec: {}\n")
	templated := filepath.Join(ws, "catalog", "templated", "recipe.yaml")
	writeFile(t, templated, recipeYAML("templated",
		"  source:\n    kind: helm-https-chart\n    url: https://example.invalid/t.tgz\n    version: \"{{ .infra.v }}\"\n", false))
	workload := filepath.Join(ws, "catalog", "workload", "recipe.yaml")
	writeFile(t, workload, recipeYAML("workload", "", false))

	for _, doc := range []string{component, templated, workload} {
		if err := Hold(context.Background(), Options{Workspace: rw, EntityPath: doc}, testRemedy); err != nil {
			t.Errorf("%s has nothing to pin and must pass the hold; got %v", doc, err)
		}
		if _, statErr := os.Stat(recipepin.LockPath(doc)); !os.IsNotExist(statErr) {
			t.Errorf("the hold wrote a lock beside %s; stat err = %v", doc, statErr)
		}
	}
}

// TestReportIsTheWriteWithoutTheWrite holds the reporting read of a plan to the
// writing one byte for byte, and to having written nothing.
func TestReportIsTheWriteWithoutTheWrite(t *testing.T) {
	ws := t.TempDir()
	cat := filepath.Join(ws, "catalog")
	recipe := filepath.Join(cat, "server", "recipe.yaml")
	writeFile(t, recipe, recipeYAML("server", "", true))
	writeFile(t, filepath.Join(cat, "server", "extra.yaml"), "kind: ConfigMap\n")
	plan, err := Compute(Inputs{View: catalog.NewLazyLookup(ws, "", []string{cat}, nil),
		URIs: []string{"helm/server:1.0.0"}, Resolver: &recordingResolver{}, WorkspaceRoot: ws})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	_, files, err := plan.Report()
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("the report must name the one lock that would move; it names %d", len(files))
	}
	if _, err := os.Stat(recipepin.LockPath(recipe)); !os.IsNotExist(err) {
		t.Fatalf("a report wrote the lock; stat err = %v", err)
	}
	if _, err := plan.Write(); err != nil {
		t.Fatalf("Write: %v", err)
	}
	written, err := os.ReadFile(recipepin.LockPath(recipe))
	if err != nil {
		t.Fatalf("read the written lock: %v", err)
	}
	if !bytes.Equal(written, files[0].Content) {
		t.Errorf("the reported bytes are not the written ones;\nreported:\n%s\nwritten:\n%s", files[0].Content, written)
	}
}

// resolvedWorkspace resolves a workspace rooted at root with no environment.
func resolvedWorkspace(t *testing.T, root string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	writeFile(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
	rw, err := workspaceconfig.Resolve(root, root, func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolve the workspace: %v", err)
	}
	return rw
}

// TestPinNeedsNoReleaseAndLeavesTheEntityUntouched drives the author's own step
// over a workspace holding no Release document at all, on a Recipe whose one
// input is an imported file — so the proof needs no network.
func TestPinNeedsNoReleaseAndLeavesTheEntityUntouched(t *testing.T) {
	ws := t.TempDir()
	rw := resolvedWorkspace(t, ws)
	recipe := filepath.Join(ws, "catalog", "server", "recipe.yaml")
	writeFile(t, recipe, recipeYAML("server", "", true))
	writeFile(t, filepath.Join(ws, "catalog", "server", "extra.yaml"), "kind: ConfigMap\n")
	before, _ := os.ReadFile(recipe)

	res, err := Pin(context.Background(), Options{Workspace: rw, EntityPath: recipe})
	if err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if !res.Pinnable || !res.Outcome.Changed {
		t.Fatalf("the first pin must write the lock; got %+v", res)
	}
	lock, present, err := recipepin.ReadLock(recipe)
	if err != nil || !present || len(lock.ArtifactDigests) != 1 {
		t.Fatalf("the lock must record the imported file; present=%v err=%v lock=%+v", present, err, lock)
	}
	if after, _ := os.ReadFile(recipe); !bytes.Equal(after, before) {
		t.Errorf("the step wrote the authored document")
	}

	again, err := Pin(context.Background(), Options{Workspace: rw, EntityPath: recipe})
	if err != nil {
		t.Fatalf("Pin again: %v", err)
	}
	if again.Outcome.Changed {
		t.Errorf("a second pin over unchanged inputs must leave the lock alone; got %+v", again.Outcome)
	}
}

// TestPinReportsAKindWithNothingToPinAndTouchesNothing is the negative control for
// the kinds the step has no class for: a Component reads nothing a lock records,
// so the step says so and a lock already sitting in the directory is left alone.
func TestPinReportsAKindWithNothingToPinAndTouchesNothing(t *testing.T) {
	ws := t.TempDir()
	rw := resolvedWorkspace(t, ws)
	component := filepath.Join(ws, "catalog", "c", "component.yaml")
	writeFile(t, component, "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: c\n  version: \"1.0.0\"\nspec: {}\n")
	existing := recipepin.LockPath(component)
	writeFile(t, existing, "sentinel\n")

	res, err := Pin(context.Background(), Options{Workspace: rw, EntityPath: component})
	if err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if res.Pinnable {
		t.Errorf("a Component has nothing a lock records; got %+v", res)
	}
	if got, _ := os.ReadFile(existing); string(got) != "sentinel\n" {
		t.Errorf("the step touched a lock beside a kind it has nothing to pin for")
	}
}
