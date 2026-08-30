// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// nullValueArtifact is the text a Go value formatter produces for an
// absent value. A JSON null decodes to an absent Go value, so a schema
// library that formats the offending value into its message emits this
// instead of naming the value the author wrote.
const nullValueArtifact = "<invalid reflect.Value>"

// validatingPrefix opens each segment of the chain a schema library
// prefixes onto its failure text to say where in the schema document the
// failing constraint sits, and libraryRootLabel is that chain's first
// segment when the schema declares no identifier of its own. One is derived
// from the other so the two cannot drift apart.
//
// Both are deliberate second copies of text the library owns, and both are
// read rather than written: the chain is recognised so it can be removed,
// and its first segment so it can be replaced by the name of the definition
// that was actually being checked (see findingMessage).
// TestLibraryNamesACallRootAsRoot pins the agreement, so a library that
// renames the label surfaces as a failing test rather than as a diagnostic
// that silently stops naming the definition it violated.
const (
	validatingPrefix = "validating "
	libraryRootLabel = validatingPrefix + "root: "
)

// findingMessage shapes a schema library's failure text into what a
// finding carries to a terminal.
//
// Three rewrites, and all of them exist because the library's text is
// written for whoever holds the Go value and the schema document rather
// than for whoever wrote the document being checked.
//
// The first names the value. A value formatter renders an absent value
// and a container in Go's own syntax — `<invalid reflect.Value>` for a
// null, `map[b:2 a:1]` for an object — neither of which the author typed
// or can act on. instance is the value the failing call was given, so the
// renderings of it and of every container inside it are known exactly and
// are replaced without having to parse the library's message: a null
// becomes the word the author wrote, and a container becomes its JSON
// type name. A scalar's rendering is left alone, because it is the value
// that actually arrived — for a template expression, the only place the
// reader can see it.
//
// The second removes the location chain. A library prefixes its text with
// where the failing constraint sits inside the schema document — a chain of
// pointer segments through keywords and property names. That is a position
// in the schema, and the reader wrote the instance: the finding's own path
// already says which of their values is at fault, so the chain adds a
// second coordinate system in vocabulary they never used. It goes.
//
// The third names the schema, and it is the reason the chain is removed
// rather than merely truncated. definition is the reusable definition the
// caller substituted for a reference at this position, or empty when it
// substituted none. Where one was substituted it became the root of the
// call, so the library labelled it "root" and the definition's name — the
// one thing telling the reader which declared shape was violated — appeared
// nowhere. Putting the name back is what the reader can act on.
//
// In the type-mismatch message specifically, the JSON type name
// substituted for a container repeats what the library's own `has type`
// clause already states. That redundancy is accepted deliberately: the
// alternatives are a message with a dangling verb, or reshaping the
// library's clause, which means keeping a copy of its grammar that a
// library change would silently invalidate.
func findingMessage(err error, instance any, definition string) string {
	if err == nil {
		return ""
	}
	return nameViolatedDefinition(stripLocationChain(sanitizeMessage(err.Error(), instance)), definition)
}

// nameViolatedDefinition prefixes a message with the reusable definition
// whose constraint the value failed, and leaves the message alone when
// there is no definition to name.
//
// Every diagnostic the walk emits at a position inside a named definition
// goes through here, rather than only the ones a schema library produced.
// A definition may state its constraint as a required key, a key count or a
// closed key set just as readily as a type, and a schema factored into
// named rules would otherwise name the rule for some of its violations and
// not others — which reads as the name being unavailable rather than as the
// inconsistency it is.
func nameViolatedDefinition(message, definition string) string {
	if definition == "" {
		return message
	}
	return localDefsPointer + definition + ": " + message
}

