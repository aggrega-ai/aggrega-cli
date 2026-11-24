// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// PredOp is how a predicate of a row's When tests the value at its path.
type PredOp string

// The operators of a predicate.
const (
	// OpEquals holds where the value at the path reads as the predicate's
	// Value.
	OpEquals PredOp = "equals"
	// OpPrefix holds where the value at the path reads as text beginning with
	// the predicate's Value.
	OpPrefix PredOp = "prefix"
	// OpContains holds where the value at the path reads as text containing
	// the predicate's Value.
	OpContains PredOp = "contains"
	// OpPresent holds where the node writes a value other than null at the
	// path.
	OpPresent PredOp = "present"
	// OpAbsent holds where OpPresent does not.
	OpAbsent PredOp = "absent"
)

// Pred is one predicate of a row's When: a test of the value a node writes at
// Path, a property path whose segments are members' keys and arrays' indices
// in decimal.
type Pred struct {
	Path  []string
	Op    PredOp
	Value string
}

// Conv is how a placed value is converted on its way to its key: empty for the
// value as is, "string" for its text, "integer" for an integer, and
// "unit:<suffix>" for a number written with that unit suffix.
type Conv string

// Placement places the value a node writes at a property path, or the
// attribute a consumer reads, under a key of the instance.
type Placement struct {
	// From is the property or attribute path the value is read at.
	From []string
	// Key is the key the value is placed under: a values or infra key, an
	// infra.overrides key, a credential slot, or a provides key.
	Key string
	// Conv converts the value on its way.
	Conv Conv
}

// Fold is a child or extension resource a row writes into the instance of its
// parent, or makes an instance of its own row.
type Fold struct {
	// Type is the child's or extension's type.
	Type string
	// Key is where the child is written in the instance, such as
	// values.database.
	Key []string
	// From is the path of the child that is written there.
	From []string
	// One makes the fold take one value: the first child by ID is written, and
	// each other is a resource not carried.
	One bool
	// Instances makes each child an instance of this row of its own, each
	// folding the parent.
	Instances bool
	// NotCarried is why the Recipe takes none of the child, empty where it
	// takes it.
	NotCarried string
}

// Equivalence states that a property or attribute path of the raw form and a
// parameter or output path of the module form hold one value, so one row reads
// a node of either form.
type Equivalence struct {
	Raw, Module []string
}

// EnvList is where a compute resource lists its environment: the array at
// Path, each of whose items names a variable under the member Name and holds
// its value under the member Value.
type EnvList struct {
	Path        []string
	Name, Value string
}

// Row maps the nodes of one type, where its When holds, to a catalog
// Component and the Recipe realizing it on the format's own target, and says
// where the node's properties go in the instance. Every path of a row — of its
// When, its placements and its Env — is written in Form; [Row.For] reads them
// in another.
//
// A row is read and never changed: the rows [Table.Rows], [Table.Resolve] and
// [Row.For] return share slices and maps with the table's own rows and with
// each other.
type Row struct {
	// Key is the vendor type of the nodes the row maps, matched against a
	// node's type regardless of case.
	Key string
	// Component is the catalog Component the nodes are instances of.
	Component string
	// Category is the Component's category label.
	Category string
	// Recipe is the Recipe realizing Component on the format's own target.
	Recipe string
	// When are the predicates a node of type Key meets for the row to map it,
	// all of them; none maps every node of the type.
	When []Pred
	// Traits are the Component's other labels, each weighed against a
	// candidate's labels as the importer kit weighs a mapping's traits.
	Traits map[string]string
	// Form is the form the row's paths are written in.
	Form Form
	// Values and Infra place properties under values and infra keys,
	// Overrides under infra.overrides keys, and Secrets under credential
	// slots; Provides place attributes a consumer reads under provides keys.
	Values, Infra, Overrides, Secrets, Provides []Placement
	// Folds are the child and extension resources the row folds.
	Folds []Fold
	// Equivalences are the paths the raw form and the module form hold one
	// value at.
	Equivalences []Equivalence
	// Env is where a compute resource lists its environment, nil for a
	// resource that lists none.
	Env *EnvList
	// Supersedes gives, for a field a hand row sets over a value the generated
	// row of its key and When sets otherwise, why the hand value supersedes the
	// generated one. A field is named as Rows names it in a [Contradiction]:
	// Component, Category, Recipe, Form or Env; Traits.<label>; Values.<key>,
	// Infra.<key>, Overrides.<key>, Secrets.<slot> or Provides.<key>;
	// Folds.<type>; Equivalences.<raw path>, its segments joined by dots.
	Supersedes map[string]string
	// Origin is where the row comes from: "generated <Recipe path>", "hand",
	// or a generated row's origin followed by "and hand" where Rows merged a
	// hand row into it.
	Origin string
}

