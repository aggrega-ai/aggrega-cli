// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// The harness the as-drafted build proofs share: a workspace whose catalog is the
// standard library's as the hub publishes it, an import run exactly as each
// documentation example's walkthrough runs it, the answers file the import
// writes for what it leaves empty filled in as a user fills it, the import run
// again with that file, and the build of what the import then drafted. Nothing
// the import drafted is edited.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// importExamplesDir holds the documentation's import examples, each a directory
// import-<format> whose source/ directory is what its walkthrough imports. The
// path leaves this repository for the aggrega-docs repository checked out beside
// it, as platformHub leaves it for aggrega-hub.
const importExamplesDir = "../../../aggrega-docs/language-reference/examples"

// providerPlugins are the provider Plugin entities the build proofs of this
// package build with, whose guests resolve from the binary's embedded set.
const providerPlugins = "../compiler/testdata/flow-definition-escape/catalog"

// hubCatalogKinds returns the directories of the hub's catalog every kind of
// entity but Plugin sits in, in the order the catalog lists them. Plugin entities
// are left out because the importers an import runs and the providers a build
// renders through are laid down by the harness, from this repository's own
// sources and embedded set, and a published copy beside them would be a second
// importer or provider of the same format.
func hubCatalogKinds(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(platformHub)
	if err != nil {
		t.Fatalf("read the hub's catalog: %v", err)
	}
	var kinds []string
	for _, e := range entries {
		if e.IsDir() && e.Name() != "plugin" {
			kinds = append(kinds, e.Name())
		}
	}
	if !slices.Contains(kinds, "component") || !slices.Contains(kinds, "recipe") {
		t.Fatalf("the hub's catalog holds %v, with no component or recipe directory", kinds)
	}
	return kinds
}

// wholeHubWorkspace is catalogFolderImportWorkspaceAttaching handing the
// importers no settings — so each runs as an import naming no attachment runs
// it — with every entity of the hub's catalog but its Plugin entities copied into
// the catalog folder at the paths the hub keeps them under: the catalog an import
// of a documentation example reads, every Component, Recipe, FeatureRecipe and
// Aggregate the drafted Manifest is composed from included.
func wholeHubWorkspace(t *testing.T) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	rw, folder := catalogFolderImportWorkspaceAttaching(t, nil)
	for _, kind := range hubCatalogKinds(t) {
		if err := os.CopyFS(filepath.Join(folder.Dir, kind), os.DirFS(filepath.Join(platformHub, kind))); err != nil {
			t.Fatalf("copy the hub's %s entities into the catalog folder: %v", kind, err)
		}
	}
	return rw
}

// exampleSource copies the source/ directory of the documentation example
// import-<format> into a directory named source of its own, leaving out the
// answers file the example ships, and returns the copy and the shipped answers
// file's path inside the copy, where an import of the copy looks for it. The
// import reads the copy and never the example, so nothing an import writes beside
// its source can reach the documentation; the directory keeps the name source,
// since an import of a directory names its draft after the directory.
//
// The shipped answers file is left out so the first import of a case meets the
// source alone, as a user's first import does, and reports everything the source
// leaves owed; the example's own answers are read from the example
// ([exampleAnswers]). The test fails where the example ships no answers file, or
// more than one.
func exampleSource(t *testing.T, format string) (string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "source")
	if err := os.CopyFS(dir, os.DirFS(filepath.Join(importExamplesDir, "import-"+format, "source"))); err != nil {
		t.Fatalf("copy the import-%s example's source: %v", format, err)
	}
	var shipped []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != AnswersFileName {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		shipped = append(shipped, rel)
		return os.Remove(path)
	})
	if err != nil {
		t.Fatalf("leave the import-%s example's answers file out of the copy: %v", format, err)
	}
	if len(shipped) != 1 {
		t.Fatalf("the import-%s example ships %d answers files %q, want one", format, len(shipped), shipped)
	}
	return dir, shipped[0]
}

