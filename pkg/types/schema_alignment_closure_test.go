// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Positional Go→schema closure for the kinds whose Go type is hand-written
// against its schema rather than generated from it.
//
// The bidirectional suite in schema_alignment_test.go compares FLATTENED name
// sets in the schema→Go direction and a hand-populated instance in the
// Go→schema direction. Both leave the same hole open, and it is the hole that
// matters for a hand-written type:
//
//   - A flattened comparison cannot see WHERE a name is declared. The pin
//     record's schema declares `digest` on a pin entry, so a `digest` field
//     added to that entry's signature block — which the schema closes to
//     bundle_digest, identity, issuer and mode — resolves against the entry's
//     own property and reads as agreement. The decoder would accept such a key
//     and the schema would refuse the document, which is a build that halts on
//     content the Go type invited.
//   - The instance direction sees only fields the hand-populated instance
//     actually sets. A field added to the Go type and not to that instance is
//     checked by nothing.
//
// So this walk is positional and reflective: it descends the Go type and the
// schema together and, at every position the schema CLOSES to its declared
// properties, requires each yaml tag the decoder would accept there to be a
// property the schema declares AT THAT POSITION. A position the schema leaves
// open is agreement by construction — the schema invites arbitrary keys there,
// so no key can diverge — which is why openness is read from the schema rather
// than assumed.
//
// Scope: the kinds whose Go fields are generated FROM the schema are excluded
// deliberately. For those the property surface and the Go fields have one
// source, and the regeneration check is the stronger guarantee; a comparison
// here would restate a property generation already provides. What is covered is
// what is hand-maintained on both sides, where the two can drift and have.
package types_test

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// closureCase is one kind walked by the positional closure check.
type closureCase struct {
	kind   envelope.Kind
	schema string
	// root is the Go type a decode of the whole document targets.
	root reflect.Type
	// exceptions maps a position path ("containers.*.image") to WHY the yaml
	// tag there has no schema property at the same position. Each must still
	// name a divergence the walk reports, or the staleness guard fails.
	exceptions map[string]string
	// canaries are positions the walk must reach. They are the instrument's
	// own needle: a walker whose reference resolution, map descent or array
	// descent silently stopped working would report zero divergences and read
	// as a pass, so the walk must prove it arrived at positions only a working
	// descent can reach.
	canaries []string
}

func closureCases() []closureCase {
	return []closureCase{
		{
			// The pin record is hand-written, and its typed decode does not
			// reject unknown fields. Since the pins are what make a build
			// reproducible, a pin field the schema refuses would surface as a build
			// that halts on its own lockfile.
			kind:       envelope.KindLockfile,
			schema:     schemas.LockfileV1alpha1,
			root:       reflect.TypeOf(lockfile.Lockfile{}),
			exceptions: map[string]string{},
			canaries: []string{
				"entities[].signature.bundle_digest",
				// Reached only through one array nested inside another.
				"source_artifacts[].referenced_by[].entity",
			},
		},
		{
			// The trust record is hand-written and its typed decode does not reject
			// unknown fields, the same position the pin record is in. A field the
			// schema refuses would surface as a build that halts on a record the
			// product itself wrote.
			kind:       envelope.KindPluginsTrust,
			schema:     schemas.PluginsTrustV1alpha1,
			root:       reflect.TypeOf(pluginstrust.Trust{}),
			exceptions: map[string]string{},
			canaries: []string{
				// Reached only by descending the record array, which is the whole
				// shape here: a walk that stopped at the document root would find
				// only `version` and report agreement.
				"plugins[].resolved_version",
				// Reached only by following the record's own reference into its
				// declarations, which is where the signature fact's closed shape is.
				"plugins[].signature.bundle_digest",
			},
		},
		{
			// The workspace configuration file is hand-written against its schema
			// as well, and carries no envelope, so its walk root is the
			// configuration root type rather than an entity struct.
			kind:   envelope.KindAggregaConfig,
			schema: schemas.AggregaConfigV1alpha1,
			root:   reflect.TypeOf(workspaceconfig.Config{}),
			exceptions: map[string]string{
				// One Go attachment type serves all eight socket lists, and the
				// schema now declares two shapes across them: the sockets whose
				// entry function an attachment may name declare `function`, and
				// the sockets whose entry point is fixed do not. So the decoder
				// accepts the tag at four positions the schema closes without it.
				//
				// Both halves of the divergence are covered, and the two halves
				// are covered differently, which is why they are listed apart
				// rather than as one entry. Schema validation runs on each file
				// before the typed decode, so the value never reaches the decoder
				// at any of the four. Behind that, a provider binding carrying
				// `function` is refused a second time on the composed list — see
				// the provider-socket inert-field check — while an importer entry
				// has no second line, and the schema is its only gate.
				"plugins.providers[].function":        "schema declares two attachment shapes over one Go type; refused by the schema before the decode, and again on the composed provider list",
				"plugins.providers_append[].function": "schema declares two attachment shapes over one Go type; refused by the schema before the decode, and again on the composed provider list",
				"plugins.importer[].function":         "schema declares two attachment shapes over one Go type; refused by the schema before the decode, which is the only gate on this socket",
				"plugins.importer_append[].function":  "schema declares two attachment shapes over one Go type; refused by the schema before the decode, which is the only gate on this socket",
			},
			canaries: []string{
				"lint.publishing_discipline.allow_dynamic_refs",
				// Reached only through one array nested inside another.
				"registries[].trusted_identities[].issuer",
			},
		},
	}
}

