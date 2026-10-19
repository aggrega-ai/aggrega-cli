// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Command imagetable generates the Compose importer's image table from the
// Compose Recipes of an aggrega-hub checkout: a table from image repository to
// the catalog Component a Recipe running an image of that repository implements,
// written as the Go source the importer embeds.
//
// It reads, of each Recipe under aggrega-catalog/recipe/compose/, two things and
// nothing else: the Component its spec.implements names, and the image of the
// container named main in its workload — the literal repository the image names,
// or the literal locals entry the image is in whole. It renders nothing and
// evaluates no template: a Recipe whose main image takes its repository from the
// Release, or composes it in any other way, contributes no row; so do the images
// of init containers, of the other containers beside main and of artifacts. Two
// Recipes naming one repository for one Component make one row; two naming it
// for different Components are refused, naming both, and no table is written,
// since a table naming one of them would decide which Component a service of that
// image is. The rows are ordered by repository, so one set of Recipes always
// writes the same bytes.
//
// The table names the aggrega-hub commit it was generated from: the checkout's
// HEAD, read through git, which is refused where the checkout's Compose Recipes
// hold changes no commit records, unless -revision names what the table is read
// from.
//
// Usage:
//
//	go run ./plugins/internal/composeimport/imagetable -hub ../aggrega-hub -out plugins/internal/composeimport/imagetable_gen.go
//	go run ./plugins/internal/composeimport/imagetable -hub DIR -revision NAME -report
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/composeimport"
)

// recipeDir is where a hub checkout keeps its Compose Recipes, beneath its root.
var recipeDir = filepath.Join("aggrega-catalog", "recipe", "compose")

func main() {
	hub := flag.String("hub", "", "the aggrega-hub checkout whose Compose Recipes the table is generated from (required)")
	out := flag.String("out", "", "the file the table is written to; standard output where empty")
	revision := flag.String("revision", "", "what the table names as the revision it was read from; the checkout's HEAD where empty")
	report := flag.Bool("report", false, "print, on standard error, what each Recipe contributed and why")
	flag.Parse()
	if *hub == "" {
		fmt.Fprintln(os.Stderr, "imagetable: -hub names no checkout")
		os.Exit(2)
	}
	if err := run(*hub, *out, *revision, *report); err != nil {
		fmt.Fprintln(os.Stderr, "imagetable: "+err.Error())
		os.Exit(1)
	}
}

// run generates the table from the checkout at hub and writes it to out.
func run(hub, out, revision string, report bool) error {
	rows, notes, err := generate(filepath.Join(hub, recipeDir))
	if report {
		for _, n := range notes {
			fmt.Fprintln(os.Stderr, n)
		}
	}
	if err != nil {
		return err
	}
	if revision == "" {
		if revision, err = hubRevision(hub); err != nil {
			return err
		}
	}
	content := render(rows, revision)
	if out == "" {
		_, err = os.Stdout.Write(content)
		return err
	}
	return os.WriteFile(out, content, 0o644)
}

// row is one row of the table.
type row struct {
	repository, component string
}

// generate reads every Recipe under dir, by the order of its path, and returns
// the table's rows ordered by repository and one note per Recipe saying what it
// contributed. Two Recipes naming one repository for different Components are
// refused, every such repository named with the Recipes naming it.
func generate(dir string) ([]row, []string, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*", "*", "recipe.yaml"))
	if err != nil {
		return nil, nil, err
	}
	if len(paths) == 0 {
		return nil, nil, fmt.Errorf("%s holds no Compose Recipe", dir)
	}
	slices.Sort(paths)
	byRepository := map[string]string{}
	namedBy := map[string][]string{}
	conflicts := map[string]bool{}
	var notes []string
	for _, p := range paths {
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, err
		}
		component, repositories, why, err := readRecipe(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", rel, err)
		}
		if len(repositories) == 0 {
			notes = append(notes, rel+": no row, since "+why)
			continue
		}
		notes = append(notes, rel+": "+strings.Join(repositories, ", ")+" -> "+component)
		for _, r := range repositories {
			namedBy[r] = append(namedBy[r], rel+" implements "+component)
			if prior, seen := byRepository[r]; seen && prior != component {
				conflicts[r] = true
				continue
			}
			byRepository[r] = component
		}
	}
	if len(conflicts) > 0 {
		var lines []string
		for _, r := range slices.Sorted(maps.Keys(conflicts)) {
			lines = append(lines, "the image repository "+r+" is named by "+strings.Join(namedBy[r], " and by "))
		}
		return nil, notes, errors.New(strings.Join(lines, "; ") + "; the table maps a repository to one Component, " +
			"so no table is written")
	}
	rows := make([]row, 0, len(byRepository))
	for _, r := range slices.Sorted(maps.Keys(byRepository)) {
		rows = append(rows, row{repository: r, component: byRepository[r]})
	}
	return rows, notes, nil
}