// exampleAnswers reads the answers file the documentation example import-<format>
// ships at rel inside its source/ directory, and returns each answer it gives by
// its place, keyed as draftedScalars keys a place: the drafted file, a colon, and
// the key path.
func exampleAnswers(t *testing.T, format, rel string) map[string]string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(importExamplesDir, "import-"+format, "source", rel))
	if err != nil {
		t.Fatalf("read the import-%s example's answers file: %v", format, err)
	}
	var byFile map[string]map[string]string
	if err := yaml.Unmarshal(body, &byFile); err != nil {
		t.Fatalf("the import-%s example's answers file does not read as drafted files of key paths: %v", format, err)
	}
	answers := map[string]string{}
	for file, keys := range byFile {
		for key, answer := range keys {
			answers[file+":"+key] = answer
		}
	}
	return answers
}

// importAsTheExample imports the source argument names inside sourceDir for
// target, as the example's walkthrough runs `aggrega import <argument> --target
// <target>` from that directory — with `--answers <answers>` where answers is
// set — writing the drafts into the directory named into beneath the workspace
// root: the source path as typed is handed on as the source's name, nobody is
// asked anything, and no pull is made. It returns the result and that directory,
// failing the test where the import is refused.
func importAsTheExample(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, sourceDir, argument, target, into,
	answers string) (Result, string) {
	t.Helper()
	outputDir := filepath.Join(rw.Root(), into)
	res, err := Import(context.Background(), rw, Options{
		SourcePath:    filepath.Join(sourceDir, filepath.FromSlash(argument)),
		SourceName:    argument,
		Target:        target,
		OutputDir:     outputDir,
		Delivery:      authoredcontent.DeliveryFor(false),
		ResolvePlugin: (&recordingCascade{}).resolve,
		Answers:       answers,
	})
	if err != nil {
		t.Fatalf("the import of %s for the target %q was refused: %v", argument, target, err)
	}
	return res, outputDir
}

// owedPlaces returns the place of every value an import reports its drafts still
// owe, sorted, keyed as draftedScalars keys a place: the drafted file, a colon,
// and the key path as the answers file writes it.
func owedPlaces(res Result) []string {
	places := make([]string, 0, len(res.Owed))
	for _, o := range res.Owed {
		places = append(places, o.File+":"+o.Key())
	}
	sort.Strings(places)
	return places
}

// holdPlaces fails the test where got, a sorted list of places, is not exactly
// the places want names, naming each place got holds that want does not and each
// place want names that got does not hold. what says whose places got are.
func holdPlaces(t *testing.T, what string, got []string, want map[string]string) {
	t.Helper()
	var unexpected, missing []string
	for _, place := range got {
		if _, ok := want[place]; !ok {
			unexpected = append(unexpected, place)
		}
	}
	for place := range want {
		if !slices.Contains(got, place) {
			missing = append(missing, place)
		}
	}
	sort.Strings(missing)
	if len(unexpected) > 0 || len(missing) > 0 {
		t.Fatalf("%s %q;\nnot expected: %q\nexpected and absent: %q", what, got, unexpected, missing)
	}
}

// fillAnswersFile writes answers into the answers file at path, as a user
// finishing an import does: it first reads the file the import wrote there and
// requires it to list exactly the places answers names, each under its drafted
// file and its key path with an empty answer, and then writes each answer beside
// its key. answers is keyed as draftedScalars keys a place.
func fillAnswersFile(t *testing.T, path string, answers map[string]string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the answers file the import wrote: %v", err)
	}
	var listed map[string]map[string]string
	if err := yaml.Unmarshal(body, &listed); err != nil {
		t.Fatalf("the answers file the import wrote does not read as drafted files of key paths: %v\n%s", err, body)
	}
	var places []string
	for file, keys := range listed {
		for key, answer := range keys {
			places = append(places, file+":"+key)
			if answer != "" {
				t.Errorf("the answers file the import wrote answers %s in %s with %q, want it left empty", key, file,
					answer)
			}
		}
	}
	sort.Strings(places)
	holdPlaces(t, "the answers file the import wrote lists", places, answers)

	filled := map[string]map[string]string{}
	for place, answer := range answers {
		file, key, _ := strings.Cut(place, ":")
		if filled[file] == nil {
			filled[file] = map[string]string{}
		}
		filled[file][key] = answer
	}
	out, err := yaml.Marshal(filled)
	if err != nil {
		t.Fatalf("encode the answers: %v", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatalf("write the answers file: %v", err)
	}
}

