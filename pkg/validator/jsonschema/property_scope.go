// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package jsonschema

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	libschema "github.com/google/jsonschema-go/jsonschema"
)

// definitionalKeywords are the two spellings a single document may carry its
// reusable sub-schemas under: the 2020-12 keyword and the draft-07 alias.
//
// Both are read wherever one is, because which spelling a document uses is the
// author's choice and a reader that honoured only one would resolve half the
// corpus.
var definitionalKeywords = [...]string{"$defs", "definitions"}

// NewUnderProperty builds a validator over a document that carries schema at
// one named property: the instance handed to the returned validator is shaped
// {property: value}, and findings' paths therefore lead with the property name.
//
// It is the one home for that assembly. Two callers need it — the conformance
// read that measures an authored payload against a contract, and the catalog
// load that refuses a declared contract no validator can be built from — and a
// second assembly written beside either would let the two disagree about which
// contracts are usable. That disagreement is not hypothetical in either
// direction: a load that accepted what the read rejects restores the silent
// drop the refusal exists to remove, and a load that rejects what the read
// accepts refuses correct input.
//
// The definitional context is hoisted onto the document root, because a
// reusable sub-schema is referenced by a document-root JSON Pointer
// ("#/$defs/Name") and the root of the assembled document is this wrapper
// rather than whatever enclosed the schema before. Both the enclosing scope's
// blocks and the schema's own are carried, the schema's own winning a key
// collision as the nearer declaration; enclosing is optional and may be nil.
// Hoisting the schema's own block is what makes a pillar-authored one resolve:
// the compact-form emitter places a definitional block at the root of the
// schema whose body declared it, so a block authored inside the property's own
// body arrives nested one level below the pointer that names it.
//
// Neither argument is mutated. A compiled schema is shared and may be cached,
// so the wrapper is assembled from fresh maps and the inputs are only read.
func NewUnderProperty(property string, schema, enclosing map[string]any) (Validator, error) {
	wrapper := map[string]any{
		"type":       "object",
		"properties": map[string]any{property: schema},
	}
	for _, keyword := range definitionalKeywords {
		merged := mergeDefinitional(enclosing[keyword], schema[keyword])
		if merged != nil {
			wrapper[keyword] = merged
		}
	}

	wrapperJSON, err := json.Marshal(wrapper)
	if err != nil {
		return nil, err
	}
	libSchema := &libschema.Schema{}
	if err := json.Unmarshal(wrapperJSON, libSchema); err != nil {
		return nil, unreadableSchemaError(err)
	}
	if err := rootPointersAtProperty(libSchema, property, schema); err != nil {
		return nil, err
	}
	return New(libSchema)
}

// rootPointersAtProperty makes every pointer written in the schema carried at
// property resolve against that schema, the way a pointer into its definitions
// already does.
//
// A pointer is read from the root of the document it sits in, and here that
// root is the wrapper rather than the schema the author wrote — so, left as
// written, "#/properties/primary" would look for a primary property on the
// wrapper, which holds only property. Each such pointer is therefore re-rooted
// at property's position: "#" becomes "#/properties/<property>" and
// "#/properties/primary" becomes "#/properties/<property>/properties/primary".
// A pointer into the definitions ("#/$defs/…", "#/definitions/…") is left as
// written, because the definitions are hoisted onto the wrapper and resolve
// there already, the enclosing scope's included. A reference by anchor name
// carries no pointer and is left alone.
//
// The pointers re-rooted are those written in the schema itself and in the
// definitions it declares, which are the ones hoisted from it. A re-rooted
// pointer naming nothing is refused here, naming the pointer as the author
// wrote it, since the resolution failure the library would raise names the
// re-rooted spelling instead.
func rootPointersAtProperty(doc *libschema.Schema, property string, schema map[string]any) error {
	base := childPointer("", property)
	var roots []*libschema.Schema
	if carried := doc.Properties[property]; carried != nil {
		roots = append(roots, carried)
	}
	for _, keyword := range definitionalKeywords {
		own, _ := schema[keyword].(map[string]any)
		hoisted := doc.Defs
		if keyword == "definitions" {
			hoisted = doc.Definitions
		}
		names := make([]string, 0, len(own))
		for name := range own {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if def := hoisted[name]; def != nil {
				roots = append(roots, def)
			}
		}
	}
	for _, root := range roots {
		if err := rootPointersIn(root, doc, property, base); err != nil {
			return err
		}
	}
	return nil
}