// recipe is what the tool reads of a Recipe document.
type recipe struct {
	Spec struct {
		Implements string         `yaml:"implements"`
		Workload   string         `yaml:"workload"`
		Locals     map[string]any `yaml:"locals"`
	} `yaml:"spec"`
}

// readRecipe returns the Component a Recipe implements, less its version, and the
// repositories its main container's image names literally — or, where it names
// none, why.
func readRecipe(raw []byte) (string, []string, string, error) {
	var r recipe
	if err := yaml.Unmarshal(raw, &r); err != nil {
		return "", nil, "", err
	}
	component, _, _ := strings.Cut(r.Spec.Implements, ":")
	if component == "" {
		return "", nil, "", errors.New("the Recipe implements no Component")
	}
	image, ok := mainImage(r.Spec.Workload)
	if !ok {
		return component, nil, "its workload writes no image for a container named main", nil
	}
	repositories, why := repositoriesOf(image, r.Spec.Locals)
	return component, repositories, why, nil
}

// mainImage returns the value the workload template writes for the image of the
// container named main: the image line written one level beneath main, which is
// written one level beneath containers. Lines holding a template action alone
// are passed over, so a block an action wraps is read as its lines are written.
func mainImage(workload string) (string, bool) {
	lines := strings.Split(workload, "\n")
	containers, main, child := -1, -1, -1
	for _, raw := range lines {
		line := strings.TrimRight(raw, " \r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "{{") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		switch {
		case containers < 0:
			if trimmed == "containers:" {
				containers = indent
			}
		case indent <= containers:
			return "", false
		case main < 0:
			if trimmed == "main:" {
				main = indent
			}
		case indent <= main:
			return "", false
		default:
			if child < 0 {
				child = indent
			}
			if indent == child && strings.HasPrefix(trimmed, "image:") {
				return unquote(strings.TrimSpace(strings.TrimPrefix(trimmed, "image:"))), true
			}
		}
	}
	return "", false
}

// unquote returns value without the one pair of quotes enclosing it whole, as
// the template writes it before it is rendered.
func unquote(value string) string {
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		return value[1 : len(value)-1]
	}
	return value
}

// repositoriesOf returns the repositories an image value names literally, each
// once and in the order written, or why it names none:
//
//   - a value holding no template action is a reference, read whole;
//   - a value that is one action reading a locals entry, {{ .locals.NAME }}, is
//     the reference that entry holds, where the Recipe writes it literally;
//   - in any other value, a literal run of text that a branch of the template
//     writes first — opening the value, or following an if, else or with action
//     — is read where it is a whole reference, ending the value or followed by
//     the branch's else or end, or a repository whose tag the action following
//     it writes, ending in a colon or an at sign.
//
// Every other part of a value — what an action writes, and literal text another
// action's output precedes — is not read, so a value whose repository a template
// composes names none.
func repositoriesOf(image string, locals map[string]any) ([]string, string) {
	parts := splitActions(image)
	if len(parts) == 1 && !parts[0].action {
		if r, ok := composeimport.Repository(parts[0].text); ok {
			return []string{r}, ""
		}
		return nil, "its main image " + quoted(image) + " is not a reference"
	}
	if len(parts) == 1 && parts[0].action {
		field := strings.TrimSpace(parts[0].text)
		if field == ".infra.image" {
			return nil, "its main image is the whole image the Release names"
		}
		if name, ok := strings.CutPrefix(field, ".locals."); ok {
			value, isString := locals[name].(string)
			if !isString || strings.Contains(value, "{{") {
				return nil, "its main image is the locals entry " + name + ", which is not a literal reference"
			}
			if r, ok := composeimport.Repository(value); ok {
				return []string{r}, ""
			}
			return nil, "its main image is the locals entry " + name + ", which is not a reference"
		}
		return nil, "its main image is " + quoted(image) + ", which a template composes"
	}
	var out []string
	for i, p := range parts {
		if p.action || !opensBranch(parts, i) {
			continue
		}
		text := strings.TrimSpace(p.text)
		next := i + 1
		var candidate string
		switch {
		case strings.HasSuffix(text, ":") || strings.HasSuffix(text, "@"):
			if next < len(parts) && parts[next].action && !closesBranch(parts[next]) {
				candidate = text
			}
		case next == len(parts) || closesBranch(parts[next]):
			candidate = text
		}
		if candidate == "" {
			continue
		}
		if r, ok := composeimport.Repository(candidate); ok && !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return nil, "its main image is " + quoted(image) + ", in which no branch writes a literal repository"
	}
	return out, ""
}

// part is one piece of a template value: an action's text, less its braces and
// its trim markers, or a run of literal text.
type part struct {
	text   string
	action bool
}

// splitActions splits a template value into its actions and its literal runs, in
// order. An action left open runs to the end of the value.
func splitActions(value string) []part {
	var out []part
	for value != "" {
		open := strings.Index(value, "{{")
		if open < 0 {
			out = append(out, part{text: value})
			break
		}
		if open > 0 {
			out = append(out, part{text: value[:open]})
		}
		rest := value[open+2:]
		end := strings.Index(rest, "}}")
		if end < 0 {
			end = len(rest)
		}
		inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(rest[:end]), "-"), "-"))
		out = append(out, part{text: inner, action: true})
		if end+2 > len(rest) {
			break
		}
		value = rest[end+2:]
	}
	return out
}

