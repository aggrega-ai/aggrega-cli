// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredshape"
)

// decodeParams decodes a request's params into dst, refusing any field dst does
// not declare and naming both what it refused and what the method accepts.
//
// It refuses rather than ignore, because a silently ignored argument reads to a
// caller as an argument that was honoured — and the two outcomes are not close
// together. A caller that sends a field asking for the write to be withheld, and
// has that field dropped, gets the write and a success answer: it asked for a
// rehearsal and got the thing itself. Nothing in the response distinguishes that
// from a request whose field was read, so the caller cannot detect it either.
//
// The declared set comes from dst's own tags rather than from a list kept beside
// each method, so a field added to a params shape is accepted the moment it is
// declared and there is no second place to update. The routing fields below are
// added to that set for the reason stated there.
//
// Params that are absent decode to the zero value, and params that are not an
// object at all are handed to the ordinary decode so the method's own shape fault
// is reported against the shape it expected. Neither is this function's question:
// it decides only whether the object's KEYS are ones the method declares.
func decodeParams(req *jsonRPCRequest, dst any) error {
	if len(req.Params) == 0 {
		return nil
	}
	var supplied map[string]json.RawMessage
	if err := json.Unmarshal(req.Params, &supplied); err != nil {
		// Not an object. The method's own decode owns that complaint, because it
		// alone knows the shape it wanted.
		return decodeParamsValue(req.Params, dst)
	}
	declared := declaredParamFields(dst)
	var unknown []string
	for name := range supplied {
		if !declared[name] && !routingParamFields[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("%s does not accept %s; it accepts: %s",
			req.Method, strings.Join(quotedFields(unknown), ", "), strings.Join(acceptedParamFields(dst), ", "))
	}
	return decodeParamsValue(req.Params, dst)
}

// paramSent reports whether the request's params carry the named key at all, for
// a method that answers a key sent empty differently from a key not sent. The
// decode cannot say which it was, because both leave the field at its zero value.
// A request with no params, or with params that are not an object, sends no key.
func paramSent(req *jsonRPCRequest, name string) bool {
	if len(req.Params) == 0 {
		return false
	}
	var supplied map[string]json.RawMessage
	if err := json.Unmarshal(req.Params, &supplied); err != nil {
		return false
	}
	_, sent := supplied[name]
	return sent
}

// decodeParamsValue performs the decode itself. A value of the wrong shape is
// reported as the field it was sent under, the shape it was sent in and the
// shape the field takes — never as the program's params type, which a client
// cannot see.
func decodeParamsValue(raw json.RawMessage, dst any) error {
	if err := json.Unmarshal(raw, dst); err != nil {
		return authoredshape.DescribeJSONDecodeError(err)
	}
	return nil
}

// noParams is the shape of a method that takes no parameters of its own.
//
// It exists so such a method reaches the decode through the same path as every
// other rather than skipping it, which is what a method with nothing to decode
// otherwise does. Skipping it looks harmless and is not: a caller that sent a
// field to a method with no shape had that field dropped, and read an answer that
// ignored it as an answer that honoured it. That the method writes nothing does
// not close the gap — a filter or a scope a caller believes it applied, silently
// dropped, produces a WIDER answer read as a narrower one.
//
// The routing fields are still accepted, so a request may name its workspace or
// mint a progress token on a method that declares neither.
type noParams struct{}

// routingParamFields are the fields the dispatch path reads off a request's raw
// params, before it knows which params shape the request even has.
//
// They belong to no method's own shape — the routing index pairs a method with a
// handler, never with a shape — so a strict read of one method's declared set
// would refuse them on every well-formed request. They are derived from the two
// shapes that own them rather than listed here, so a field added to either is
// tolerated the moment it is declared: a hand-kept list is the copy that goes
// stale, and it would go stale as a REFUSAL of a field the daemon reads.
var routingParamFields = mergedParamFields(workspaceParams{}, workDoneProgressParams{})

// declaredParamFields is the set of wire names dst declares, keyed for lookup.
//
// An embedded struct's fields are the outer shape's own on the wire, so the walk
// descends into one. A field tagged "-" is not on the wire and is not declared.
func declaredParamFields(dst any) map[string]bool {
	out := map[string]bool{}
	collectParamFields(reflect.TypeOf(dst), out)
	return out
}

// mergedParamFields is the union of the wire names several shapes declare.
func mergedParamFields(shapes ...any) map[string]bool {
	out := map[string]bool{}
	for _, s := range shapes {
		collectParamFields(reflect.TypeOf(s), out)
	}
	return out
}

// collectParamFields walks t's exported fields into out, descending through
// pointers and embedded structs.
func collectParamFields(t reflect.Type, out map[string]bool) {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, ok := wireFieldName(f)
		if !ok {
			continue
		}
		if f.Anonymous && name == "" {
			collectParamFields(f.Type, out)
			continue
		}
		if name != "" {
			out[name] = true
		}
	}
}

// wireFieldName is the name a field carries on the wire: its json tag where it
// has one, otherwise its Go name, which is what the encoding package itself
// falls back to. The second return is false for a field that is not on the wire
// at all — unexported, or tagged "-".
func wireFieldName(f reflect.StructField) (string, bool) {
	if f.PkgPath != "" && !f.Anonymous {
		return "", false
	}
	tag, tagged := f.Tag.Lookup("json")
	if !tagged {
		if f.Anonymous {
			return "", true
		}
		return f.Name, true
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "-" {
		return "", false
	}
	if name == "" {
		if f.Anonymous {
			return "", true
		}
		return f.Name, true
	}
	return name, true
}

// acceptedParamFields is the sorted set a refusal names, so a caller reading one
// learns the whole surface rather than only that its own field was wrong.
//
// The routing fields are included because a caller may legitimately send them on
// any method, and a list that omitted them would tell a caller its workspace
// pointer was inadmissible.
func acceptedParamFields(dst any) []string {
	set := declaredParamFields(dst)
	for name := range routingParamFields {
		set[name] = true
	}
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// quotedFields quotes each name so a refusal reads unambiguously where a field
// name is a word the surrounding sentence also uses.
func quotedFields(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = fmt.Sprintf("%q", n)
	}
	return out
}
