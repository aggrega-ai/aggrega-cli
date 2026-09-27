// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Schema → Go alignment for the daemon's wire types: the direction that catches
// a schema property the Go silently drops.
//
// Its sibling asserts the other direction — a populated Go value validates
// against the schema — and one direction alone catches one kind of drift and
// sails past the other. A property declared in the schema and carried by no Go
// field marshals to nothing, so a client reading the published contract expects
// a key the daemon never sends, and every assertion in the other direction
// passes while it does.
//
// Three mechanisms, and each keeps the next honest:
//
//   - The SUBJECT SET is the schema's own population. Every $defs entry whose
//     property surface is non-empty must be paired with the Go types that carry
//     it, or the pairing gate fails. That is what stops this direction quietly
//     covering the positions that happen to be listed.
//   - The WALK collects every property name under a position — through
//     properties, items, additionalProperties, patternProperties, the boolean
//     combinators, the if/then/else trio and prefixItems — and compares the
//     names against the json tags of the paired Go types.
//   - The DISPATCH-BOUNDARY RULE is derived from the schema rather than listed,
//     and is explained where it is declared below.
//
// # Why the walk does not follow $ref, and why that is safe here
//
// Every $ref in this document targets a $defs entry, and the pairing gate
// obliges every $defs entry with properties to be a subject in its own right.
// So a referenced shape is walked at its own position rather than inlined into
// each referrer, and nothing is skipped. Following refs instead would report the
// same drift once per referrer, and would make the request envelope — whose
// per-method dispatch reaches every params shape through a ref — carry the whole
// document's property surface against a Go type holding raw bytes.
//
// The safety of that choice rests entirely on the pairing gate, so the two are
// not separable: weaken the gate and this walk starts certifying positions it
// never reaches. TestWireSchemaWalkDescendsIntoEveryContainer plants a name
// behind each container keyword and asserts the walk returns it, and asserts the
// ref case is the one it does NOT return — so the non-descent is a measured
// property rather than an assumption.
package serve

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// wireBoundaryParamsDef is the position that declares the fields the session
// reads off a raw request before it routes, and it is the single source this
// file derives the dispatch-boundary rule from. The Go types paired with it are
// the ones that decode those fields, so the position and its Go home are held
// together by the ordinary alignment below rather than by a claim.
const wireBoundaryParamsDef = "#/$defs/WorkspaceAndProgressParams"

// wireSchemaSubject is one schema position and the Go types whose json tags must
// account for every property under it.
type wireSchemaSubject struct {
	// def is a JSON pointer naming a $defs entry.
	def string
	// roots are the Go types whose tags cover the position between them. More
	// than one is ordinary: a position whose fields are read by two different
	// decoders has a root per decoder.
	roots []reflect.Type
}

// wireExternalSchemaRoot pairs a schema position with a Go type declared outside
// this package.
type wireExternalSchemaRoot struct {
	root reflect.Type
	// why states where the type lives and what reaches it, since the reader
	// cannot find it by looking at this package.
	why string
}