// TestSchemaStructAlignment_GoKeysAtClosedPositions walks each hand-written
// kind's Go type against its schema positionally: every yaml tag the decoder
// accepts at a position the schema closes must be declared by the schema at
// that same position.
func TestSchemaStructAlignment_GoKeysAtClosedPositions(t *testing.T) {
	for _, c := range closureCases() {
		t.Run(string(c.kind), func(t *testing.T) {
			var root any
			if err := json.Unmarshal([]byte(c.schema), &root); err != nil {
				t.Fatalf("decode %s schema: %v", c.kind, err)
			}
			doc, ok := root.(map[string]any)
			if !ok {
				t.Fatalf("%s schema root is not an object", c.kind)
			}

			w := &closureWalk{t: t, kind: c.kind, doc: doc}
			w.walk(c.root, doc, "", map[closureStep]bool{})

			// Instrument check first: a walk that reached nothing would report no
			// divergence and pass. Assert it arrived where only a working descent
			// can, before believing anything it did not report. A case with no
			// canary certifies nothing, so its absence is fatal rather than
			// permissive — an empty list is exactly how a silent pass is bought.
			if len(c.canaries) == 0 {
				t.Fatalf("%s carries no canary position, so a pass here would certify nothing about "+
					"whether the walk reached its type at all", c.kind)
			}
			for _, canary := range c.canaries {
				if !w.reached[canary] {
					t.Errorf("%s: the walk never reached %q, so it cannot have compared that subtree; "+
						"its silence about divergences is unsound — repair the walk before trusting it",
						c.kind, canary)
				}
			}

			var unexplained []string
			for _, d := range w.divergences {
				if c.exceptions[d] != "" {
					continue
				}
				unexplained = append(unexplained, d)
			}
			sort.Strings(unexplained)
			if len(unexplained) > 0 {
				t.Errorf("%s: %d yaml tag(s) the decoder accepts at a position the schema closes, with no "+
					"property declared there and no exception: %v\n"+
					"each is a document the decoder would accept and the schema would refuse: either declare "+
					"the property in the schema, drop the Go field, or add a commented exception",
					c.kind, len(unexplained), unexplained)
			}

			// Staleness guard: an exception naming a position the walk no longer
			// reports is the list rotting, and a rotted list suppresses a real
			// divergence the next time the shapes move.
			reported := map[string]bool{}
			for _, d := range w.divergences {
				reported[d] = true
			}
			for path := range c.exceptions {
				if !reported[path] {
					t.Errorf("%s: stale exception %q — the walk reports no divergence there any more; "+
						"remove the exception", c.kind, path)
				}
			}
		})
	}
}

// closureStep is one (Go type, schema position) pair, the cycle guard's key. The
// pair rather than the type alone: one Go type legitimately appears at several
// schema positions, and collapsing them would skip all but the first.
type closureStep struct {
	goType reflect.Type
	path   string
}

// closureWalk accumulates one kind's walk: the positions it reached and the
// divergences it found.
type closureWalk struct {
	t    *testing.T
	kind envelope.Kind
	doc  map[string]any

	reached     map[string]bool
	divergences []string
}

// schemaKeywordsWalked are the structural keywords this walk models.
var schemaKeywordsWalked = map[string]bool{
	"$ref": true, "properties": true, "additionalProperties": true,
	"patternProperties": true, "items": true, "allOf": true, "oneOf": true,
	"if": true, "then": true, "else": true, "$defs": true, "definitions": true,
}