// rootPointersIn re-roots the pointers of s and of every sub-schema beneath it;
// see rootPointersAtProperty.
func rootPointersIn(s, doc *libschema.Schema, property, base string) error {
	for _, ref := range []*string{&s.Ref, &s.DynamicRef} {
		rooted, ok := pointerRootedAt(*ref, base)
		if !ok {
			continue
		}
		target, err := url.Parse(rooted)
		if err != nil || schemaAtPointer(doc, target.Fragment) == nil {
			return &AdapterError{
				Keyword: "$ref",
				Message: fmt.Sprintf("the reference %q names nothing in the %s schema it is written in: "+
					"a pointer is read from the root of that schema, as %q is", *ref, property, localDefsPrefix+"<Name>"),
				Err: ErrSchemaResolve(),
			}
		}
		*ref = rooted
	}
	for _, child := range subSchemas(s) {
		if err := rootPointersIn(child.schema, doc, property, base); err != nil {
			return err
		}
	}
	return nil
}

// pointerRootedAt returns ref re-rooted at base when ref is a pointer from the
// document root other than one into the definitions, and reports false for any
// other reference.
func pointerRootedAt(ref, base string) (string, bool) {
	if ref != "#" && !strings.HasPrefix(ref, "#/") {
		return "", false
	}
	for _, keyword := range definitionalKeywords {
		if strings.HasPrefix(ref, "#/"+keyword+"/") {
			return "", false
		}
	}
	return "#" + base + strings.TrimPrefix(ref, "#"), true
}

// unreadableSchemaError restates a decode failure over a schema document in
// terms of what the author wrote, because this failure now reaches a diagnostic
// rather than being discarded.
//
// The decoder reports where the fault landed in ITS OWN types — a Go struct
// field path naming the library's internal shapes — and none of that is
// something an author typed, can look up, or can act on. What survives the
// translation is the part that is about the document: a value of one JSON kind
// was written where a schema is required. That is the whole of the diagnosis a
// reader needs in order to recognise the mistake, and the pillar and source line
// the caller already carries are what locate it.
//
// The offending key inside the pillar is deliberately NOT derived here. Working
// it out means deciding which keywords take a schema as their value, which is a
// second answer to what a valid schema is, kept where the schema language is
// already defined rather than grown beside a decoder.
func unreadableSchemaError(err error) error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && typeErr.Value != "" {
		return fmt.Errorf(
			"the contract writes a %s where a nested schema is required", typeErr.Value)
	}
	return errors.New("the contract is not readable as a schema")
}

// mergeDefinitional combines an enclosing scope's definitional block with the
// schema's own, the schema's own winning a collision. It returns nil when
// neither side carries one, so the caller can leave the keyword off the wrapper
// entirely rather than emitting an empty block.
//
// A side that is present but not a map is returned as-is when it is the only
// side, so a malformed block reaches the library and is reported there. Judging
// it here would be a second answer to what a valid schema is.
func mergeDefinitional(enclosing, own any) any {
	enclosingMap, enclosingOK := enclosing.(map[string]any)
	ownMap, ownOK := own.(map[string]any)
	switch {
	case !enclosingOK && !ownOK:
		if own != nil {
			return own
		}
		return enclosing
	case !enclosingOK:
		return own
	case !ownOK:
		if own != nil {
			return own
		}
		return enclosing
	}
	merged := make(map[string]any, len(enclosingMap)+len(ownMap))
	for name, def := range enclosingMap {
		merged[name] = def
	}
	for name, def := range ownMap {
		merged[name] = def
	}
	return merged
}
