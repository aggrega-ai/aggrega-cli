// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Command imagetable generates the Compose importer's image table from the
// Compose Recipes of an aggrega-hub checkout: a table from image repository to
// the catalog Component a Recipe running an image of that repository implements,
// with that Component's labels as the row's traits, written as the Go source the
// importer embeds.
//
// It reads, of each Recipe under aggrega-catalog/recipe/compose/, the Component
// its spec.implements names, and the image of the container named main in its
// workload — the literal repository the image names, or the literal locals entry
// the image is in whole. It renders nothing and evaluates no template: a Recipe
// whose main image takes its repository from the Release, or composes it in any
// other way, contributes no row; so do the images of init containers, of the
// other containers beside main and of artifacts. Two Recipes naming one
// repository for one Component make one row; two naming it for different
// Components are refused, naming both, and no table is written, since a table
// naming one of them would decide which Component a service of that image is.
// The rows are ordered by repository, so one set of Recipes always writes the
// same bytes.
//
// Of a Recipe contributing a row it also reads, literally, what lets a Compose
// service running that image be drafted as an instance configured as the service
// is: the infra key a literal repository's tag is written from; the variables of
// the main container's environment written as one value of the Component; the
// variable an attached secret is read under where the attachment declares no key
// of its own, and the slot it is attached on; and the provides keys publishing a
// value of the Component, or the instance's own name, whole. Two Recipes of one
// Component saying different things of one key keep neither.
//
// Of each Component a row names it takes the labels, and nothing else, from the
// checkout's catalog (aggrega-catalog/component/, loaded with the schema
// libraries the Components read), as an import is handed them: the version a reference naming
// the Component without a version resolves to, its extends chain resolved. Every
// label but the category label is written as one of the row's traits, ordered by
// key; the category scores in a signal of its own, so it is not weighed again as a
// trait. A row naming a Component the checkout holds no version of is refused,
// naming the Recipe, and no table is written.
//
// The table names the aggrega-hub commit it was generated from: the checkout's
// HEAD, read through git, which is refused where the checkout's Compose Recipes,
// Components or schema libraries hold changes no commit records, unless -revision
// names what the table is read from.
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
	"go/format"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/importersocket"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/composeimport"
)

// Where a hub checkout keeps, beneath its root, its Compose Recipes, its
// Components and the schema libraries its Components may read.
var (
	recipeDir    = filepath.Join("aggrega-catalog", "recipe", "compose")
	componentDir = filepath.Join("aggrega-catalog", "component")
	libraryDir   = filepath.Join("aggrega-catalog", "schemalibrary")
)

// categoryLabel is the label a Component's category is written under, which the
// importer's scorer weighs in a signal of its own.
const categoryLabel = "component.aggrega.ai/category"

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
	if info, err := os.Stat(*hub); err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "imagetable: no aggrega-hub checkout at %s; check out aggrega-hub there, or name the "+
			"checkout with -hub\n", *hub)
		os.Exit(1)
	}
	if err := run(*hub, *out, *revision, *report); err != nil {
		fmt.Fprintln(os.Stderr, "imagetable: "+err.Error())
		os.Exit(1)
	}
}

// run generates the table from the checkout at hub and writes it to out.
func run(hub, out, revision string, report bool) error {
	rows, notes, err := generateFromHub(hub)
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
	content, err := render(rows, revision)
	if err != nil {
		return err
	}
	if out == "" {
		_, err = os.Stdout.Write(content)
		return err
	}
	return os.WriteFile(out, content, 0o644)
}

// row is one row of the table: a repository, the Component it maps to, the
// traits the Component's labels give it, which are absent until withTraits
// writes them, and what the Recipes running an image of the repository say of
// how a service running it is configured.
type row struct {
	repository, component string
	traits                map[string]string
	translation
}