// wireExternalSchemaRoots pairs the positions whose Go home is declared in
// another package.
//
// The other direction's population is parsed from this package's own source, so
// it structurally cannot see these types — they reach the wire as the field type
// of a shape that IS declared here. Without a row each, their property surfaces
// would be walked against nothing.
var wireExternalSchemaRoots = map[string]wireExternalSchemaRoot{
	"#/$defs/Position": {
		root: reflect.TypeOf(yamlpos.Position{}),
		why:  "the document-position type, reached as the cursor field of the position-taking request shapes",
	},
	"#/$defs/Range": {
		root: reflect.TypeOf(yamlpos.Range{}),
		why:  "the document-span type, reached through the location shape",
	},
	"#/$defs/Location": {
		root: reflect.TypeOf(yamlpos.Location{}),
		why:  "the span-in-a-named-document type, reached as the entries of the reference and implementation answers",
	},
	"#/$defs/Registry": {
		root: reflect.TypeOf(registryops.Registry{}),
		why:  "the configured-registry row, reached as the entries of the registry inventory answer",
	},
	"#/$defs/RegistryTrust": {
		root: reflect.TypeOf(registryops.Trust{}),
		why:  "the per-registry signature posture, reached through the registry row; the Go name is shorter than the wire name because the package already qualifies it",
	},
	"#/$defs/TrustFetchOutcome": {
		root: reflect.TypeOf(registryops.TrustFetchOutcome{}),
		why:  "what reading a registry's advertised trust policy did, reached from the registry-add and configuration-init answers",
	},
	"#/$defs/HostUpdate": {
		root: reflect.TypeOf(registryops.HostUpdate{}),
		why:  "what an index refresh did for one registry, reached as the entries of the registry-update answer",
	},
	"#/$defs/SkippedRepo": {
		root: reflect.TypeOf(registryops.SkippedRepo{}),
		why:  "one repository an index walk passed over, reached through the per-registry refresh row",
	},
	"#/$defs/DeprecationNote": {
		root: reflect.TypeOf(registryops.DeprecationNote{}),
		why:  "one deprecation marker an index walk met, reached through the per-registry refresh row",
	},
	"#/$defs/PublishedVersion": {
		root: reflect.TypeOf(registryindex.PublishedVersion{}),
		why:  "one version the local index records with its manifest digest and verified signer, reached as the entries of the search record's published list",
	},
	"#/$defs/VerifiedSignature": {
		root: reflect.TypeOf(lockfile.VerifiedSignature{}),
		why:  "the verified-signature fact the lockfile and the local index share, reached as the signer of a published version",
	},
	"#/$defs/PluginCLICommand": {
		root: reflect.TypeOf(wasm.PluginCLICommand{}),
		why:  "a command verb a plugin contributes, generated from the plugin-description contract and reached through the plugin-sync answer",
	},
	"#/$defs/PluginCommandFlag": {
		root: reflect.TypeOf(wasm.PluginCommandFlag{}),
		why:  "a flag a plugin declares on a command it contributes, reached through the contributed command",
	},
	"#/$defs/ClientNotification": {
		root: reflect.TypeOf(jsonRPCRequest{}),
		why:  "the envelope a client sends with no identifier; it is decoded into the same inbound envelope type a request is, which is why it has no shape of its own here",
	},
}

// wireSchemaSubjects pairs every $defs position with the Go types that must
// carry it.
//
// Most pairings are read off the other direction's table rather than restated: a
// row there already names a Go type and the position its marshalled form must
// validate against, and that is the same pairing this direction needs read the
// other way round. A row addressing a position INSIDE a definition contributes
// its type to the definition that contains it, since the containing type's own
// tree reaches it.
func wireSchemaSubjects(t *testing.T) []wireSchemaSubject {
	t.Helper()

	roots := map[string][]reflect.Type{}
	add := func(def string, typ reflect.Type) {
		for _, existing := range roots[def] {
			if existing == typ {
				return
			}
		}
		roots[def] = append(roots[def], typ)
	}

	for _, s := range wireSubjects() {
		add(wireContainingDef(s.def), s.typ)
	}
	for def, external := range wireExternalSchemaRoots {
		if strings.TrimSpace(external.why) == "" {
			t.Errorf("the external root for %s carries no reason; an unexplained pairing is "+
				"indistinguishable from a guess", def)
		}
		add(def, external.root)
	}

	out := make([]wireSchemaSubject, 0, len(roots))
	for def, types := range roots {
		out = append(out, wireSchemaSubject{def: def, roots: types})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].def < out[j].def })
	return out
}

// wireContainingDef reduces a pointer addressing a position inside a definition
// to the definition that contains it.
func wireContainingDef(pointer string) string {
	rest, ok := strings.CutPrefix(pointer, "#/$defs/")
	if !ok {
		return pointer
	}
	name, _, _ := strings.Cut(rest, "/")
	return "#/$defs/" + name
}

