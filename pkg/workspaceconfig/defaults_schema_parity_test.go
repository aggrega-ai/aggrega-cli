// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"
)

// The configuration schema states a default for each setting it declares, and
// Default seeds the values the loader's overlay pattern preserves when a
// workspace file omits a key. Those are two independent statements of one fact,
// and the checks below compare them in both directions: a schema default with a
// different value in the code, and a value the code seeds that the schema
// declares no default for, each fail.
//
// The two sides are derived independently, which is what makes the comparison
// able to fail. The schema side is parsed from the schema itself; the code side
// is read from Default through the yaml tags that name each setting. Neither
// side holds a list of setting names and neither holds an expected value, so
// there is nothing here to keep in step by hand as settings are added.
//
// WHAT THIS DOES NOT COVER, stated because it is real: these checks run when
// this package's own sources change, and an edit confined to the schema changes
// no file in this package. A selective test run that picks packages by what
// changed therefore does not reach these checks on a schema-only edit; a run
// over the whole module does. So a green selective run is not evidence that the
// two sides agree — only a run that executed these checks is.
//
// The schema is read from the exported string the schemas module embeds, and
// the reference to schemas.AggregaConfigV1beta1 below is what makes those
// bytes an input the build accounts for: the schema is compiled into this test
// binary rather than read from disk at run time, so a schema edit changes this
// binary and a previously recorded result cannot be reused for it.

// defaultsCoverageLocation names the file these checks live in, so a finding
// sends a reader to the one place the comparison is written rather than to
// whichever check happened to report it.
const defaultsCoverageLocation = "pkg/workspaceconfig/defaults_schema_parity_test.go"

// defaultsListSegment marks where a setting's dotted path enters a list entry.
//
// It is part of the path rather than a flag beside it because the path is what
// a finding prints, and a reader needs to see that a setting sits inside a list
// entry to make sense of the value it is compared against.
const defaultsListSegment = "[]"

// defaultsMapSegment marks where a setting's dotted path enters a map entry —
// the value one key of a map setting carries — for the reason
// defaultsListSegment marks a list entry.
const defaultsMapSegment = "{}"

// schemaDefault is one default the configuration schema declares, at the path a
// config file writes the setting at.
type schemaDefault struct {
	path  string
	value any
}