// assertAnsweredAsDrafted holds the drafts an answered import wrote under
// answeredDir to the drafts the unanswered import of the same source wrote under
// draftedDir: every value answers names reads as its answer, nothing is left
// empty, and every other value of every drafted file is the one the unanswered
// drafts hold, at the same place — the answers are all the import changed.
func assertAnsweredAsDrafted(t *testing.T, draftedDir, answeredDir string, answers map[string]string) {
	t.Helper()
	drafted, answered := draftedScalars(t, draftedDir), draftedScalars(t, answeredDir)
	for place, n := range drafted {
		got, ok := answered[place]
		switch answer, owed := answers[place]; {
		case !ok:
			t.Errorf("the answered drafts hold no %s", place)
		case owed && got.Value != answer:
			t.Errorf("%s reads %q in the answered drafts, want its answer %q", place, got.Value, answer)
		case !owed && got.Value != n.Value:
			t.Errorf("%s reads %q in the answered drafts and %q in the unanswered ones", place, got.Value, n.Value)
		}
	}
	for place := range answered {
		if _, ok := drafted[place]; !ok {
			t.Errorf("the answered drafts hold %s, which the unanswered ones do not", place)
		}
	}
	if left := emptyPlaceholders(t, answeredDir); len(left) > 0 {
		t.Errorf("the answered drafts still leave empty %q", left)
	}
}

// draftedScalars reads every YAML file the import drafted under dir and returns
// each scalar it holds, keyed by the file's path relative to dir, a colon, and
// the scalar's place in the document: mapping keys joined by dots, a sequence
// member's index in brackets — release.yaml:spec.components.api.infra.image.
// Comments are not scalars, and a block scalar is one scalar however many lines
// it spans, so a flow carried as text is read as the one value it is.
//
// 🔴 This reading is deliberately written apart from the import's own reading of
// what a draft still owes: it is the oracle the proofs hold that reading to, so
// sharing it would leave the proofs agreeing with whatever it reads.
func draftedScalars(t *testing.T, dir string) map[string]*yaml.Node {
	t.Helper()
	scalars := map[string]*yaml.Node{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".yaml" {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(body, &doc); err != nil {
			t.Fatalf("the drafted %s does not read as YAML: %v", rel, err)
		}
		var walk func(n *yaml.Node, at string)
		walk = func(n *yaml.Node, at string) {
			switch n.Kind {
			case yaml.DocumentNode:
				for _, c := range n.Content {
					walk(c, at)
				}
			case yaml.MappingNode:
				for i := 0; i+1 < len(n.Content); i += 2 {
					key := n.Content[i].Value
					if at != "" {
						key = at + "." + key
					}
					walk(n.Content[i+1], key)
				}
			case yaml.SequenceNode:
				for i, c := range n.Content {
					walk(c, at+"["+strconv.Itoa(i)+"]")
				}
			case yaml.ScalarNode:
				scalars[filepath.ToSlash(rel)+":"+at] = n
			}
		}
		walk(&doc, "")
		return nil
	})
	if err != nil {
		t.Fatalf("read the drafts under %s: %v", dir, err)
	}
	return scalars
}

// isEmptyPlaceholder reports whether n is a value the import left for the user:
// a string written as the empty quoted string, which is how a draft marks a value
// no source holds. An absent value and an empty block are not written that way.
func isEmptyPlaceholder(n *yaml.Node) bool {
	return n.ShortTag() == "!!str" && n.Value == "" &&
		(n.Style == yaml.DoubleQuotedStyle || n.Style == yaml.SingleQuotedStyle)
}

// emptyPlaceholders returns the place of every value the drafts under dir leave
// for the user, sorted, keyed as draftedScalars keys them.
func emptyPlaceholders(t *testing.T, dir string) []string {
	t.Helper()
	var places []string
	for place, n := range draftedScalars(t, dir) {
		if isEmptyPlaceholder(n) {
			places = append(places, place)
		}
	}
	sort.Strings(places)
	return places
}

