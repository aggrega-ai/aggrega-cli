// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonpatch

import (
	"errors"
	"fmt"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ErrTestFailed is returned (wrapped) when an RFC 6902 test operation's
// value does not match the document value at its path. Callers that
// treat a failed test as a controlled outcome rather than a malformed
// patch match on it with errors.Is.
const ErrTestFailed cerrors.Code = "compiler-jsonpatch/test-failed"

var _ = cerrors.Register(ErrTestFailed, cerrors.Meta{
	Message: "jsonpatch: test operation failed",
})

// Op is one RFC 6902 operation the engine applies. It is deliberately a
// local type, structurally identical to the Wasm-ABI patch operation
// but NOT that type: keeping the engine free of the ABI import is what
// makes it a provider-agnostic leaf reusable by every patch producer
// (the feature-recipe-pipeline step output, the mutator socket, a test
// harness). A caller that holds an ABI-shaped operation maps it onto Op
// at its own call site; the mapping lives in exactly that one place. Do
// not "unify" this with the ABI type — doing so drags an ABI dependency
// into a leaf engine and couples it to one producer.
type Op struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
	From  string `json:"from,omitempty"`
}

// Apply mutates doc in place by applying ops in order. On the first
// failing operation it returns an error naming the operation's index,
// op, and path; doc may be partially mutated at that point, so a caller
// that needs all-or-nothing semantics snapshots doc before the call and
// restores it on error. doc's root must be a JSON object (the shape of
// every rendered manifest the pipeline patches).
func Apply(doc *types.OrderedMap[any], ops []Op) error {
	if doc == nil {
		return errors.New("jsonpatch: nil document")
	}
	for i, op := range ops {
		if err := applyOne(doc, op); err != nil {
			return fmt.Errorf("jsonpatch: op %d (%q %q): %w", i, op.Op, op.Path, err)
		}
	}
	return nil
}

func applyOne(doc *types.OrderedMap[any], op Op) error {
	tokens, err := parsePointer(op.Path)
	if err != nil {
		return err
	}
	switch op.Op {
	case "add":
		return doAdd(doc, tokens, op.Value)
	case "remove":
		_, err := doRemove(doc, tokens)
		return err
	case "replace":
		return doReplace(doc, tokens, op.Value)
	case "test":
		return doTest(doc, tokens, op.Value)
	case "move":
		return doMove(doc, tokens, op.From)
	case "copy":
		return doCopy(doc, tokens, op.From)
	default:
		return fmt.Errorf("unknown operation %q", op.Op)
	}
}

// container is the object or array that holds an operation's final
// reference token. For an object the engine mutates in place (the map
// is a pointer). For an array, length-changing mutations produce a new
// slice that storeArr writes back into the grandparent that holds the
// array.
type container struct {
	obj      *types.OrderedMap[any]
	arr      []any
	storeArr func([]any)
	isArr    bool
}

// navigateToParent walks tokens[:len-1] from the document root and
// returns the container holding the final token plus that token.
// Descending through a missing object key or a non-container value is
// an error (RFC 6902 does not create intermediate locations). tokens
// must be non-empty; whole-document operations are handled before this
// is called.
func navigateToParent(root *types.OrderedMap[any], tokens []string) (*container, string, error) {
	var cur any = root
	// store writes a replacement of cur back into its parent. It is
	// only ever invoked to upgrade a value-form object to a pointer, or
	// to write a length-changed array back up; in-place object and
	// array-element mutations do not need it.
	store := func(any) {}

	for i := 0; i < len(tokens)-1; i++ {
		t := tokens[i]
		switch node := cur.(type) {
		case *types.OrderedMap[any]:
			child, ok := node.Get(t)
			if !ok {
				return nil, "", fmt.Errorf("path segment %q not found", t)
			}
			cur = child
			store = func(v any) { node.Set(t, v) }
		case types.OrderedMap[any]:
			up := node
			store(&up)
			child, ok := up.Get(t)
			if !ok {
				return nil, "", fmt.Errorf("path segment %q not found", t)
			}
			cur = child
			store = func(v any) { up.Set(t, v) }
		case []any:
			idx, _, err := arrayIndex(t, len(node), false)
			if err != nil {
				return nil, "", err
			}
			if idx >= len(node) {
				return nil, "", fmt.Errorf("array index %d out of range (len %d)", idx, len(node))
			}
			arr := node
			cur = arr[idx]
			store = func(v any) { arr[idx] = v }
		default:
			return nil, "", fmt.Errorf("cannot descend through %q: parent is not an object or array", t)
		}
	}

	last := tokens[len(tokens)-1]
	switch node := cur.(type) {
	case *types.OrderedMap[any]:
		return &container{obj: node}, last, nil
	case types.OrderedMap[any]:
		up := node
		store(&up)
		return &container{obj: &up}, last, nil
	case []any:
		// store writes the (any-typed) replacement of this array back
		// into its parent slot; wrap it so length-changing mutations
		// hand it a []any.
		writeBack := store
		return &container{isArr: true, arr: node, storeArr: func(ns []any) { writeBack(ns) }}, last, nil
	default:
		return nil, "", fmt.Errorf("target parent is not an object or array")
	}
}

// getAt resolves the value at tokens, returning (value, true) on a hit
// and (nil, false) when the location is absent. The empty token list
// resolves to the whole document.
func getAt(doc *types.OrderedMap[any], tokens []string) (any, bool) {
	if len(tokens) == 0 {
		return doc, true
	}
	c, last, err := navigateToParent(doc, tokens)
	if err != nil {
		return nil, false
	}
	return c.get(last)
}