// NotImported is a Recipe of the format's target that no row reaches, with
// the reason.
type NotImported struct {
	Recipe, Reason string
}

// ImageRow maps a compute node whose main container runs an image of
// Repository to the container product Component and Recipe whose literal image
// default names that repository; the image's tag is written under TagKey.
type ImageRow struct {
	Repository, Component, Recipe, TagKey string
}

// Table is a format's mapping table.
type Table struct {
	// Revision is the catalog revision the generated rows are read from.
	Revision string
	// Generated are the rows read from the catalog's Recipes, and Hand the
	// rows written by hand, each keyed like a generated row.
	Generated, Hand []Row
	// NotImported are the Recipes of the format's target no row reaches.
	NotImported []NotImported
	// Images are the container product rows.
	Images []ImageRow
}

// Contradiction is a field a hand row sets over a different value of the
// generated row of its key and When, with no reason in its Supersedes.
type Contradiction struct {
	// Key and When identify the two rows.
	Key  string
	When []Pred
	// Field is the field, named as Row.Supersedes names it.
	Field string
	// Generated and Hand are the field's two values, as text.
	Generated, Hand string
}

// Error names the rows, the field and both values, and what to write.
func (c *Contradiction) Error() string {
	return fmt.Sprintf("the hand row of %s%s sets %s to %q where the generated row sets %q, with no reason in its "+
		"Supersedes: write under Supersedes[%q] why the hand value supersedes the generated one, or remove it",
		c.Key, whenText(c.When), c.Field, c.Hand, c.Generated, c.Field)
}

// DuplicateRow is a key and When two rows of one origin share, so the second
// could never be chosen.
type DuplicateRow struct {
	Key  string
	When []Pred
	// Origin is "generated" or "hand".
	Origin string
}

// Error names the key, the When and the rows' origin.
func (d *DuplicateRow) Error() string {
	return fmt.Sprintf("two %s rows map %s%s: keep one row for each key and When", d.Origin, d.Key,
		whenText(d.When))
}