// translation is what the Compose Recipes of one Component, read literally, say
// of the service they write, so a Compose service running an image of theirs
// can be drafted as an instance of that Component configured as the service is:
//
//   - tag, the infra key whose value is the tag of the main container's image,
//     where the image is written as a literal repository, a colon and that key;
//   - values, each variable of the main container's environment whose value is
//     one value of the Component and nothing else, mapped to that value's key;
//   - secrets, each variable a Recipe reads an attached secret's value under
//     where the attachment declares no key of its own, mapped to the slot the
//     secret is attached on;
//   - provides, each value of the Component a provides key publishes whole,
//     mapped to that key; and host, the provides key publishing the instance's
//     own name, the address a peer reaches it at.
//
// Where two Recipes of one Component say different things of one key, neither
// is kept, so the table never chooses between them.
type translation struct {
	tag      string
	values   map[string]string
	secrets  map[string]string
	provides map[string]string
	host     string
}

// generateFromHub generates the table's rows from the Compose Recipes of the
// checkout at hub, each with the traits its Component's labels give it there, and
// one note per Recipe saying what it contributed.
func generateFromHub(hub string) ([]row, []string, error) {
	rows, notes, err := generate(filepath.Join(hub, recipeDir))
	if err != nil {
		return nil, notes, err
	}
	labels, err := componentLabels(hub)
	if err != nil {
		return nil, notes, err
	}
	rows, err = withTraits(rows, labels)
	return rows, notes, err
}

// componentLabels returns the labels of each Component the catalog of the
// checkout at hub holds, keyed by name, exactly as an import is handed them: the
// catalog is loaded as the compiler loads one and projected as the importer
// socket projects it, so the version a name resolves to and the extends chain a
// Component inherits labels through are the import's own.
func componentLabels(hub string) (map[string]map[string]string, error) {
	dirs := []string{filepath.Join(hub, componentDir)}
	if info, err := os.Stat(filepath.Join(hub, libraryDir)); err == nil && info.IsDir() {
		dirs = append(dirs, filepath.Join(hub, libraryDir))
	}
	cat, err := catalog.LoadAll("", dirs...)
	if err != nil {
		return nil, fmt.Errorf("load the Components of %s: %w", hub, err)
	}
	out := map[string]map[string]string{}
	if view := importersocket.ProjectCatalog(cat); view != nil {
		for _, c := range view.Components {
			out[c.Name] = c.Labels
		}
	}
	return out, nil
}

// withTraits returns the rows, each carrying as its traits every label the
// Component it names carries in labels but the category label. A row naming a
// Component labels holds no entry for is refused, every such row named, and no
// rows are returned.
func withTraits(rows []row, labels map[string]map[string]string) ([]row, error) {
	out := make([]row, 0, len(rows))
	var missing []string
	for _, r := range rows {
		componentLabels, ok := labels[r.component]
		if !ok {
			missing = append(missing, "the image repository "+r.repository+" maps to "+r.component)
			continue
		}
		var traits map[string]string
		for k, v := range componentLabels {
			if k == categoryLabel {
				continue
			}
			if traits == nil {
				traits = map[string]string{}
			}
			traits[k] = v
		}
		r.traits = traits
		out = append(out, r)
	}
	if len(missing) > 0 {
		return nil, errors.New(strings.Join(missing, "; ") + ", which the hub's catalog holds no version of, so " +
			"no row can carry its labels and no table is written")
	}
	return out, nil
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
	tags := map[string]*string{}
	translations := map[string]*translation{}
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
		r, err := readRecipe(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", rel, err)
		}
		if len(r.repositories) == 0 {
			notes = append(notes, rel+": no row, since "+r.why)
			continue
		}
		notes = append(notes, rel+": "+strings.Join(r.repositories, ", ")+" -> "+r.component)
		if prior, seen := translations[r.component]; seen {
			prior.merge(r.translation)
		} else {
			t := r.translation
			translations[r.component] = &t
		}
		for _, repository := range r.repositories {
			namedBy[repository] = append(namedBy[repository], rel+" implements "+r.component)
			if prior, seen := tags[repository]; !seen {
				tag := r.tag
				tags[repository] = &tag
			} else if *prior != r.tag {
				*prior = ""
			}
			if prior, seen := byRepository[repository]; seen && prior != r.component {
				conflicts[repository] = true
				continue
			}
			byRepository[repository] = r.component
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
		t := *translations[byRepository[r]]
		t.tag = *tags[r]
		rows = append(rows, row{repository: r, component: byRepository[r], translation: t})
	}
	return rows, notes, nil
}