// TestWireSchemaDefsAreAllPairedWithGo is the gate that stops this direction
// covering only the positions somebody listed.
//
// Every $defs entry whose property surface is non-empty must be paired with at
// least one Go type. An entry with no properties needs no pairing and is passed
// over by that derived fact rather than by name — there is nothing under it for
// this direction to compare, so pairing it would assert nothing.
//
// There is deliberately no way to declare a position unpairable. Every position
// this schema declares is decoded by some Go type today, and an escape hatch
// with no occupant is an invitation to widen the one hole this gate exists to
// close: a position that cannot be paired is a finding to carry, not a row to
// suppress.
//
// The reverse direction matters as much: a pairing naming a position the schema
// no longer declares is reported rather than left sitting there addressing
// nothing.
func TestWireSchemaDefsAreAllPairedWithGo(t *testing.T) {
	t.Parallel()

	doc, _ := compileWireSchema(t)
	defs, ok := doc["$defs"].(map[string]any)
	if !ok || len(defs) == 0 {
		t.Fatalf("the shipped serve-wire schema declares no $defs object, so the subject population " +
			"could not be read and every assertion resting on it would certify nothing")
	}

	subjects := wireSchemaSubjects(t)
	paired := map[string]bool{}
	for _, s := range subjects {
		paired[s.def] = true
	}

	var unpaired []string
	for name, def := range defs {
		if len(wireSchemaProperties(def, "")) == 0 {
			continue
		}
		if !paired["#/$defs/"+name] {
			unpaired = append(unpaired, name)
		}
	}
	sort.Strings(unpaired)
	if len(unpaired) > 0 {
		t.Errorf("%d schema definition(s) carry properties that no Go type is held against: %v\n"+
			"each is a shape some Go type decodes — pair it, through the other direction's table "+
			"where that type is declared in this package, or through wireExternalSchemaRoots where "+
			"it is not. A position genuinely decoded by nothing is a finding about the schema, "+
			"because it publishes a shape the session never produces or reads",
			len(unpaired), unpaired)
	}

	for _, s := range subjects {
		name, ok := strings.CutPrefix(s.def, "#/$defs/")
		if !ok {
			t.Errorf("%s is not a definition pointer, so the pairing addresses nothing", s.def)
			continue
		}
		if _, declared := defs[name]; !declared {
			t.Errorf("a Go type is paired with %s, which the shipped schema no longer declares; "+
				"remove the pairing", s.def)
		}
	}
}

// TestWireShapeSchemaToGo is the schema-property-not-in-Go detector.
//
// Every property the schema declares under a position must be a json key one of
// that position's Go types produces, or be a dispatch-boundary field under the
// rule below. A property with neither is a key the published contract promises
// and the daemon never sends.
//
// Reading the tags is what makes this direction possible at all. Decoding a
// document back into the struct cannot do it: an absent key and a key carrying
// the type's zero value arrive identically, so a dropped property is
// indistinguishable from a populated one, and a decode sees no tag to compare
// against in the first place.
func TestWireShapeSchemaToGo(t *testing.T) {
	t.Parallel()

	doc, _ := compileWireSchema(t)
	boundary := wireBoundaryPropertyNames(t, doc)
	exempted := map[string]int{}

	// The dispatch-boundary rule, stated once so the two places that consult it
	// cannot come apart: a property means the shared field the session reads only
	// where it carries a covered name AND points at the shared declaration. The
	// position the names are derived from is the field's own home and is never
	// held against itself.
	sharedField := func(def string, property wireSchemaProperty) bool {
		return def != wireBoundaryParamsDef && boundary[property.name] && property.byRef
	}

	for _, s := range wireSchemaSubjects(t) {
		position, err := resolveSchemaPointer(doc, s.def)
		if err != nil {
			t.Errorf("%s: %v — the pairing names a position the shipped schema does not declare, so "+
				"it would check nothing", s.def, err)
			continue
		}

		tags := map[string]bool{}
		seen := map[reflect.Type]bool{}
		names := make([]string, 0, len(s.roots))
		for _, root := range s.roots {
			wireJSONKeys(root, seen, tags)
			names = append(names, root.String())
		}

		var unexplained []string
		for _, property := range wireSchemaProperties(position, "") {
			if tags[property.name] {
				if sharedField(s.def, property) {
					t.Errorf("%s: %s declares %q by reference, which is how this schema spells the "+
						"field the session reads off the raw request before it routes — yet %s "+
						"declares a json key of that name too. Two homes for one field: either the "+
						"shape means its own field, in which case the schema must declare it inline "+
						"the way the shapes that own one do, or it means the shared one, in which "+
						"case the Go field is shadowing what the boundary already read",
						s.def, property.path, property.name, strings.Join(names, ", "))
				}
				continue
			}
			if sharedField(s.def, property) {
				exempted[property.name]++
				continue
			}
			unexplained = append(unexplained, property.path)
		}
		sort.Strings(unexplained)
		if len(unexplained) > 0 {
			t.Errorf("%s: %d schema propert(ies) that %s carries no json key for: %v\n"+
				"each is either a property the Go silently drops — add the field — or a shape the "+
				"schema declares that nothing decodes, which is a schema defect rather than a Go one",
				s.def, len(unexplained), strings.Join(names, ", "), unexplained)
		}
	}

	// A rule nothing exercises is a rule that has stopped being true without
	// anything saying so, and an exemption in that state licenses exactly the
	// drift it was written to expose.
	for name := range boundary {
		if exempted[name] == 0 {
			t.Errorf("the dispatch-boundary rule exempts %q and no position consumed it: every "+
				"position declaring it by reference now carries a json key of that name. Either the "+
				"field has moved into the params shapes, in which case the rule is false and goes, "+
				"or the schema has stopped spelling it by reference, in which case the derivation "+
				"below is reading the wrong position", name)
		}
	}
}