// buildAsDrafted builds the Release the import drafted under dir — with the
// Aggregate and the Manifest it names, the drafted ones beside it — through the
// build-faithful harness, over the hub's catalog read in place, every kind but
// its Plugin entities, and the provider Plugin entities the build proofs of this
// package build with. It returns what the build rendered, by path, failing the
// test where the build halts.
func buildAsDrafted(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	catalogs := []string{providerPlugins}
	for _, kind := range hubCatalogKinds(t) {
		catalogs = append(catalogs, filepath.Join(platformHub, kind))
	}
	artifacts := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  filepath.Join(dir, "release.yaml"),
		CatalogPaths: catalogs,
	}).Artifacts
	if len(artifacts) == 0 {
		t.Fatal("the build of the drafts completed and rendered nothing")
	}
	return artifacts
}

// renderedStack is the part of a rendered Docker Compose file the as-drafted
// proofs read: each service's image and environment.
type renderedStack struct {
	Services map[string]struct {
		Image       string            `yaml:"image"`
		Environment map[string]string `yaml:"environment"`
	} `yaml:"services"`
}

// renderedComposeStack reads the one Docker Compose file a build rendered, which
// a Release placing its components in no boundary of its own writes as
// compose/docker-compose.yaml and one placing them in a boundary beneath that
// boundary's directory.
func renderedComposeStack(t *testing.T, artifacts map[string][]byte) renderedStack {
	t.Helper()
	var matched []string
	for path := range artifacts {
		if path == "compose/docker-compose.yaml" || strings.HasSuffix(path, "/compose/docker-compose.yaml") {
			matched = append(matched, path)
		}
	}
	if len(matched) != 1 {
		t.Fatalf("want one rendered Docker Compose file, found %q", matched)
	}
	var stack renderedStack
	if err := yaml.Unmarshal(artifacts[matched[0]], &stack); err != nil {
		t.Fatalf("the rendered %s does not read: %v\n%s", matched[0], err, artifacts[matched[0]])
	}
	return stack
}

// renderedContainer is the first container a rendered Kubernetes workload runs:
// its image and its environment, each variable's literal value or the Secret and
// key it is read from.
type renderedContainer struct {
	Image string
	Env   []helmVariable
}

// renderedFirstContainer reads the rendered file at path, which must hold one
// Deployment or StatefulSet, and returns its first container. It reads a
// StatefulSet and the image as well as the environment, which deploymentEnvs,
// reading a Deployment's variables alone, does not.
func renderedFirstContainer(t *testing.T, artifacts map[string][]byte, path string) renderedContainer {
	t.Helper()
	body, ok := artifacts[path]
	if !ok {
		all := make([]string, 0, len(artifacts))
		for p := range artifacts {
			all = append(all, p)
		}
		sort.Strings(all)
		t.Fatalf("the build rendered no %s; it rendered %v", path, all)
	}
	var doc struct {
		Kind string `yaml:"kind"`
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct {
						Image string         `yaml:"image"`
						Env   []helmVariable `yaml:"env"`
					} `yaml:"containers"`
				} `yaml:"spec"`
			} `yaml:"template"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil || (doc.Kind != "Deployment" && doc.Kind != "StatefulSet") ||
		len(doc.Spec.Template.Spec.Containers) == 0 {
		t.Fatalf("the rendered %s is not a workload with a container (%v):\n%s", path, err, body)
	}
	c := doc.Spec.Template.Spec.Containers[0]
	return renderedContainer{Image: c.Image, Env: c.Env}
}

// literal returns the literal value the container is given for the variable
// name, failing the test where it is given none.
func (c renderedContainer) literal(t *testing.T, name string) string {
	t.Helper()
	for _, e := range c.Env {
		if e.Name == name && e.Value != nil {
			return *e.Value
		}
	}
	t.Fatalf("the container is given no literal %s: %+v", name, c.Env)
	return ""
}

// secretOf returns the Secret the container reads the variable name from,
// failing the test where it reads it from none.
func (c renderedContainer) secretOf(t *testing.T, name string) string {
	t.Helper()
	for _, e := range c.Env {
		if e.Name == name && e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
			return e.ValueFrom.SecretKeyRef.Name
		}
	}
	t.Fatalf("the container reads %s from no Secret: %+v", name, c.Env)
	return ""
}
