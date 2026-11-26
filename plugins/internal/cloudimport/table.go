// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"fmt"
	"maps"
	"regexp"
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
	// OpLacks holds where OpContains does not, the value at the path read: the
	// node writes no text there containing the predicate's Value, nothing or
	// null included.
	OpLacks PredOp = "lacks"
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
// value as is, "string" for its text, "integer" for an integer,
// "unit:<suffix>" for a number written with that unit suffix, and
// "map:<from>=<to>,<from>=<to>…" for a scalar the source spells in a
// vocabulary of its own: the value whose text is a <from>, matched regardless
// of case, is written as its <to>, a YAML scalar as it stands, so true reads
// back as a boolean. A <from> holds neither a comma nor an equals sign; a <to>
// holds letters, digits, '.', '_', '+' and '-' alone, so it reads back as the
// scalar written. A value the map holds no <from> for is not written.
type Conv string

// convMap is the prefix of a map converter.
const convMap = "map:"

// mapTo is the scalar a map converter writes for the value whose text is
// from, matched regardless of case.
type mapTo struct {
	from, to string
}

// plainScalar is the text of a <to> of a map converter.
var plainScalar = regexp.MustCompile(`^[A-Za-z0-9_.+-]+$`)

// mapping returns the pairs of the map converter c, in the order written, and
// why c is malformed: a pair with an empty side or with no equals sign, a <to>
// that is no plain scalar, or a <from> written twice regardless of case. It is
// false where c is no map converter.
func (c Conv) mapping() ([]mapTo, string, bool) {
	text, ok := strings.CutPrefix(string(c), convMap)
	if !ok {
		return nil, "", false
	}
	var pairs []mapTo
	for _, pair := range strings.Split(text, ",") {
		from, to, found := strings.Cut(pair, "=")
		switch {
		case !found || from == "" || to == "":
			return nil, fmt.Sprintf("the pair %q is not <from>=<to> with both sides written", pair), true
		case !plainScalar.MatchString(to):
			return nil, fmt.Sprintf("the pair %q maps to text that is no plain scalar of letters, digits, '.', "+
				"'_', '+' and '-'", pair), true
		}
		for _, p := range pairs {
			if strings.EqualFold(p.from, from) {
				return nil, fmt.Sprintf("it maps %q twice", from), true
			}
		}
		pairs = append(pairs, mapTo{from: from, to: to})
	}
	return pairs, "", true
}

// mapped returns the scalar the map converter c writes for the value whose
// text is value, false where it holds no <from> for it.
func (c Conv) mapped(value string) (string, bool) {
	pairs, why, ok := c.mapping()
	if !ok || why != "" {
		return "", false
	}
	for _, p := range pairs {
		if strings.EqualFold(p.from, value) {
			return p.to, true
		}
	}
	return "", false
}

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
	// A Provides placement with an empty From publishes the resource's
	// identifier, which a reference to the resource itself reads.
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
	// Folds.<type>; Equivalences.<raw path>, its segments joined by dots. The
	// type of Folds.<type> and the path of Equivalences.<raw path> are matched
	// regardless of case, as the merge matches the entries they name.
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
// Repository to the container product Component and Recipe whose literal
// default image names that repository.
type ImageRow struct {
	Repository, Component, Recipe string
	// ImageKey is the infra key under which a Release writes the whole image
	// the Recipe runs, its repository and its tag together, in place of the
	// default; empty where the Recipe reads no such key.
	ImageKey string
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
		c.Key, WhenText(c.When), c.Field, c.Hand, c.Generated, c.Field)
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
		WhenText(d.When))
}

// MalformedConverter is a placement of a row whose map converter cannot be
// read, so no value placed through it could be written.
type MalformedConverter struct {
	// Key and When identify the row, and Origin says where it comes from.
	Key    string
	When   []Pred
	Origin string
	// Field names the placement as Row.Supersedes names it: Values.<key>,
	// Infra.<key>, Overrides.<key>, Secrets.<slot> or Provides.<key>.
	Field string
	// Conv is the converter, and Why what is malformed in it.
	Conv Conv
	Why  string
}