// TestWireRoutedParamsDeclareTheBoundaryFields closes the direction the walk
// above structurally cannot see.
//
// The walk compares what the schema declares against what the Go carries, so a
// property the schema stops declaring altogether leaves it nothing to compare:
// the boundary fields have no field on a params shape by construction, so
// dropping one from a params position produces no disagreement anywhere and the
// published contract quietly starts refusing a field the session still reads.
//
// The subject set is the schema's own per-method dispatch — the request envelope
// pairs each method with the position its params must take — so a method added
// is covered the moment it is routed, and nothing here is keyed on how a
// position is named.
//
// Every routed position must DECLARE each boundary field. How it declares it is
// not this test's business: by reference means the shared field the session
// reads, inline means the shape owns one, and the walk above already holds the
// inline case to a Go field. Absent is the only failure, and it is the one that
// otherwise passes in silence.
//
// The positions reached by the notification dispatch are deliberately not
// subjects. A notification has no response channel to refuse on, so the session
// reads no token off one, and those positions correctly declare neither field.
func TestWireRoutedParamsDeclareTheBoundaryFields(t *testing.T) {
	t.Parallel()

	doc, _ := compileWireSchema(t)
	boundary := wireBoundaryPropertyNames(t, doc)

	envelope, err := resolveSchemaPointer(doc, "#/$defs/Request")
	if err != nil {
		t.Fatalf("#/$defs/Request: %v — the routed-method population is derived from the request "+
			"envelope's dispatch, so it cannot be read at all if the envelope is not there", err)
	}
	dispatch, ok := envelope["allOf"].([]any)
	if !ok || len(dispatch) == 0 {
		t.Fatalf("#/$defs/Request declares no per-method dispatch, so this gate would walk an empty " +
			"population and report every routed method clean without reading one")
	}

	routed := map[string]bool{}
	for i, entry := range dispatch {
		object, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		then, ok := object["then"].(map[string]any)
		if !ok {
			continue
		}
		properties, ok := then["properties"].(map[string]any)
		if !ok {
			continue
		}
		params, ok := properties["params"].(map[string]any)
		if !ok {
			continue
		}
		reference, ok := params["$ref"].(string)
		if !ok {
			t.Errorf("#/$defs/Request/allOf/%d pairs a method with a params shape written out in "+
				"place rather than with a named position, so this gate cannot tell which position "+
				"that method routes to", i)
			continue
		}
		routed[reference] = true
	}
	if len(routed) == 0 {
		t.Fatalf("the request envelope's dispatch named no params position, so the derivation is " +
			"reading the wrong shape and this gate covers nothing")
	}

	for _, pointer := range sortedKeys(routed) {
		position, err := resolveSchemaPointer(doc, pointer)
		if err != nil {
			t.Errorf("%s: %v — a method routes to a position the schema does not declare", pointer, err)
			continue
		}
		properties, _ := position["properties"].(map[string]any)
		var absent []string
		for _, name := range sortedKeys(boundary) {
			if _, declared := properties[name]; !declared {
				absent = append(absent, name)
			}
		}
		if len(absent) > 0 {
			t.Errorf("%s is the params position of a routed method and declares neither a shared nor "+
				"an owned %v. The session reads that field off the raw request before it knows which "+
				"params shape the request has, so it is read on this method whatever the schema says "+
				"— and a position that does not declare it publishes a contract refusing a field the "+
				"session accepts", pointer, absent)
		}
	}
}