// configSchemaDefaults walks a configuration schema and returns every default
// it declares.
//
// This traversal is deliberately separate from originSchemaRows, and the two
// are not to be unified: that walk reports settings at the granularity the
// origin report attributes, where a list is one unit and is never descended,
// while a default declared on a field of a list entry is a promise to a reader
// in its own right and has to be reached. What the two share is
// originSchemaPropertyFreeKeywords — the classification of which keywords
// cannot hide a declared setting — so a schema that grows a composition keyword
// is refused by both at once rather than quietly narrowing one of them.
//
// A keyword the walk neither acts on nor recognizes as property-free is
// refused. The walk cannot report a default it never reached and nothing else
// would notice that it did not reach one, so the refusal is what keeps the
// comparison's reach a decision rather than an accident.
//
// Defaults come back in a deterministic order — lexical by property name within
// each object, depth first — so a finding built from them reads the same across
// runs.
func configSchemaDefaults(schema string) ([]schemaDefault, error) {
	var root map[string]any
	if err := json.Unmarshal([]byte(schema), &root); err != nil {
		return nil, fmt.Errorf("unmarshal the configuration schema: %w", err)
	}
	defs, _ := root["$defs"].(map[string]any)

	var found []schemaDefault
	var walk func(node map[string]any, path string, seen map[string]bool) error

	// describe names a node for a finding. The schema's root carries no path,
	// and a finding about it that said "the setting" would send a reader looking
	// for a property that does not exist.
	describe := func(path string) string {
		if path == "" {
			return "the schema root"
		}
		return fmt.Sprintf("the %q setting", path)
	}

	// resolve follows a chain of local definition references, returning the node
	// the chain lands on. A reference that leaves the document, or one that
	// closes a cycle, is reported rather than followed: either would make the
	// walk's answer depend on how far it happened to get.
	resolve := func(node map[string]any, path string, seen map[string]bool) (map[string]any, map[string]bool, error) {
		for {
			raw, ok := node["$ref"]
			if !ok {
				return node, seen, nil
			}
			ref, ok := raw.(string)
			if !ok {
				return nil, nil, fmt.Errorf("%s carries a reference that is not a string", describe(path))
			}
			const localPrefix = "#/$defs/"
			if !strings.HasPrefix(ref, localPrefix) {
				return nil, nil, fmt.Errorf("%s references %q, which is not a local definition; "+
					"the walk resolves local definitions only, so it cannot report what that reference declares",
					describe(path), ref)
			}
			if seen[ref] {
				return nil, nil, fmt.Errorf("%s closes a reference cycle at %q", describe(path), ref)
			}
			target, ok := defs[strings.TrimPrefix(ref, localPrefix)].(map[string]any)
			if !ok {
				return nil, nil, fmt.Errorf("%s references %q, which the schema does not define", describe(path), ref)
			}
			next := map[string]bool{ref: true}
			for k := range seen {
				next[k] = true
			}
			node, seen = target, next
		}
	}

	walk = func(node map[string]any, path string, seen map[string]bool) error {
		node, seen, err := resolve(node, path, seen)
		if err != nil {
			return err
		}

		// Keywords are examined in a fixed order so a schema carrying two the
		// walk refuses is refused with the same finding across runs.
		keywords := make([]string, 0, len(node))
		for keyword := range node {
			keywords = append(keywords, keyword)
		}
		sort.Strings(keywords)
		for _, keyword := range keywords {
			switch keyword {
			case "properties", "items", "$defs", "$ref":
				// Acted on below. Definitions are reached through a reference
				// rather than descended where they are declared, so a definition
				// block itself declares no setting.
			case "additionalProperties":
				// A closed object states that there is nothing further to find,
				// and a map's entry is descended below. An object left open by
				// `true`, or one declaring properties beside a subschema for
				// every other key, admits keys the walk cannot enumerate, so both
				// are refused.
				if closed, ok := node[keyword].(bool); ok {
					if closed {
						return fmt.Errorf("%s admits additional properties by schema, so the walk "+
							"cannot enumerate what it declares", describe(path))
					}
					continue
				}
				if !isSchemaMap(node) {
					return fmt.Errorf("%s admits additional properties by schema, so the walk "+
						"cannot enumerate what it declares", describe(path))
				}
			default:
				if !originSchemaPropertyFreeKeywords[keyword] {
					return fmt.Errorf("%s carries the %q keyword, which the walk does not act on "+
						"and cannot rule out as hiding a declared default. Either teach the walk to descend it, "+
						"or add it to the property-free keywords in %s with the reason it cannot carry a subschema",
						describe(path), keyword, originCoverageLocation)
				}
			}
		}

		if raw, ok := node["default"]; ok {
			if path == "" {
				return fmt.Errorf("the schema root declares a default, which names no setting")
			}
			found = append(found, schemaDefault{path: path, value: raw})
		}

		if raw, ok := node["items"]; ok {
			child, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("%s declares a list whose entry is not a schema object", describe(path))
			}
			return walk(child, path+defaultsListSegment, seen)
		}
		if isSchemaMap(node) {
			// A default declared on a field of a map's entry is a promise to a
			// reader on the same terms as one on a list entry's field, so the
			// entry is descended rather than recorded as a unit.
			return walk(node["additionalProperties"].(map[string]any), path+defaultsMapSegment, seen)
		}
		props, ok := node["properties"].(map[string]any)
		if !ok {
			return nil
		}
		names := make([]string, 0, len(props))
		for name := range props {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			child, ok := props[name].(map[string]any)
			if !ok {
				return fmt.Errorf("the %q property is not a schema object", name)
			}
			next := name
			if path != "" {
				next = path + "." + name
			}
			if err := walk(child, next, seen); err != nil {
				return err
			}
		}
		return nil
	}

	// The root is walked as a node like any other, so its own keywords are held
	// to the same guard. A composition keyword at the root would otherwise be
	// the one place the refusal above could not see.
	if err := walk(root, "", map[string]bool{}); err != nil {
		return nil, err
	}
	return found, nil
}

// defaultsYAMLName returns the name a config file writes a struct field under.
// A field with no tag is matched lowercased, which is what the YAML decoder
// does with an untagged field.
func defaultsYAMLName(f reflect.StructField) string {
	tag, ok := f.Tag.Lookup("yaml")
	if !ok {
		return strings.ToLower(f.Name)
	}
	name := strings.Split(tag, ",")[0]
	if name == "" {
		return strings.ToLower(f.Name)
	}
	return name
}

// defaultsFieldIndex returns the index of the struct field a config file writes
// under the given name.
func defaultsFieldIndex(t reflect.Type, name string) (int, error) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		if defaultsYAMLName(f) == name {
			return i, nil
		}
	}
	return 0, fmt.Errorf("the %s type declares no field written as %q", t.Name(), name)
}

