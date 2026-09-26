// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// What the command says about a drafted document, and about a kind its
// single-entity scope does not take, driven at the real entrypoint: the drafted
// document comes from the drafting command itself, and each verdict is read off
// a spawned binary rather than off the command object, because the refusal an
// author reads is the one the whole path renders.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
)

// runIsolated runs the binary in dir with a home directory and configuration
// directories of its own, and returns everything it emitted with the error, so
// no drive reads or writes a cache or a configuration another run shares.
func runIsolated(t *testing.T, bin, dir string, args ...string) (string, error) {
	t.Helper()
	home := t.TempDir()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"XDG_CACHE_HOME="+filepath.Join(home, "cache"),
		"XDG_CONFIG_HOME="+filepath.Join(home, "config"),
		"XDG_DATA_HOME="+filepath.Join(home, "data"),
	)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// draftWith drafts a document through the drafting command and returns it.
func draftWith(t *testing.T, bin, dir, kind, name string) string {
	t.Helper()
	cmd := exec.Command(bin, "scaffold", kind, name)
	cmd.Dir = dir
	var out, stderr strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("the command refused to draft a %s: %v (%s)", kind, err, stderr.String())
	}
	return out.String()
}

// writeInto writes a document at a path relative to dir, creating its parents.
func writeInto(t *testing.T, dir, rel, doc string) string {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return path
}

// fillLine replaces a line a drafted document carries, failing when it
// carries no such line, so a fill that changed nothing cannot pass for one that
// did.
func fillLine(t *testing.T, doc, old, repl string) string {
	t.Helper()
	if !strings.Contains(doc, old) {
		t.Fatalf("the drafted document carries no %q:\n%s", old, doc)
	}
	return strings.Replace(doc, old, repl, 1)
}

// TestTheCommandNamesEveryPlaceholderADraftCarries drafts a Component and a
// FeatureRecipe with the drafting command and validates each as drafted,
// part-filled and filled.
//
// As drafted, each is refused naming every placeholder with what it takes, and
// the pattern the schema checks an empty value against is not what the author
// is shown. A FeatureRecipe with only its version filled is refused for the two
// references still owed, although its schema admits them empty. Filled, both
// validate.
func TestTheCommandNamesEveryPlaceholderADraftCarries(t *testing.T) {
	// Not parallel: spawns subprocesses.
	bin := buildServeBinary(t)
	ws := t.TempDir()

	component := draftWith(t, bin, ws, "component", "my-service")
	featureRecipe := draftWith(t, bin, ws, "featurerecipe", "my-feature-recipe")

	frRefs := []string{
		"7:3: spec.capability takes what its schema describes: The explicit provider capability this recipe binds to",
		"8:3: spec.implements takes what its schema describes: Declares which catalog Feature type this recipe is able to implement",
	}
	refused := []struct {
		name string
		doc  string
		want []string
	}{
		{
			name: "a Component as drafted",
			doc:  component,
			want: []string{
				"2 placeholders still owed",
				"5:3: metadata.version takes a semantic version, such as 1.0.0",
				"7:5: metadata.labels.component.aggrega.ai/type takes one of: server, worker, task, function, stateful-server, resource",
			},
		},
		{
			name: "a FeatureRecipe as drafted",
			doc:  featureRecipe,
			want: append([]string{"3 placeholders still owed", "5:3: metadata.version takes a semantic version, such as 1.0.0"}, frRefs...),
		},
		{
			name: "a FeatureRecipe with only its version filled",
			doc:  fillLine(t, featureRecipe, `  version: ""`, `  version: "1.0.0"`),
			want: append([]string{"2 placeholders still owed"}, frRefs...),
		},
	}
	for i, tc := range refused {
		path := writeInto(t, ws, "refused-"+string(rune('a'+i))+".yaml", tc.doc)
		out, err := runIsolated(t, bin, ws, "validate", path)
		if err == nil {
			t.Errorf("%s: accepted with its placeholders unfilled, so an empty required value reads to this "+
				"product as a value:\n%s", tc.name, out)
			continue
		}
		for _, line := range tc.want {
			if !strings.Contains(out, line) {
				t.Errorf("%s: the refusal does not say %q:\n%s", tc.name, line, out)
			}
		}
		if strings.Contains(out, "does not match regular expression") || strings.Contains(out, "does not equal any of") {
			t.Errorf("%s: the schema's rendering of an empty value still stands in for the placeholder line:\n%s", tc.name, out)
		}
	}

	filled := []struct {
		name string
		doc  string
	}{
		{name: "a Component with its placeholders filled", doc: fillLine(t,
			fillLine(t, component, `  version: ""`, `  version: "0.1.0"`),
			`    component.aggrega.ai/type: ""`, `    component.aggrega.ai/type: "server"`)},
		{name: "a FeatureRecipe with its placeholders filled", doc: fillLine(t, fillLine(t,
			fillLine(t, featureRecipe, `  version: ""`, `  version: "1.0.0"`),
			`  capability: ""`, `  capability: "helm"`),
			`  implements: ""`, `  implements: "example/observability"`)},
	}
	for i, tc := range filled {
		path := writeInto(t, ws, "filled-"+string(rune('a'+i))+".yaml", tc.doc)
		if out, err := runIsolated(t, bin, ws, "validate", path); err != nil {
			t.Errorf("%s: still refused, so it was refused for its shape rather than its placeholders: %v\n%s",
				tc.name, err, out)
		}
	}
}