func (c *container) get(token string) (any, bool) {
	if !c.isArr {
		return c.obj.Get(token)
	}
	idx, _, err := arrayIndex(token, len(c.arr), false)
	if err != nil || idx >= len(c.arr) {
		return nil, false
	}
	return c.arr[idx], true
}

func doAdd(doc *types.OrderedMap[any], tokens []string, value any) error {
	if len(tokens) == 0 {
		return setRoot(doc, value)
	}
	c, last, err := navigateToParent(doc, tokens)
	if err != nil {
		return err
	}
	if !c.isArr {
		// Object add: create the member, or replace it if it exists.
		c.obj.Set(last, value)
		return nil
	}
	idx, _, err := arrayIndex(last, len(c.arr), true)
	if err != nil {
		return err
	}
	if idx > len(c.arr) {
		return fmt.Errorf("array index %d out of range for add (len %d)", idx, len(c.arr))
	}
	c.storeArr(insertAt(c.arr, idx, value))
	return nil
}

func doRemove(doc *types.OrderedMap[any], tokens []string) (any, error) {
	if len(tokens) == 0 {
		old := deepCopy(doc)
		clearObject(doc)
		return old, nil
	}
	c, last, err := navigateToParent(doc, tokens)
	if err != nil {
		return nil, err
	}
	if !c.isArr {
		old, ok := c.obj.Get(last)
		if !ok {
			return nil, fmt.Errorf("cannot remove %q: member does not exist", last)
		}
		c.obj.Delete(last)
		return old, nil
	}
	idx, _, err := arrayIndex(last, len(c.arr), false)
	if err != nil {
		return nil, err
	}
	if idx >= len(c.arr) {
		return nil, fmt.Errorf("cannot remove array index %d: out of range (len %d)", idx, len(c.arr))
	}
	old := c.arr[idx]
	c.storeArr(removeAt(c.arr, idx))
	return old, nil
}

func doReplace(doc *types.OrderedMap[any], tokens []string, value any) error {
	if len(tokens) == 0 {
		return setRoot(doc, value)
	}
	c, last, err := navigateToParent(doc, tokens)
	if err != nil {
		return err
	}
	if !c.isArr {
		if _, ok := c.obj.Get(last); !ok {
			return fmt.Errorf("cannot replace %q: member does not exist", last)
		}
		c.obj.Set(last, value)
		return nil
	}
	idx, _, err := arrayIndex(last, len(c.arr), false)
	if err != nil {
		return err
	}
	if idx >= len(c.arr) {
		return fmt.Errorf("cannot replace array index %d: out of range (len %d)", idx, len(c.arr))
	}
	// Same-length write: the backing array is shared with the
	// grandparent, so an in-place assignment is already visible.
	c.arr[idx] = value
	return nil
}

func doTest(doc *types.OrderedMap[any], tokens []string, value any) error {
	got, ok := getAt(doc, tokens)
	if !ok {
		return fmt.Errorf("%w: no value at path", ErrTestFailed)
	}
	if !deepEqual(got, value) {
		return ErrTestFailed
	}
	return nil
}

func doMove(doc *types.OrderedMap[any], tokens []string, from string) error {
	if from == "" {
		return errors.New("move requires a non-empty \"from\" location")
	}
	fromTokens, err := parsePointer(from)
	if err != nil {
		return err
	}
	if isProperPrefix(fromTokens, tokens) {
		return fmt.Errorf("cannot move: from %q is a proper prefix of path", from)
	}
	val, ok := getAt(doc, fromTokens)
	if !ok {
		return fmt.Errorf("cannot move: from %q does not exist", from)
	}
	if _, err := doRemove(doc, fromTokens); err != nil {
		return err
	}
	return doAdd(doc, tokens, val)
}

func doCopy(doc *types.OrderedMap[any], tokens []string, from string) error {
	if from == "" {
		return errors.New("copy requires a non-empty \"from\" location")
	}
	fromTokens, err := parsePointer(from)
	if err != nil {
		return err
	}
	val, ok := getAt(doc, fromTokens)
	if !ok {
		return fmt.Errorf("cannot copy: from %q does not exist", from)
	}
	return doAdd(doc, tokens, deepCopy(val))
}

// setRoot replaces the whole document with value, which must be a JSON
// object (the document root is always an object in this engine). The
// existing keys are cleared and value's keys are deep-copied in order.
func setRoot(doc *types.OrderedMap[any], value any) error {
	m, ok := asOrderedMap(value)
	if !ok {
		return fmt.Errorf("whole-document replacement requires a JSON object value")
	}
	clearObject(doc)
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		doc.Set(k, deepCopy(v))
	}
	return nil
}

// clear removes every member from doc in place. Keys() returns a
// snapshot, so deleting while iterating it is safe.
func clearObject(doc *types.OrderedMap[any]) {
	for _, k := range doc.Keys() {
		doc.Delete(k)
	}
}

// insertAt returns a new slice with value inserted at idx (idx == len
// appends). The result is a fresh backing array, so the caller writes
// it back via storeArr rather than relying on in-place mutation.
func insertAt(s []any, idx int, value any) []any {
	out := make([]any, 0, len(s)+1)
	out = append(out, s[:idx]...)
	out = append(out, value)
	out = append(out, s[idx:]...)
	return out
}

// removeAt returns a new slice with the element at idx removed.
func removeAt(s []any, idx int) []any {
	out := make([]any, 0, len(s)-1)
	out = append(out, s[:idx]...)
	out = append(out, s[idx+1:]...)
	return out
}

// isProperPrefix reports whether a is a proper prefix of b (every token
// of a equals the leading tokens of b, and a is strictly shorter). RFC
// 6902 forbids moving a location into one of its own children.
func isProperPrefix(a, b []string) bool {
	if len(a) >= len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