// codeDefaultAt returns the default the code applies at a setting's dotted
// path.
//
// A path that enters a list entry is resolved against a zero-valued entry, and
// that is the contract rather than a convenience: Default constructs no list
// entries, so the value a decoded entry carries for a key its author omitted is
// its type's zero value. A schema default on such a field is therefore a
// promise that the zero value is what a reader gets, and comparing against a
// zero-valued entry is what tests that promise. A path entering a map entry is
// resolved the same way, against a zero value of the map's value type.
func codeDefaultAt(path string) (any, error) {
	v := reflect.ValueOf(Default())
	for _, segment := range strings.Split(path, ".") {
		name := strings.TrimSuffix(segment, defaultsListSegment)
		entersList := name != segment
		entersMap := false
		if !entersList {
			name = strings.TrimSuffix(segment, defaultsMapSegment)
			entersMap = name != segment
		}
		if v.Kind() != reflect.Struct {
			return nil, fmt.Errorf("the %q setting resolves through a %s, which declares no fields", path, v.Kind())
		}
		i, err := defaultsFieldIndex(v.Type(), name)
		if err != nil {
			return nil, fmt.Errorf("the %q setting does not resolve: %w", path, err)
		}
		v = v.Field(i)
		switch {
		case entersList:
			if v.Kind() != reflect.Slice {
				return nil, fmt.Errorf("the %q setting is written as a list entry, but the code declares "+
					"the %q field as a %s rather than a list", path, name, v.Kind())
			}
			v = reflect.New(v.Type().Elem()).Elem()
		case entersMap:
			entry, err := orderedMapValueType(v.Type())
			if err != nil {
				return nil, fmt.Errorf("the %q setting is written as a map entry, but %w", path, err)
			}
			v = reflect.New(entry).Elem()
		}
	}
	return v.Interface(), nil
}

// orderedMapValueType returns the value type of a field the code declares as an
// ordered map, which is the type every map setting in the configuration is
// decoded into. The value type is read off the map the ordered map holds, since
// the ordered map exposes no other statement of it.
func orderedMapValueType(t reflect.Type) (reflect.Type, error) {
	if t.Kind() == reflect.Struct {
		if f, ok := t.FieldByName("values"); ok && f.Type.Kind() == reflect.Map {
			return f.Type.Elem(), nil
		}
	}
	return nil, fmt.Errorf("the code declares the field as a %s rather than an ordered map", t)
}

// defaultsJSONShape renders a value into the shape the comparison is made in,
// so both sides are compared as the same kind of thing.
//
// One normalization is applied and it is deliberate: an absent list is rendered
// as an empty one, because "no entries" is the whole content of both statements
// and a schema cannot spell the difference. Every other difference is reported.
func defaultsJSONShape(v any) (any, error) {
	rv := reflect.ValueOf(v)
	if rv.IsValid() && rv.Kind() == reflect.Slice && rv.IsNil() {
		v = reflect.MakeSlice(rv.Type(), 0, 0).Interface()
	}
	if rv.IsValid() && rv.Kind() == reflect.Map && rv.IsNil() {
		v = reflect.MakeMap(rv.Type()).Interface()
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("render %v for comparison: %w", v, err)
	}
	var shape any
	if err := json.Unmarshal(encoded, &shape); err != nil {
		return nil, fmt.Errorf("read back %s for comparison: %w", encoded, err)
	}
	return shape, nil
}

// codeSeededDefaults returns every setting Default gives a value other than its
// zero value, keyed by the dotted path a config file writes it at.
//
// It is derived by comparing Default against a zero Config rather than from any
// list, so a field that starts being seeded joins this set with no edit here.
func codeSeededDefaults() (map[string]any, error) {
	seeded := map[string]any{}
	var walk func(seededV, zeroV reflect.Value, path string) error
	walk = func(seededV, zeroV reflect.Value, path string) error {
		if seededV.Kind() == reflect.Struct {
			t := seededV.Type()
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				if f.PkgPath != "" {
					continue
				}
				name := defaultsYAMLName(f)
				if name == "-" {
					continue
				}
				next := name
				if path != "" {
					next = path + "." + name
				}
				if err := walk(seededV.Field(i), zeroV.Field(i), next); err != nil {
					return err
				}
			}
			return nil
		}
		seededShape, err := defaultsJSONShape(seededV.Interface())
		if err != nil {
			return err
		}
		zeroShape, err := defaultsJSONShape(zeroV.Interface())
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(seededShape, zeroShape) {
			seeded[path] = seededV.Interface()
		}
		return nil
	}
	if err := walk(reflect.ValueOf(Default()), reflect.ValueOf(Config{}), ""); err != nil {
		return nil, err
	}
	return seeded, nil
}