// opensBranch reports whether the literal run at i is what a branch of the
// template writes first: it opens the value, or follows an if, else or with
// action.
func opensBranch(parts []part, i int) bool {
	if i == 0 {
		return true
	}
	prev := parts[i-1]
	if !prev.action {
		return false
	}
	return prev.text == "else" || strings.HasPrefix(prev.text, "if ") || strings.HasPrefix(prev.text, "else ") ||
		strings.HasPrefix(prev.text, "with ")
}

// closesBranch reports whether an action ends the branch before it: an else or
// an end.
func closesBranch(p part) bool {
	return p.action && (p.text == "end" || p.text == "else" || strings.HasPrefix(p.text, "else "))
}

// quoted writes a value into a note.
func quoted(value string) string {
	return fmt.Sprintf("%q", value)
}

// hubRevision returns the commit the checkout at hub has checked out, refusing a
// checkout whose Compose Recipes hold changes no commit records.
func hubRevision(hub string) (string, error) {
	head, err := exec.Command("git", "-C", hub, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("read the commit %s has checked out (pass -revision to name it): %w", hub, err)
	}
	status, err := exec.Command("git", "-C", hub, "status", "--porcelain", "--", filepath.ToSlash(recipeDir)).Output()
	if err != nil {
		return "", fmt.Errorf("read whether the Compose Recipes of %s hold changes: %w", hub, err)
	}
	if len(bytes.TrimSpace(status)) > 0 {
		return "", fmt.Errorf("the Compose Recipes of %s hold changes no commit records, so no commit names what the "+
			"table is read from (pass -revision to name it)", hub)
	}
	return strings.TrimSpace(string(head)), nil
}

// render writes the rows as the Go source the importer embeds, naming revision
// as the revision they were read from.
func render(rows []row, revision string) []byte {
	var b bytes.Buffer
	b.WriteString("// SPDX-License-Identifier: Apache-2.0\n")
	b.WriteString("// Copyright 2026 Cognitio S.r.l.\n\n")
	b.WriteString("// Code generated by imagetable from the Compose Recipes of aggrega-hub. DO NOT EDIT.\n\n")
	b.WriteString("package composeimport\n\n")
	b.WriteString("// TableRevision is the aggrega-hub revision whose Compose Recipes the image\n")
	b.WriteString("// table was generated from.\n")
	fmt.Fprintf(&b, "const TableRevision = %q\n\n", revision)
	b.WriteString("// imageTable maps each image repository the main container of a Compose\n")
	b.WriteString("// Recipe of the hub names literally to the Component the Recipe implements,\n")
	b.WriteString("// ordered by repository.\n")
	b.WriteString("var imageTable = []imageRow{\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "\t{Repository: %q, Component: %q},\n", r.repository, r.component)
	}
	b.WriteString("}\n")
	return b.Bytes()
}