// Error names the row, the placement, the converter and what to write.
func (m *MalformedConverter) Error() string {
	origin := m.Origin
	if origin == "" {
		origin = "the format's table"
	}
	return fmt.Sprintf("the row of %s%s (%s) converts %s with %q, and %s: write the converter as "+
		"map:<from>=<to>,<from>=<to>, each <from> once and each <to> a plain scalar", m.Key, WhenText(m.When),
		origin, m.Field, string(m.Conv), m.Why)
}

// malformed returns a *MalformedConverter for the first placement of rows
// whose map converter is malformed, nil where none is.
func malformed(rows []Row) error {
	for _, r := range rows {
		lists := []struct {
			field string
			ps    []Placement
		}{{"Values", r.Values}, {"Infra", r.Infra}, {"Overrides", r.Overrides}, {"Secrets", r.Secrets},
			{"Provides", r.Provides}}
		for _, l := range lists {
			for _, p := range l.ps {
				if _, why, ok := p.Conv.mapping(); ok && why != "" {
					return &MalformedConverter{Key: r.Key, When: r.When, Origin: r.Origin,
						Field: l.field + "." + p.Key, Conv: p.Conv, Why: why}
				}
			}
		}
	}
	return nil
}

// Rows returns the table's rows: each generated row in order, the hand row of
// its key and When merged into it, then each hand row no generated row shares
// a key and When with, in order. Two keys are one where they differ only in
// case, and two Whens are one where they hold the same predicates in any
// order, their paths and values differing only in case. A merged row takes,
// field by field, the hand value where the hand row sets one and the
// generated value otherwise; a list field is merged entry by entry, an entry
// matched by its key, and a hand entry of a key the generated row does not
// set is added after the generated ones. A fold's child type and an
// equivalence's raw path are matched regardless of case, as a node's type and
// properties are; an instance's own key — of values, infra, overrides, a
// slot, provides — is matched exactly. Two entries of one key are the same
// where they differ only in the case of a type, a property path or a member
// name. A hand value set over a different
// generated one needs a written reason in the hand row's Supersedes: without
// one, Rows returns a [*Contradiction] naming the key, the field and both
// values. Two rows of one origin sharing a key and When return a
// [*DuplicateRow], and a placement whose map converter cannot be read a
// [*MalformedConverter] naming its row. A merged row shares the slices and maps of the fields it
// takes with the generated and the hand row, and a row left unmerged is the
// table's own: none is to be changed.
func (t Table) Rows() ([]Row, error) {
	if err := duplicates(t.Generated, "generated"); err != nil {
		return nil, err
	}
	if err := duplicates(t.Hand, "hand"); err != nil {
		return nil, err
	}
	if err := malformed(t.Generated); err != nil {
		return nil, err
	}
	if err := malformed(t.Hand); err != nil {
		return nil, err
	}
	handAt := make(map[string]int, len(t.Hand))
	for i, h := range t.Hand {
		handAt[h.Identity()] = i
	}
	merged := make([]bool, len(t.Hand))
	out := make([]Row, 0, len(t.Generated)+len(t.Hand))
	for _, g := range t.Generated {
		i, ok := handAt[g.Identity()]
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
		id := r.Identity()
		if seen[id] {
			return &DuplicateRow{Key: r.Key, When: r.When, Origin: origin}
		}
		seen[id] = true
	}
	return nil
}