// sortedKeys returns a map's keys in order, so a failure lists them the same way
// on every run.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// wireBoundaryPropertyNames returns the property names the session reads off a
// raw request before it routes.
//
// # The rule, and why it is two clauses rather than two names
//
// A handful of fields carry one meaning on every method, so the session decodes
// them at the routing boundary — before it knows which params shape the request
// even has. A params shape therefore declares no field for them, and their
// absence from it is correct rather than drift.
//
// Stating that as a list of the affected positions would be a list that goes
// quiet the moment a method is added. Stating it as a list of field NAMES would
// be wider than the truth: several shapes here declare a field of the same name
// and mean their own, and a name-level exemption would swallow a real drop on
// every one of them.
//
// So the rule is derived from the schema, in two clauses, and the schema already
// draws both:
//
//   - The NAMES come from the boundary position itself, whose Go home is the
//     pair of shapes that decode those fields. A field added there widens the
//     rule; a field removed narrows it. Neither needs an edit here.
//   - A property is covered only where it is declared BY REFERENCE to that same
//     shared declaration. A shape declaring a field of the same name INLINE is
//     saying it owns one, and it must have the Go field to prove it.
//
// The second clause is what keeps the rule from being a hole over the name, and
// it is load-bearing rather than theoretical: shapes on this wire do declare
// their own field of a boundary name, and each is held to it.
//
// The rule self-asserts in both directions. A position that stops declaring a
// boundary field by reference is no longer covered, so its Go type must carry
// the field or the walk above refuses. A Go type that gains a json key for a
// field still declared by reference is reported as a second home rather than
// quietly accepted. And a rule no position consumes fails as dead.
func wireBoundaryPropertyNames(t *testing.T, doc map[string]any) map[string]bool {
	t.Helper()

	position, err := resolveSchemaPointer(doc, wireBoundaryParamsDef)
	if err != nil {
		t.Fatalf("%s: %v — the dispatch-boundary rule is derived from this position, so it cannot "+
			"be stated at all if the position is not there", wireBoundaryParamsDef, err)
	}
	properties, ok := position["properties"].(map[string]any)
	if !ok || len(properties) == 0 {
		t.Fatalf("%s declares no properties, so the dispatch-boundary rule would exempt nothing "+
			"and every position resting on it would report its boundary fields as dropped",
			wireBoundaryParamsDef)
	}

	out := map[string]bool{}
	for name, declaration := range properties {
		object, ok := declaration.(map[string]any)
		if !ok {
			t.Fatalf("%s: property %q is not a subschema object", wireBoundaryParamsDef, name)
		}
		if _, byRef := object["$ref"].(string); !byRef {
			t.Fatalf("%s: property %q is declared inline rather than by reference, so the shared "+
				"declaration every other position points at does not exist and the rule cannot tell "+
				"a position that means the shared field from one that means its own",
				wireBoundaryParamsDef, name)
		}
		out[name] = true
	}
	return out
}