// stripLocationChain removes the leading run of `validating <location>: `
// segments a schema library prefixes onto its failure text.
//
// A segment is recognised only when its location carries no space, which is
// what keeps the removal from eating into the failure text itself: the
// clause after the chain always contains a space, so a message whose chain
// has been consumed stops matching. A location drawn from a document's own
// content could in principle contain the opening word, and it would still
// carry a space and so still stop the walk.
func stripLocationChain(message string) string {
	for strings.HasPrefix(message, validatingPrefix) {
		rest := message[len(validatingPrefix):]
		separator := strings.Index(rest, ": ")
		if separator < 0 {
			return message
		}
		if strings.ContainsRune(rest[:separator], ' ') {
			return message
		}
		message = rest[separator+2:]
	}
	return message
}

// sanitizeMessage rewrites a schema library's failure text so what
// reaches a terminal names the authored value rather than an artifact of
// how the value is held in memory. instance is the value the failing
// validation call was given.
func sanitizeMessage(msg string, instance any) string {
	msg = strings.ReplaceAll(msg, nullValueArtifact, "null")
	for _, container := range containerRenderings(instance) {
		msg = strings.ReplaceAll(msg, container.text, container.jsonType)
	}
	return msg
}

// containerRendering pairs the text a Go value formatter produces for one
// container with the JSON type name that replaces it.
type containerRendering struct {
	text     string
	jsonType string
}

// containerRenderings returns the Go-syntax renderings of every object
// and array reachable from v, longest first.
//
// Longest first is what makes nesting safe: an outer container's
// rendering contains its children's, so replacing the outer one first
// removes them too, and the child entries then match nothing. The
// children are collected because the library validates array elements
// itself, so the value it names in a message may be an element rather
// than the value it was handed.
//
// The order is fully determined — length descending, then the text
// itself — so the same failure produces the same message on every run
// even though the walk over an object's values is unordered.
//
// Two things this deliberately does not guard against, recorded so
// neither reads as an oversight.
//
// It does not detect a value that reaches itself, or one whose parts are
// shared often enough that walking them costs more than reading them. It
// does not need to: the library formats the same value with the same
// formatter before this is ever called, so any value that would exhaust
// this walk has already exhausted that one, and a guard here would
// protect nothing that was not already lost.
//
// It does not treat an empty container's rendering — two characters for
// an array, five for an object — as too short to substitute. For it to
// replace text it should not, the failing value would have to be or
// contain an empty container while the message separately names a schema
// whose own rendering contains the same two characters. The result would
// be a message reading oddly; it is never a wrong verdict and never
// leaves Go syntax behind, which is the property that matters here.
func containerRenderings(v any) []containerRendering {
	var out []containerRendering
	collectContainerRenderings(reflect.ValueOf(v), &out)
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].text) != len(out[j].text) {
			return len(out[i].text) > len(out[j].text)
		}
		return out[i].text < out[j].text
	})
	return out
}

func collectContainerRenderings(rv reflect.Value, out *[]containerRendering) {
	for rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Map:
		value := rv.Interface()
		*out = append(*out, containerRendering{
			text:     fmt.Sprintf("%v", value),
			jsonType: JSONTypeName(value),
		})
		for _, key := range rv.MapKeys() {
			collectContainerRenderings(rv.MapIndex(key), out)
		}
	case reflect.Slice, reflect.Array:
		value := rv.Interface()
		*out = append(*out, containerRendering{
			text:     fmt.Sprintf("%v", value),
			jsonType: JSONTypeName(value),
		})
		for i := 0; i < rv.Len(); i++ {
			collectContainerRenderings(rv.Index(i), out)
		}
	}
}

// JSONTypeName names a decoded value's JSON type — the vocabulary a
// schema author writes in a `type:` keyword — rather than the Go type
// holding it. A diagnostic saying an object was wanted where an array
// was written is actionable; one naming the in-memory representation of
// a list is not.
//
// A value outside the JSON types (which a document decoded from YAML or
// JSON cannot produce) reports "value", so no in-memory type name
// reaches a terminal by way of an unforeseen input.
//
// It is exported because the question it answers — what did a document's
// decoded value turn out to be — is asked outside this package too, by a
// caller shaping advice for a value that arrived as the wrong shape. One
// answer serves both, so a second switch over the same Go types cannot come
// to disagree with this one about what a decoded value is called.
func JSONTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64, float32,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return "number"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	default:
		return "value"
	}
}