// schemaKeywordsInert are the keywords that constrain a value without changing
// which keys are accepted at a position, so this walk can ignore them.
var schemaKeywordsInert = map[string]bool{
	"$schema": true, "$id": true, "$comment": true, "title": true, "description": true,
	"type": true, "required": true, "enum": true, "const": true, "default": true,
	"examples": true, "deprecated": true, "readOnly": true, "writeOnly": true,
	"format": true, "pattern": true, "minLength": true, "maxLength": true,
	"minimum": true, "maximum": true, "exclusiveMinimum": true, "exclusiveMaximum": true,
	"multipleOf": true, "minItems": true, "maxItems": true, "uniqueItems": true,
	"minProperties": true, "maxProperties": true,
}

// checkKeywords reports any keyword the walk neither models nor can safely
// ignore. A keyword that changes which keys a position accepts — a negation, a
// tuple-typed array, a key-name constraint — would make this walk's answer
// wrong rather than incomplete, so it fails loudly instead of being skipped.
func (w *closureWalk) checkKeywords(node map[string]any, path string) {
	for k := range node {
		if schemaKeywordsWalked[k] || schemaKeywordsInert[k] {
			continue
		}
		w.t.Errorf("%s: schema at %q uses keyword %q, which this walk does not model; "+
			"its verdict for that subtree would be unsound", w.kind, positionLabel(path), k)
	}
}

// resolve follows a local reference to the schema it names. An external or
// unresolvable reference fails the test rather than being skipped, because a
// silently unresolved reference is a whole subtree the walk stops comparing.
func (w *closureWalk) resolve(node map[string]any, path string) map[string]any {
	ref, ok := node["$ref"].(string)
	if !ok {
		return node
	}
	if !strings.HasPrefix(ref, "#/") {
		w.t.Errorf("%s: schema at %q references %q, which is not local to the document; "+
			"this walk cannot follow it", w.kind, positionLabel(path), ref)
		return nil
	}
	cur := any(w.doc)
	for _, seg := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		seg = strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")
		m, ok := cur.(map[string]any)
		if !ok {
			w.t.Errorf("%s: reference %q at %q does not resolve", w.kind, ref, positionLabel(path))
			return nil
		}
		cur, ok = m[seg]
		if !ok {
			w.t.Errorf("%s: reference %q at %q does not resolve", w.kind, ref, positionLabel(path))
			return nil
		}
	}
	out, ok := cur.(map[string]any)
	if !ok {
		w.t.Errorf("%s: reference %q at %q resolves to a non-object schema", w.kind, ref, positionLabel(path))
		return nil
	}
	// A reference beside sibling keywords is a shape this walk does not merge;
	// say so rather than dropping the siblings.
	for k := range node {
		if k != "$ref" && !schemaKeywordsInert[k] {
			w.t.Errorf("%s: schema at %q carries %q alongside $ref; this walk does not merge them",
				w.kind, positionLabel(path), k)
		}
	}
	return out
}

// position is the merged view of one schema position: which property names it
// declares (with the subschema for each), whether it closes to those names,
// and the subschemas a map value or an array element takes.
type position struct {
	properties map[string][]map[string]any
	closed     bool
	values     []map[string]any
	items      []map[string]any
}