// wireSchemaProperty is one property declaration the walk found.
type wireSchemaProperty struct {
	// name is the property key, which is the json key a Go type must produce.
	name string
	// path is where the declaration sits inside the walked position, so a
	// failure names a place rather than only a key.
	path string
	// byRef reports whether the declaration is a reference rather than a shape
	// written out in place. It is what separates a shape pointing at a shared
	// declaration from one stating its own.
	byRef bool
}

// wireSchemaProperties walks one schema position and returns every property it
// declares, at any depth below that position.
//
// It descends through every keyword that can carry a subschema: properties,
// patternProperties, additionalProperties, items in both its object and array
// spellings, prefixItems, the boolean combinators, the if/then/else trio,
// contains and not. A pattern key is not a property name — the key is a pattern
// and the names it admits are data — so patternProperties is descended into
// without being recorded.
//
// It descends into neither $ref nor a definition object, and the two omissions
// are the same decision: a definition is a position in its own right, walked at
// its own row by the pairing gate, so reaching it from here would report one
// drift once per referrer. The file comment says why that is safe, and the
// container test asserts both halves.
func wireSchemaProperties(node any, path string) []wireSchemaProperty {
	var out []wireSchemaProperty
	var walk func(any, string)
	walk = func(value any, at string) {
		object, ok := value.(map[string]any)
		if !ok {
			return
		}
		if properties, ok := object["properties"].(map[string]any); ok {
			for name, declaration := range properties {
				byRef := false
				if shape, ok := declaration.(map[string]any); ok {
					_, byRef = shape["$ref"].(string)
				}
				out = append(out, wireSchemaProperty{
					name:  name,
					path:  at + "/properties/" + name,
					byRef: byRef,
				})
				walk(declaration, at+"/properties/"+name)
			}
		}
		if members, ok := object["patternProperties"].(map[string]any); ok {
			for key, member := range members {
				walk(member, at+"/patternProperties/"+key)
			}
		}
		for _, keyword := range []string{"additionalProperties", "items", "if", "then", "else", "not", "contains"} {
			if member, ok := object[keyword].(map[string]any); ok {
				walk(member, at+"/"+keyword)
			}
		}
		for _, keyword := range []string{"allOf", "anyOf", "oneOf", "items", "prefixItems"} {
			members, ok := object[keyword].([]any)
			if !ok {
				continue
			}
			for i, member := range members {
				walk(member, fmt.Sprintf("%s/%s/%d", at, keyword, i))
			}
		}
	}
	walk(node, path)
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

// wireJSONKeys walks a Go type collecting every json key its tree can produce.
//
// The derivation follows the encoder's own: a field tagged "-" produces no key,
// a tagged field's key is the name before the first comma, and a field with no
// name — tagged or untagged — produces a key spelled as the Go field itself. An
// embedded field with no name of its own contributes its own type's keys rather
// than a key, which is how a shape whose fields marshal onto its container is
// reached.
func wireJSONKeys(typ reflect.Type, seen map[reflect.Type]bool, out map[string]bool) {
	if typ == nil || seen[typ] {
		return
	}
	seen[typ] = true
	switch typ.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
		wireJSONKeys(typ.Elem(), seen, out)
	case reflect.Struct:
		for i := range typ.NumField() {
			field := typ.Field(i)
			tag := field.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name, _, _ := strings.Cut(tag, ",")
			if field.Anonymous && name == "" {
				wireJSONKeys(field.Type, seen, out)
				continue
			}
			if !field.IsExported() {
				continue
			}
			if name == "" {
				name = field.Name
			}
			out[name] = true
			wireJSONKeys(field.Type, seen, out)
		}
	}
}