// Rows returns the table's rows: each generated row in order, the hand row of
// its key and When merged into it, then each hand row no generated row shares
// a key and When with, in order. Two keys are one where they differ only in
// case, and two Whens are one where they hold the same predicates in any
// order, their paths and values differing only in case. A merged row takes,
// field by field, the hand value where the hand row sets one and the
// generated value otherwise; a list field is merged entry by entry, an entry
// matched by its key, and a hand entry of a key the generated row does not
// set is added after the generated ones. A hand value set over a different
// generated one needs a written reason in the hand row's Supersedes: without
// one, Rows returns a [*Contradiction] naming the key, the field and both
// values. Two rows of one origin sharing a key and When return a
// [*DuplicateRow]. A merged row shares the slices and maps of the fields it
// takes with the generated and the hand row, and a row left unmerged is the
// table's own: none is to be changed.
func (t Table) Rows() ([]Row, error) {
	if err := duplicates(t.Generated, "generated"); err != nil {
		return nil, err
	}
	if err := duplicates(t.Hand, "hand"); err != nil {
		return nil, err
	}
	handAt := make(map[string]int, len(t.Hand))
	for i, h := range t.Hand {
		handAt[identity(h)] = i
	}
	merged := make([]bool, len(t.Hand))
	out := make([]Row, 0, len(t.Generated)+len(t.Hand))
	for _, g := range t.Generated {
		i, ok := handAt[identity(g)]
		if !ok {
			out = append(out, g)
			continue
		}
		merged[i] = true
		row, err := merge(g, t.Hand[i])
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	for i, h := range t.Hand {
		if !merged[i] {
			out = append(out, h)
		}
	}
	return out, nil
}

// duplicates returns a *DuplicateRow for the first row of rows whose key and
// When an earlier row shares.
func duplicates(rows []Row, origin string) error {
	seen := make(map[string]bool, len(rows))
	for _, r := range rows {
		id := identity(r)
		if seen[id] {
			return &DuplicateRow{Key: r.Key, When: r.When, Origin: origin}
		}
		seen[id] = true
	}
	return nil
}

// identity is the text of a row's key and When, lowercased, its predicates in
// sorted order, so two rows whose keys, paths and values differ only in case,
// and whose Whens hold the same predicates in any order, share one identity.
func identity(r Row) string {
	preds := make([]string, len(r.When))
	for i, p := range r.When {
		preds[i] = strings.ToLower(strings.Join(p.Path, "\x1f")) + "\x1e" + string(p.Op) + "\x1e" +
			strings.ToLower(p.Value)
	}
	slices.Sort(preds)
	return strings.ToLower(r.Key) + "\x1d" + strings.Join(preds, "\x1d")
}

// merger merges the fields of a hand row into the generated row of its key
// and When.
type merger struct {
	gen, hand Row
}

// merge returns the generated row g with the hand row h merged into it.
func merge(g, h Row) (Row, error) {
	m := merger{gen: g, hand: h}
	out := Row{Key: g.Key, When: g.When, Supersedes: h.Supersedes, Origin: "hand"}
	if g.Origin != "" {
		out.Origin = g.Origin + " and hand"
	}
	var err error
	if out.Component, err = m.scalar("Component", g.Component, h.Component); err != nil {
		return Row{}, err
	}
	if out.Category, err = m.scalar("Category", g.Category, h.Category); err != nil {
		return Row{}, err
	}
	if out.Recipe, err = m.scalar("Recipe", g.Recipe, h.Recipe); err != nil {
		return Row{}, err
	}
	form, err := m.scalar("Form", string(g.Form), string(h.Form))
	if err != nil {
		return Row{}, err
	}
	out.Form = Form(form)
	if out.Traits, err = m.traits(); err != nil {
		return Row{}, err
	}
	lists := []struct {
		field     string
		gen, hand []Placement
		into      *[]Placement
	}{
		{"Values", g.Values, h.Values, &out.Values},
		{"Infra", g.Infra, h.Infra, &out.Infra},
		{"Overrides", g.Overrides, h.Overrides, &out.Overrides},
		{"Secrets", g.Secrets, h.Secrets, &out.Secrets},
		{"Provides", g.Provides, h.Provides, &out.Provides},
	}
	for _, l := range lists {
		if *l.into, err = mergeEntries(m, l.field, l.gen, l.hand, placementKey, placementsSame, placementText); err != nil {
			return Row{}, err
		}
	}
	if out.Folds, err = mergeEntries(m, "Folds", g.Folds, h.Folds, foldKey, foldsSame, foldText); err != nil {
		return Row{}, err
	}
	if out.Equivalences, err = mergeEntries(m, "Equivalences", g.Equivalences, h.Equivalences, equivalenceKey,
		equivalencesSame, equivalenceText); err != nil {
		return Row{}, err
	}
	switch {
	case h.Env == nil:
		out.Env = g.Env
	case g.Env == nil || envSame(*g.Env, *h.Env) || m.reasoned("Env"):
		out.Env = h.Env
	default:
		return Row{}, m.contradiction("Env", envText(*g.Env), envText(*h.Env))
	}
	return out, nil
}

// reasoned reports that the hand row writes a reason for superseding field.
func (m merger) reasoned(field string) bool {
	return m.hand.Supersedes[field] != ""
}

// contradiction returns the *Contradiction of field.
func (m merger) contradiction(field, gen, hand string) error {
	return &Contradiction{Key: m.gen.Key, When: m.gen.When, Field: field, Generated: gen, Hand: hand}
}

// scalar merges the hand value of a text field over the generated one.
func (m merger) scalar(field, gen, hand string) (string, error) {
	if hand == "" {
		return gen, nil
	}
	if gen == "" || gen == hand || m.reasoned(field) {
		return hand, nil
	}
	return "", m.contradiction(field, gen, hand)
}

// traits merges the hand row's traits over the generated row's, label by
// label in sorted order, so the first contradiction is one whatever order the
// maps hold their labels in.
func (m merger) traits() (map[string]string, error) {
	if len(m.hand.Traits) == 0 {
		return m.gen.Traits, nil
	}
	out := make(map[string]string, len(m.gen.Traits)+len(m.hand.Traits))
	maps.Copy(out, m.gen.Traits)
	for _, label := range slices.Sorted(maps.Keys(m.hand.Traits)) {
		hand := m.hand.Traits[label]
		if gen, set := m.gen.Traits[label]; set && gen != hand && !m.reasoned("Traits."+label) {
			return nil, m.contradiction("Traits."+label, gen, hand)
		}
		out[label] = hand
	}
	return out, nil
}

// mergeEntries merges the hand entries of a list field into the generated
// ones, an entry matched to another by its key: a generated entry the hand
// row sets no entry of its key for stays, one it sets is replaced by the
// first hand entry of that key, and the hand entries of a key no generated
// entry has follow, in order.
func mergeEntries[T any](m merger, field string, gen, hand []T, key func(T) string, same func(a, b T) bool,
	text func(T) string) ([]T, error) {
	if len(hand) == 0 {
		return gen, nil
	}
	handAt := make(map[string]int, len(hand))
	for i := len(hand) - 1; i >= 0; i-- {
		handAt[key(hand[i])] = i
	}
	generated := make(map[string]bool, len(gen))
	out := make([]T, 0, len(gen)+len(hand))
	for _, g := range gen {
		k := key(g)
		generated[k] = true
		i, set := handAt[k]
		if !set {
			out = append(out, g)
			continue
		}
		h := hand[i]
		if !same(g, h) && !m.reasoned(field+"."+k) {
			return nil, m.contradiction(field+"."+k, text(g), text(h))
		}
		out = append(out, h)
	}
	for _, h := range hand {
		if !generated[key(h)] {
			out = append(out, h)
		}
	}
	return out, nil
}

func placementKey(p Placement) string { return p.Key }

func placementsSame(a, b Placement) bool {
	return slices.Equal(a.From, b.From) && a.Key == b.Key && a.Conv == b.Conv
}

func placementText(p Placement) string {
	if p.Conv == "" {
		return pathText(p.From)
	}
	return pathText(p.From) + " as " + string(p.Conv)
}

func foldKey(f Fold) string { return f.Type }

func foldsSame(a, b Fold) bool {
	return a.Type == b.Type && slices.Equal(a.Key, b.Key) && slices.Equal(a.From, b.From) && a.One == b.One &&
		a.Instances == b.Instances && a.NotCarried == b.NotCarried
}

func foldText(f Fold) string {
	text := "into " + pathText(f.Key) + " from " + pathText(f.From)
	if f.One {
		text += ", one value"
	}
	if f.Instances {
		text += ", an instance each"
	}
	if f.NotCarried != "" {
		text += ", not carried: " + f.NotCarried
	}
	return text
}

func equivalenceKey(e Equivalence) string { return pathText(e.Raw) }

func equivalencesSame(a, b Equivalence) bool {
	return slices.Equal(a.Raw, b.Raw) && slices.Equal(a.Module, b.Module)
}

func equivalenceText(e Equivalence) string { return pathText(e.Module) }

func envSame(a, b EnvList) bool {
	return slices.Equal(a.Path, b.Path) && a.Name == b.Name && a.Value == b.Value
}

func envText(e EnvList) string {
	return pathText(e.Path) + " (name " + e.Name + ", value " + e.Value + ")"
}

// pathText writes a path with its segments joined by dots.
func pathText(path []string) string {
	return strings.Join(path, ".")
}

// whenText writes a When as report text, empty for none.
func whenText(when []Pred) string {
	if len(when) == 0 {
		return ""
	}
	preds := make([]string, len(when))
	for i, p := range when {
		preds[i] = pathText(p.Path) + " " + string(p.Op)
		if p.Op != OpPresent && p.Op != OpAbsent {
			preds[i] += " " + fmt.Sprintf("%q", p.Value)
		}
	}
	return " when " + strings.Join(preds, " and ")
}