// mergePosition folds a schema position and its composition branches into one
// view. Branch properties union, because a key declared in any branch is a key
// some valid document may carry. Closure is read from the position itself and
// from its allOf branches only: those apply to every document, while a oneOf
// arm or an if/then arm applies to some, and treating one arm's closure as the
// position's would report a key the other arm declares.
func (w *closureWalk) mergePosition(node map[string]any, path string, depth int) position {
	pos := position{properties: map[string][]map[string]any{}}
	if depth > 20 {
		w.t.Errorf("%s: schema composition at %q nests deeper than this walk follows", w.kind, positionLabel(path))
		return pos
	}
	node = w.resolve(node, path)
	if node == nil {
		return pos
	}
	w.checkKeywords(node, path)

	if props, ok := node["properties"].(map[string]any); ok {
		for name, sub := range props {
			// A `false` subschema forbids the key in this branch rather than
			// declaring its shape. The name still counts as declared, because
			// another branch may permit it; there is simply nothing to descend
			// into here.
			if m, ok := sub.(map[string]any); ok {
				pos.properties[name] = append(pos.properties[name], m)
				continue
			}
			if _, exists := pos.properties[name]; !exists {
				pos.properties[name] = nil
			}
		}
	}
	switch ap := node["additionalProperties"].(type) {
	case bool:
		if !ap {
			pos.closed = true
		}
	case map[string]any:
		pos.values = append(pos.values, ap)
	}
	if pp, ok := node["patternProperties"].(map[string]any); ok {
		for _, sub := range pp {
			if m, ok := sub.(map[string]any); ok {
				pos.values = append(pos.values, m)
			}
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		pos.items = append(pos.items, items)
	}

	// allOf applies conjunctively, so both its property declarations and its
	// closure bind the position.
	if arr, ok := node["allOf"].([]any); ok {
		for _, sub := range arr {
			m, ok := sub.(map[string]any)
			if !ok {
				continue
			}
			branch := w.mergePosition(m, path, depth+1)
			pos.absorb(branch, true)
		}
	}
	// oneOf arms and the if/then/else arms each describe some documents, so they
	// contribute declared names but never closure.
	for _, key := range []string{"oneOf", "if", "then", "else"} {
		switch v := node[key].(type) {
		case []any:
			for _, sub := range v {
				if m, ok := sub.(map[string]any); ok {
					pos.absorb(w.mergePosition(m, path, depth+1), false)
				}
			}
		case map[string]any:
			pos.absorb(w.mergePosition(v, path, depth+1), false)
		}
	}
	return pos
}

// absorb folds a branch view into the position, taking its closure only when
// the branch binds every document.
func (p *position) absorb(branch position, bindsEveryDocument bool) {
	for name, subs := range branch.properties {
		if _, exists := p.properties[name]; !exists {
			p.properties[name] = nil
		}
		p.properties[name] = append(p.properties[name], subs...)
	}
	p.values = append(p.values, branch.values...)
	p.items = append(p.items, branch.items...)
	if bindsEveryDocument && branch.closed {
		p.closed = true
	}
}

// walk descends a Go type and a schema position together, recording each key it
// matched and each key the decoder accepts that the schema does not declare at
// a closed position.
func (w *closureWalk) walk(goType reflect.Type, node map[string]any, path string, seen map[closureStep]bool) {
	if goType == nil || node == nil {
		return
	}
	if w.reached == nil {
		w.reached = map[string]bool{}
	}
	step := closureStep{goType: goType, path: path}
	if seen[step] {
		return
	}
	seen[step] = true

	switch goType.Kind() {
	case reflect.Ptr:
		w.walk(goType.Elem(), node, path, seen)
		return
	case reflect.Slice, reflect.Array:
		pos := w.mergePosition(node, path, 0)
		for _, item := range pos.items {
			w.walk(goType.Elem(), item, path+"[]", seen)
		}
		return
	case reflect.Map:
		pos := w.mergePosition(node, path, 0)
		for _, v := range pos.values {
			w.walk(goType.Elem(), v, path+".*", seen)
		}
		return
	case reflect.Struct:
		// OrderedMap is a map at the document level: its keys are author data,
		// and its value type occupies the position's value schema. The value
		// type is read through the unexported backing field, whose type
		// information is visible across packages.
		if strings.HasPrefix(goType.Name(), "OrderedMap[") {
			vf, ok := goType.FieldByName("values")
			if !ok || vf.Type.Kind() != reflect.Map {
				w.t.Errorf("%s: cannot read the value type of %s at %q; the ordered-map shape changed and "+
					"this walk would skip the whole subtree", w.kind, goType, positionLabel(path))
				return
			}
			pos := w.mergePosition(node, path, 0)
			for _, v := range pos.values {
				w.walk(vf.Type.Elem(), v, path+".*", seen)
			}
			return
		}
		pos := w.mergePosition(node, path, 0)
		for i := 0; i < goType.NumField(); i++ {
			f := goType.Field(i)
			tag := f.Tag.Get("yaml")
			name := strings.Split(tag, ",")[0]
			inline := strings.Contains(tag, ",inline")
			if tag == "-" {
				continue
			}
			if name == "" {
				// An embedded or inlined field carries its own fields at this
				// same position, so it is walked against this position rather
				// than a child of it.
				if f.Anonymous || inline {
					w.walk(f.Type, node, path, seen)
				}
				continue
			}
			child := path
			if child != "" {
				child += "."
			}
			child += name

			subs, declared := pos.properties[name]
			if !declared {
				// An undeclared key matters only where the schema closes the
				// position. Where it is open the schema invites arbitrary keys,
				// so no key there can diverge from it.
				if pos.closed {
					w.divergences = append(w.divergences, child)
				}
				continue
			}
			w.reached[child] = true
			for _, sub := range subs {
				w.walk(f.Type, sub, child, seen)
			}
		}
		return
	default:
		// A scalar or interface position has no keys to compare.
		return
	}
}

// positionLabel renders a walk path for a message, naming the document root
// explicitly so a report never points at an empty string.
func positionLabel(path string) string {
	if path == "" {
		return "(document root)"
	}
	return path
}