// TestACatalogLoadReportsAnEmptyValueAsItsSchemaDoes holds the reading's bound:
// it belongs to the single-entity scope. A whole-catalog run meeting the same
// drafted Component reports what the schema reports, in the schema's words, and
// one meeting a FeatureRecipe whose references are empty admits it, as the
// schema does.
func TestACatalogLoadReportsAnEmptyValueAsItsSchemaDoes(t *testing.T) {
	// Not parallel: spawns subprocesses.
	bin := buildServeBinary(t)

	drafted := t.TempDir()
	writeInto(t, drafted, filepath.Join("catalog", "app", "component.yaml"), draftWith(t, bin, drafted, "component", "my-service"))
	out, err := runIsolated(t, bin, drafted, "validate")
	if err == nil {
		t.Fatalf("a whole-catalog run accepted a drafted Component:\n%s", out)
	}
	for _, want := range []string{
		"document envelope validation failed (Component",
		`metadata.version: pattern: "" does not match regular expression`,
		"metadata.labels.component.aggrega.ai/type: enum:  does not equal any of",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the catalog load no longer reports %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "still owed") {
		t.Errorf("the placeholder reading reached a catalog load:\n%s", out)
	}

	admitted := t.TempDir()
	fr := fillLine(t, draftWith(t, bin, admitted, "featurerecipe", "my-feature-recipe"), `  version: ""`, `  version: "1.0.0"`)
	writeInto(t, admitted, filepath.Join("catalog", "app", "featurerecipe.yaml"), fr)
	if out, err := runIsolated(t, bin, admitted, "validate"); err != nil {
		t.Errorf("a whole-catalog run refused a FeatureRecipe its schema admits: %v\n%s", err, out)
	}
}

// TestAStandaloneAggregateIsRefusedWithTheScopesThatCheckIt drafts an Aggregate
// and hands it to the single-entity scope, which does not take one, and reads
// that the refusal says what does.
func TestAStandaloneAggregateIsRefusedWithTheScopesThatCheckIt(t *testing.T) {
	// Not parallel: spawns subprocesses.
	bin := buildServeBinary(t)
	ws := t.TempDir()
	path := writeInto(t, ws, "aggregate.yaml", draftWith(t, bin, ws, "aggregate", "my-app"))

	out, err := runIsolated(t, bin, ws, "validate", path)
	if err == nil {
		t.Fatalf("the single-entity scope accepted an Aggregate:\n%s", out)
	}
	for _, want := range []string{
		"is not a single-entity validation target",
		"the kinds this scope accepts are Component, Feature, Recipe, FeatureRecipe, Workload and Plugin",
		"An Aggregate is checked by the whole-catalog scope when it sits in a catalog directory",
		"and by the Release scope over a Release that deploys it",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
}

// TestTheHelpNamesTheKindsTheSingleEntityScopeAccepts holds the help's sentence
// naming the accepted kinds to the list the scope reads, so the two cannot come
// apart when a kind is added to one of them.
func TestTheHelpNamesTheKindsTheSingleEntityScopeAccepts(t *testing.T) {
	t.Parallel()
	kinds := validateops.SingleEntityKinds()
	if len(kinds) < 2 {
		t.Fatalf("the scope accepts %d kinds, so the sentence below asserts nothing", len(kinds))
	}
	phrase := strings.Join(kinds[:len(kinds)-1], ", ") + " and " + kinds[len(kinds)-1]
	help := strings.Join(strings.Fields(newValidateCmd().Long), " ")
	if want := "The kinds it accepts as a single-entity target are " + phrase + "."; !strings.Contains(help, want) {
		t.Errorf("the help does not name the kinds the scope accepts; want %q in:\n%s", want, help)
	}
	if want := "a whole-catalog run reaches an Aggregate and the library kinds when they sit in a catalog directory"; !strings.Contains(help, want) {
		t.Errorf("the help does not say where a whole-catalog run reaches an Aggregate; want %q in:\n%s", want, help)
	}
}