// TestWireSchemaWalkDescendsIntoEveryContainer is the needle under the walk.
//
// A walk that fails to descend through one keyword returns a clean verdict over
// a surface it never read, and nothing about a clean verdict says which of the
// two it is. So each container keyword carries a planted name here, and the walk
// must return all of them.
//
// The reference case is the deliberate negative: the walk must NOT return a name
// reachable only by following a reference, because the pairing gate walks the
// referenced position at its own row instead. That assertion is what makes the
// non-descent a measured property, and it is the half a needle over positive
// cases alone would miss.
func TestWireSchemaWalkDescendsIntoEveryContainer(t *testing.T) {
	t.Parallel()

	// The fixture is a whole document and the walk is given one position inside
	// it, which is how it is called everywhere else. The referenced shape
	// therefore sits outside the walked position, exactly as it does in the
	// shipped schema, and reaching it would take following the reference.
	const fixture = `{
	  "$defs": {
	    "Subject": {
	      "properties": {
	        "underProperties": {
	          "properties": {"underNestedProperties": {"type": "string"}}
	        },
	        "underItemsObject": {"items": {"properties": {"insideItems": {"type": "string"}}}},
	        "underItemsArray": {"items": [{"properties": {"insideItemsArray": {"type": "string"}}}]},
	        "underPrefixItems": {"prefixItems": [{"properties": {"insidePrefixItems": {"type": "string"}}}]},
	        "underAdditional": {"additionalProperties": {"properties": {"insideAdditional": {"type": "string"}}}},
	        "underPattern": {"patternProperties": {"^x": {"properties": {"insidePattern": {"type": "string"}}}}},
	        "underContains": {"contains": {"properties": {"insideContains": {"type": "string"}}}},
	        "underBoolean": true,
	        "byReference": {"$ref": "#/$defs/Referenced"}
	      },
	      "allOf": [{"properties": {"insideAllOf": {"type": "string"}}}],
	      "anyOf": [{"properties": {"insideAnyOf": {"type": "string"}}}],
	      "oneOf": [{"properties": {"insideOneOf": {"type": "string"}}}],
	      "if": {"properties": {"insideIf": {"type": "string"}}},
	      "then": {"properties": {"insideThen": {"type": "string"}}},
	      "else": {"properties": {"insideElse": {"type": "string"}}},
	      "not": {"properties": {"insideNot": {"type": "string"}}}
	    },
	    "Referenced": {"properties": {"insideReference": {"type": "string"}}}
	  }
	}`

	var document map[string]any
	if err := json.Unmarshal([]byte(fixture), &document); err != nil {
		t.Fatalf("the container fixture does not decode as JSON: %v", err)
	}
	position, err := resolveSchemaPointer(document, "#/$defs/Subject")
	if err != nil {
		t.Fatalf("the container fixture does not carry the position the walk is given: %v", err)
	}

	found := map[string]bool{}
	byRef := map[string]bool{}
	for _, property := range wireSchemaProperties(position, "") {
		found[property.name] = true
		byRef[property.name] = property.byRef
	}

	for _, planted := range []string{
		"underProperties", "underNestedProperties",
		"underItemsObject", "insideItems",
		"underItemsArray", "insideItemsArray",
		"underPrefixItems", "insidePrefixItems",
		"underAdditional", "insideAdditional",
		"underPattern", "insidePattern",
		"underContains", "insideContains",
		"insideAllOf", "insideAnyOf", "insideOneOf",
		"insideIf", "insideThen", "insideElse", "insideNot",
		"underBoolean", "byReference",
	} {
		if !found[planted] {
			t.Errorf("the walk did not return %q, so it does not descend through the keyword that "+
				"carries it and every position whose surface lies behind that keyword is being "+
				"reported clean without being read", planted)
		}
	}

	if found["insideReference"] {
		t.Error("the walk returned a name reachable only by following a reference. It is not supposed " +
			"to follow one: a referenced position is walked at its own row, and inlining it here " +
			"would report one drift once per referrer and would flatten the request envelope's " +
			"per-method dispatch into the envelope's own surface")
	}

	if !byRef["byReference"] {
		t.Error("a property declared as a reference was not reported as one, so the dispatch-boundary " +
			"rule's second clause — cover a field only where it points at the shared declaration — " +
			"cannot tell a reference from a shape written out in place, and would widen into a rule " +
			"over the bare name")
	}
	for _, inPlace := range []string{"underProperties", "underBoolean"} {
		if byRef[inPlace] {
			t.Errorf("%q is declared in place and was reported as a reference, which would hand the "+
				"dispatch-boundary rule to every shape that declares a field of a covered name",
				inPlace)
		}
	}
}