// TestConfigSchemaDefaultsMatchCodeDefaults compares every default the
// configuration schema declares against the value the code applies for that
// setting.
//
// A setting the schema declares a default for that the code has no field for is
// a finding rather than a skip: it means the schema promises a reader something
// no code path can deliver.
func TestConfigSchemaDefaultsMatchCodeDefaults(t *testing.T) {
	t.Parallel()

	declared, err := configSchemaDefaults(schemas.AggregaConfigV1beta1)
	if err != nil {
		t.Fatalf("read the defaults the configuration schema declares: %v", err)
	}
	if len(declared) == 0 {
		t.Fatalf("the configuration schema declares no defaults, which no schema of this shape does; "+
			"the walk in %s reported nothing rather than finding nothing", defaultsCoverageLocation)
	}

	// A setting inside a list entry is the coverage a walk loses first, because
	// nothing else fails when a traversal stops descending a list. Asserting the
	// comparison reached one keeps that reach from narrowing in silence.
	entered := false
	for _, d := range declared {
		if strings.Contains(d.path, defaultsListSegment) {
			entered = true
			break
		}
	}
	if !entered {
		t.Errorf("no compared setting sits inside a list entry, so the walk in %s is no longer "+
			"descending lists and every default declared on a list entry's field is going uncompared",
			defaultsCoverageLocation)
	}

	// The criterion is recomputed here rather than written down, so the figure a
	// verbose run reports cannot drift from what was actually compared: one pair
	// per `default` keyword the schema declares, counting a default on a list
	// entry's field once for the entry rather than once per entry.
	t.Logf("compared %d schema-declared defaults against the value the code applies", len(declared))

	for _, d := range declared {
		switch value := d.value.(type) {
		case []any:
			// A populated list default is refused rather than compared: the
			// comparison renders the code side through its own field names,
			// which are not the names a config file writes, so a structured
			// default would be compared on an axis that does not hold.
			if len(value) > 0 {
				t.Errorf("the %q setting declares a populated list as its default, which the comparison in %s "+
					"cannot check: teach it to compare a structured default before declaring one", d.path, defaultsCoverageLocation)
				continue
			}
		case map[string]any:
			t.Errorf("the %q setting declares an object as its default, which the comparison in %s "+
				"cannot check: teach it to compare a structured default before declaring one", d.path, defaultsCoverageLocation)
			continue
		}

		applied, err := codeDefaultAt(d.path)
		if err != nil {
			t.Errorf("the configuration schema declares a default for a setting the code cannot resolve: %v", err)
			continue
		}
		want, err := defaultsJSONShape(d.value)
		if err != nil {
			t.Errorf("the %q setting: %v", d.path, err)
			continue
		}
		got, err := defaultsJSONShape(applied)
		if err != nil {
			t.Errorf("the %q setting: %v", d.path, err)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("the %q setting: the configuration schema declares the default %v, "+
				"and the code applies %v. A reader trusting the schema is told something the product does not do; "+
				"correct whichever side is wrong", d.path, want, got)
		}
	}
}

// TestCodeSeededDefaultsAreDeclaredInSchema checks the other direction: every
// setting the code seeds with a value other than its zero value is a setting the
// schema declares that same default for.
//
// This is the direction that catches a default the product applies and the
// schema never documents, where a reader is told the value is the zero value
// and gets something else.
func TestCodeSeededDefaultsAreDeclaredInSchema(t *testing.T) {
	t.Parallel()

	seeded, err := codeSeededDefaults()
	if err != nil {
		t.Fatalf("read the defaults the code applies: %v", err)
	}
	if len(seeded) == 0 {
		t.Fatalf("the code seeds no default at all, which Default is written to do; "+
			"the comparison in %s reported nothing rather than finding nothing", defaultsCoverageLocation)
	}

	declared, err := configSchemaDefaults(schemas.AggregaConfigV1beta1)
	if err != nil {
		t.Fatalf("read the defaults the configuration schema declares: %v", err)
	}
	declaredAt := make(map[string]bool, len(declared))
	for _, d := range declared {
		declaredAt[d.path] = true
	}

	paths := make([]string, 0, len(seeded))
	for path := range seeded {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if !declaredAt[path] {
			t.Errorf("the code applies %v as the default for the %q setting, and the configuration schema "+
				"declares no default there. A reader is told the setting starts at its zero value and gets "+
				"something else; declare the default in the schema or stop applying it", seeded[path], path)
		}
	}
}