// Identity is the text of the row's key and When, lowercased, its predicates
// in sorted order, so two rows whose keys, paths and values differ only in
// case, and whose Whens hold the same predicates in any order, share one
// identity. Two rows of one identity are one row to [Table.Rows]: a hand row
// merges into the generated row of its identity, and two rows of one origin
// sharing it are a [*DuplicateRow].
func (r Row) Identity() string {
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
		*l.into, err = mergeEntries(m, l.field, l.gen, l.hand, placementKey, false, placementsSame, placementText)
		if err != nil {
			return Row{}, err
		}
	}
	if out.Folds, err = mergeEntries(m, "Folds", g.Folds, h.Folds, foldKey, true, foldsSame, foldText); err != nil {
		return Row{}, err
	}
	if out.Equivalences, err = mergeEntries(m, "Equivalences", g.Equivalences, h.Equivalences, equivalenceKey, true,
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

// reasonedEntry reports that the hand row writes a reason for superseding the
// entry of list field keyed entry: under the name field.entry, or where
// foldCase is set, under field. followed by entry written in any case.
func (m merger) reasonedEntry(field, entry string, foldCase bool) bool {
	if m.reasoned(field + "." + entry) {
		return true
	}
	if !foldCase {
		return false
	}
	for _, name := range slices.Sorted(maps.Keys(m.hand.Supersedes)) {
		if rest, ok := strings.CutPrefix(name, field+"."); ok && strings.EqualFold(rest, entry) && m.reasoned(name) {
			return true
		}
	}
	return false
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
// ones, an entry matched to another by its key — regardless of case where
// foldCase is set, for a key that is a vendor type or a property path, and
// exactly otherwise: a generated entry the hand row sets no entry of its key
// for stays, one it sets is replaced by the first hand entry of that key, and
// the hand entries of a key no generated entry has follow, in order. A
// contradiction names the entry by the generated entry's key.
func mergeEntries[T any](m merger, field string, gen, hand []T, key func(T) string, foldCase bool,
	same func(a, b T) bool, text func(T) string) ([]T, error) {
	if len(hand) == 0 {
		return gen, nil
	}
	match := func(entry T) string {
		if foldCase {
			return strings.ToLower(key(entry))
		}
		return key(entry)
	}
	handAt := make(map[string]int, len(hand))
	for i := len(hand) - 1; i >= 0; i-- {
		handAt[match(hand[i])] = i
	}
	generated := make(map[string]bool, len(gen))
	out := make([]T, 0, len(gen)+len(hand))
	for _, g := range gen {
		generated[match(g)] = true
		i, set := handAt[match(g)]
		if !set {
			out = append(out, g)
			continue
		}
		h := hand[i]
		if !same(g, h) && !m.reasonedEntry(field, key(g), foldCase) {
			return nil, m.contradiction(field+"."+key(g), text(g), text(h))
		}
		out = append(out, h)
	}
	for _, h := range hand {
		if !generated[match(h)] {
			out = append(out, h)
		}
	}
	return out, nil
}

func placementKey(p Placement) string { return p.Key }

func placementsSame(a, b Placement) bool {
	return samePath(a.From, b.From) && a.Key == b.Key && a.Conv == b.Conv
}

func placementText(p Placement) string {
	if p.Conv == "" {
		return pathText(p.From)
	}
	return pathText(p.From) + " as " + string(p.Conv)
}

func foldKey(f Fold) string { return f.Type }

func foldsSame(a, b Fold) bool {
	return strings.EqualFold(a.Type, b.Type) && slices.Equal(a.Key, b.Key) && samePath(a.From, b.From) &&
		a.One == b.One && a.Instances == b.Instances && a.NotCarried == b.NotCarried
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
	return samePath(a.Raw, b.Raw) && samePath(a.Module, b.Module)
}

func equivalenceText(e Equivalence) string { return pathText(e.Module) }

func envSame(a, b EnvList) bool {
	return samePath(a.Path, b.Path) && strings.EqualFold(a.Name, b.Name) && strings.EqualFold(a.Value, b.Value)
}

// samePath reports whether two property or attribute paths name one place:
// the same segments, compared regardless of case as a node's properties are
// read.
func samePath(a, b []string) bool {
	return slices.EqualFunc(a, b, strings.EqualFold)
}

func envText(e EnvList) string {
	return pathText(e.Path) + " (name " + e.Name + ", value " + e.Value + ")"
}

// pathText writes a path with its segments joined by dots.
func pathText(path []string) string {
	return strings.Join(path, ".")
}

// WhenText writes a When as report text — " when " and its predicates joined
// by "and", in the order written — and empty for none.
func WhenText(when []Pred) string {
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