// merge keeps in t what t and o, read from two Recipes of one Component, say
// alike: each key both map to one thing, or one of them alone maps; a key they
// map to two things is dropped, and so is host where they name two.
func (t *translation) merge(o translation) {
	for _, m := range []struct{ into, from *map[string]string }{{&t.values, &o.values}, {&t.secrets, &o.secrets},
		{&t.provides, &o.provides}} {
		*m.into = mergedMap(*m.into, *m.from)
	}
	if t.host != o.host {
		if t.host == "" {
			t.host = o.host
		} else if o.host != "" {
			t.host = ""
		}
	}
}

// mergedMap is a and b together, a key the two map to different things dropped.
func mergedMap(a, b map[string]string) map[string]string {
	out := map[string]string{}
	dropped := map[string]bool{}
	for _, m := range []map[string]string{a, b} {
		for k, v := range m {
			if prior, seen := out[k]; seen && prior != v {
				dropped[k] = true
			}
			out[k] = v
		}
	}
	for k := range dropped {
		delete(out, k)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// recipe is what the tool reads of a Recipe document.
type recipe struct {
	Spec struct {
		Implements string         `yaml:"implements"`
		Workload   string         `yaml:"workload"`
		Locals     map[string]any `yaml:"locals"`
		Provides   map[string]any `yaml:"provides"`
	} `yaml:"spec"`
}

// readRecipeResult is what the tool reads of one Recipe: the Component it
// implements, less its version; the repositories its main container's image
// names literally — or, where it names none, why; and its translation.
type readRecipeResult struct {
	component    string
	repositories []string
	why          string
	translation
}

// readRecipe reads one Recipe document.
func readRecipe(raw []byte) (readRecipeResult, error) {
	var r recipe
	if err := yaml.Unmarshal(raw, &r); err != nil {
		return readRecipeResult{}, err
	}
	component, _, _ := strings.Cut(r.Spec.Implements, ":")
	if component == "" {
		return readRecipeResult{}, errors.New("the Recipe implements no Component")
	}
	out := readRecipeResult{component: component}
	image, ok := mainImage(r.Spec.Workload)
	if !ok {
		out.why = "its workload writes no image for a container named main"
		return out, nil
	}
	out.repositories, out.why = repositoriesOf(image, r.Spec.Locals)
	out.tag = tagKeyOf(image)
	out.values = mainValues(r.Spec.Workload)
	out.secrets = secretVariables(r.Spec.Workload)
	out.provides, out.host = publishedValues(r.Spec.Provides)
	return out, nil
}

// infraKey and valueKey match an action reading one infra key or one value of
// the Component whole, capturing the key.
var (
	infraKey = regexp.MustCompile(`^\.infra\.([a-z][a-z0-9_]*)$`)
	valueKey = regexp.MustCompile(`^\.values\.([a-z][a-z0-9_]*)$`)
)

// tagKeyOf returns the infra key the tag of an image value is, where some branch
// of the template writes a literal repository, a colon and an action reading
// that key whole — postgres:{{ .infra.engine_version }} — and every branch
// doing so reads one key; empty otherwise.
func tagKeyOf(image string) string {
	parts := splitActions(image)
	key := ""
	for i, p := range parts {
		if p.action || !strings.HasSuffix(p.text, ":") || i+1 >= len(parts) || !parts[i+1].action {
			continue
		}
		m := infraKey.FindStringSubmatch(parts[i+1].text)
		if m == nil {
			continue
		}
		if key != "" && key != m[1] {
			return ""
		}
		key = m[1]
	}
	return key
}

// variableLine matches one variable of a container's environment written on one
// line as NAME: VALUE, the value quoted or not, capturing both.
var variableLine = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*):\s*(.*)$`)

// mainValues returns each variable of the main container's environment — the
// lines written one level beneath its variables key — whose value is one action
// reading one value of the Component whole, {{ .values.KEY }}, mapped to KEY.
// Lines holding a template action alone are passed over, as mainImage passes
// them over.
func mainValues(workload string) map[string]string {
	out := map[string]string{}
	containers, main, child, variables, entry := -1, -1, -1, -1, -1
	for _, raw := range strings.Split(workload, "\n") {
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
			continue
		case indent <= containers:
			return nilIfEmpty(out)
		case main < 0:
			if trimmed == "main:" {
				main = indent
			}
			continue
		case indent <= main:
			return nilIfEmpty(out)
		}
		if child < 0 {
			child = indent
		}
		switch {
		case indent == child:
			variables, entry = -1, -1
			if trimmed == "variables:" {
				variables = indent
			}
		case variables >= 0 && indent > variables:
			if entry < 0 {
				entry = indent
			}
			if indent != entry {
				continue
			}
			m := variableLine.FindStringSubmatch(trimmed)
			if m == nil {
				continue
			}
			parts := splitActions(unquote(strings.TrimSpace(m[2])))
			if len(parts) != 1 || !parts[0].action {
				continue
			}
			if k := valueKey.FindStringSubmatch(parts[0].text); k != nil {
				out[m[1]] = k[1]
			}
		}
	}
	return nilIfEmpty(out)
}

// passwordDefault matches the line a Recipe reads an attached secret's key on,
// taking the attachment's values.key and, where it declares none, a default
// variable — default "NAME" ( … "key" … ) — capturing that variable; and slotOf
// the slot of the Component's own features the line reads the attachment from,
// as .features.SLOT or index .features "SLOT", never a connection's.
var (
	passwordDefault = regexp.MustCompile(`default "([A-Za-z_][A-Za-z0-9_]*)" .*"key"`)
	slotOf          = regexp.MustCompile(`(?:^|[^$\w])\.features\.([a-z][a-z0-9_]*)\b|index \.features "([a-z][a-z0-9_]*)"`)
)

// secretVariables returns each variable a Recipe reads an attached secret's
// value under where the attachment declares no key of its own, mapped to the
// slot of the Component's own features the secret is attached on, read from the
// line choosing that key. A line naming no such slot, or two, is passed over.
func secretVariables(workload string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(workload, "\n") {
		m := passwordDefault.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		slots := map[string]bool{}
		for _, s := range slotOf.FindAllStringSubmatch(line, -1) {
			slots[s[1]+s[2]] = true
		}
		if len(slots) == 1 {
			for s := range slots {
				out[m[1]] = s
			}
		}
	}
	return nilIfEmpty(out)
}

// publishedValues returns, of a Recipe's provides, each value of the Component a
// key publishes whole — {{ .values.KEY }} — mapped to the provides key, and the
// key publishing the instance's own name, {{ .context.instance_id }}, empty where
// none does or two do.
func publishedValues(provides map[string]any) (map[string]string, string) {
	out := map[string]string{}
	host := ""
	hosts := 0
	for _, key := range slices.Sorted(maps.Keys(provides)) {
		text, ok := provides[key].(string)
		if !ok {
			continue
		}
		parts := splitActions(strings.TrimSpace(text))
		if len(parts) != 1 || !parts[0].action {
			continue
		}
		if parts[0].text == ".context.instance_id" {
			host, hosts = key, hosts+1
			continue
		}
		if m := valueKey.FindStringSubmatch(parts[0].text); m != nil {
			if _, seen := out[m[1]]; seen {
				delete(out, m[1])
				continue
			}
			out[m[1]] = key
		}
	}
	if hosts != 1 {
		host = ""
	}
	return nilIfEmpty(out), host
}

// nilIfEmpty returns m, nil where it holds nothing, so an empty translation is
// written as none. DELIBERATE NEAR-DUPLICATE of the provider core's unexported
// helper of that name: this generator is a command of its own, run at build time
// beside the importer, and imports nothing from a provider plugin's package.
func nilIfEmpty(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
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
// checkout whose Compose Recipes, Components or schema libraries hold changes no
// commit records.
func hubRevision(hub string) (string, error) {
	head, err := exec.Command("git", "-C", hub, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("read the commit %s has checked out (pass -revision to name it): %w", hub, err)
	}
	status, err := exec.Command("git", "-C", hub, "status", "--porcelain", "--", filepath.ToSlash(recipeDir),
		filepath.ToSlash(componentDir), filepath.ToSlash(libraryDir)).Output()
	if err != nil {
		return "", fmt.Errorf("read whether the Compose Recipes and Components of %s hold changes: %w", hub, err)
	}
	if len(bytes.TrimSpace(status)) > 0 {
		return "", fmt.Errorf("the Compose Recipes, Components or schema libraries of %s hold changes no commit "+
			"records, so no commit names what the table is read from (pass -revision to name it)", hub)
	}
	return strings.TrimSpace(string(head)), nil
}

// render writes the rows as the Go source the importer embeds, naming revision
// as the revision they were read from, formatted as gofmt formats it. A row's
// traits are written ordered by key, so one set of rows always writes the same
// bytes.
func render(rows []row, revision string) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("// SPDX-License-Identifier: Apache-2.0\n")
	b.WriteString("// Copyright 2026 Cognitio s.r.l.\n\n")
	b.WriteString("// Code generated by imagetable from the Compose Recipes and Components of aggrega-hub. DO NOT EDIT.\n\n")
	b.WriteString("package composeimport\n\n")
	b.WriteString("// TableRevision is the aggrega-hub revision whose Compose Recipes and\n")
	b.WriteString("// Components the image table was generated from.\n")
	fmt.Fprintf(&b, "const TableRevision = %q\n\n", revision)
	b.WriteString("// imageTable maps each image repository the main container of a Compose\n")
	b.WriteString("// Recipe of the hub names literally to the Component the Recipe implements,\n")
	b.WriteString("// with every label of that Component but its category as the row's traits,\n")
	b.WriteString("// and what its Recipes say of how a service running the image is\n")
	b.WriteString("// configured, ordered by repository.\n")
	b.WriteString("var imageTable = []imageRow{\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "\t{Repository: %q, Component: %q,", r.repository, r.component)
		writeMap(&b, "Traits", r.traits)
		if r.tag != "" {
			fmt.Fprintf(&b, " Tag: %q,", r.tag)
		}
		writeMap(&b, "Values", r.values)
		writeMap(&b, "Secrets", r.secrets)
		writeMap(&b, "Provides", r.provides)
		if r.host != "" {
			fmt.Fprintf(&b, " Host: %q,", r.host)
		}
		b.WriteString("},\n")
	}
	b.WriteString("}\n")
	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format the generated table: %w", err)
	}
	return formatted, nil
}

// writeMap writes m as the field named field of a row, its keys ordered, so one
// set of rows always writes the same bytes; nothing where m holds nothing.
func writeMap(b *bytes.Buffer, field string, m map[string]string) {
	if len(m) == 0 {
		return
	}
	fmt.Fprintf(b, " %s: map[string]string{\n", field)
	for _, k := range slices.Sorted(maps.Keys(m)) {
		fmt.Fprintf(b, "\t\t%q: %q,\n", k, m[k])
	}
	b.WriteString("\t},")
}
