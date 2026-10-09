// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package gotmpl

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"text/template"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ErrIndexOfAbsentMapKey is the Loud Guardrail halt raised when a template reads
// a map through `index` with a key the map does not hold.
//
// Go's own `index` answers that read with the zero value of the map's element
// type, which for the maps a template reads is a nil interface, and the printer
// writes a nil interface as the literal text "<no value>". The missing-key option
// a surface chooses does not govern `index` at all, so a template printing such a
// read — a credential read by a key name another template computed, say — renders
// the placeholder text into an artifact while the build succeeds. The halt names
// the key so the read that found nothing is the one corrected.
//
// It is a condition of `index` alone. A dot read of an absent key keeps
// following its surface's missing-key policy, and the functions that test for a
// key before reading it — `optional`, and Sprig's `get` and `hasKey` — never
// raise it. Category: whichever the surface evaluating the template charges.
// Match with errors.Is(err, ErrIndexOfAbsentMapKey()).
func ErrIndexOfAbsentMapKey() error { return errIndexOfAbsentMapKey }

var errIndexOfAbsentMapKey = cerrors.Register("index-of-absent-map-key", cerrors.Meta{
	ShortCode: "E0327",
	Message:   "template indexes a map with a key the map does not hold",
	RulePlace: cerrors.RuleNoStageReaches("A template's index call evaluates the rule, at the moment it runs, on every surface " +
		"that evaluates a template — a render, a source template pinned by a pull, a schema predicate, a parameter " +
		"interpolation. The map and the key it judges exist only inside that one evaluation, and the key may itself be " +
		"computed by the template, so no declared stage of a compilation holds them as facts."),
})

// absentMapKeyAdvice is what a halted read is told to do instead. It names the
// presence-testing functions as examples rather than as a set every surface
// registers, because the function sets differ by surface and this one sentence
// reaches every surface that evaluates a template.
const absentMapKeyAdvice = "read a key that may be absent through a function that tests for it first, " +
	"such as optional, get or hasKey"

// BuiltinReplacements returns the bindings that replace a function Go's template
// engine defines itself, and nothing else. Today that is `index`, bound to the
// strict reading described on [ErrIndexOfAbsentMapKey].
//
// [Helpers] carries these bindings too, so every surface taking [Helpers] or
// [FuncMap] already has them. This accessor exists for a surface that builds its
// function set from Sprig directly and deliberately offers no Aggrega helper: it
// merges this set over its own and so reads `index` exactly as every other
// surface does, without being handed helpers it chose not to offer.
//
// A template's own functions are looked up before the engine's built-ins, so a
// binding here replaces the built-in of the same name on every template parsed
// with it. A fresh map is returned on each call.
func BuiltinReplacements() template.FuncMap {
	return template.FuncMap{
		"index": strictIndex,
	}
}

// strictIndex is `index` with one difference from the function Go's template
// engine defines under that name: where a map is indexed with a key it does not
// hold, it halts with [ErrIndexOfAbsentMapKey] naming the key, where the engine's
// own answers with the zero value of the map's element type.
//
// Every other case answers exactly as the engine's does — a slice, an array or a
// string indexed by position, an index out of range, a key of the wrong type, a
// present key whatever it holds (a present key holding nil included), a chain of
// several keys. The signature is the engine's own, so the engine passes the
// arguments and unwraps the result the way it does for the built-in.
//
// 🔴 The body reproduces the engine's function rather than delegating to it,
// because the engine does not export it. That makes it a deliberate
// near-duplicate, held to the engine by a test that runs one template through
// this binding and through the engine's own `index` and requires every answer
// but the absent key to agree — so an upgrade of Go that changes the engine's
// behaviour fails that test instead of leaving the two to drift.
func strictIndex(item reflect.Value, keys ...reflect.Value) (reflect.Value, error) {
	item = concreteValue(item)
	if !item.IsValid() {
		return reflect.Value{}, errors.New("index of untyped nil")
	}
	for _, key := range keys {
		key = concreteValue(key)
		var isNil bool
		if item, isNil = dereference(item); isNil {
			return reflect.Value{}, errors.New("index of nil pointer")
		}
		switch item.Kind() {
		case reflect.Array, reflect.Slice, reflect.String:
			position, err := sequencePosition(key, item.Len())
			if err != nil {
				return reflect.Value{}, err
			}
			// A position equal to the length passes the bound above exactly as it
			// does in the engine, and this call panics with reflect's own message;
			// the engine recovers that panic into the call's error, so the answer
			// is the same as the built-in's.
			item = item.Index(position)
		case reflect.Map:
			mapKey, err := mapKeyOf(key, item.Type().Key())
			if err != nil {
				return reflect.Value{}, err
			}
			found := item.MapIndex(mapKey)
			if !found.IsValid() {
				return reflect.Value{}, &absentMapKeyError{key: keyText(mapKey)}
			}
			item = found
		default:
			return reflect.Value{}, fmt.Errorf("can't index item of type %s", item.Type())
		}
	}
	return item, nil
}

// absentMapKeyError is one read the strict `index` refused. Its text is the
// condition's sentence, the key it did not find and the advice; it unwraps to the
// condition, so the condition's codes reach every frame that wraps it.
//
// It is a plain error rather than a diagnostic envelope on purpose: the engine
// prefixes it with the template's name, the line and column, and the failing
// action as the template spells it, and that position is what tells an author
// which `index` call to change. An envelope here would be rendered in place of
// that prefix rather than beneath it.
type absentMapKeyError struct {
	key string
}

func (e *absentMapKeyError) Error() string {
	return cerrors.ComposeMessage(errIndexOfAbsentMapKey, []string{"key " + e.key}, absentMapKeyAdvice)
}

func (e *absentMapKeyError) Unwrap() error { return errIndexOfAbsentMapKey }

// keyText spells a map key the way a template author wrote it: a string key
// quoted, any other key as its value.
func keyText(key reflect.Value) string {
	if key.Kind() == reflect.String {
		return strconv.Quote(key.String())
	}
	return fmt.Sprint(key.Interface())
}

// concreteValue returns the value an interface holds, the zero reflect.Value for
// a nil interface, and any other value unchanged.
func concreteValue(v reflect.Value) reflect.Value {
	if v.Kind() != reflect.Interface {
		return v
	}
	if v.IsNil() {
		return reflect.Value{}
	}
	return v.Elem()
}

// dereference follows pointers and interfaces to the value at the end, reporting
// whether it met a nil on the way.
func dereference(v reflect.Value) (reflect.Value, bool) {
	for ; v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface; v = v.Elem() {
		if v.IsNil() {
			return v, true
		}
	}
	return v, false
}

// sequencePosition converts an index argument to a position in a sequence of the
// given length.
func sequencePosition(index reflect.Value, length int) (int, error) {
	var x int64
	switch index.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		x = index.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		x = int64(index.Uint())
	case reflect.Invalid:
		return 0, errors.New("cannot index slice/array with nil")
	default:
		return 0, fmt.Errorf("cannot index slice/array with type %s", index.Type())
	}
	if x < 0 || int(x) < 0 || int(x) > length {
		return 0, fmt.Errorf("index out of range: %d", x)
	}
	return int(x), nil
}

// mapKeyOf converts an index argument to a key of the map's key type, a nil
// argument becoming that type's zero where the type admits nil.
func mapKeyOf(key reflect.Value, keyType reflect.Type) (reflect.Value, error) {
	if !key.IsValid() {
		if !admitsNil(keyType) {
			return reflect.Value{}, fmt.Errorf("value is nil; should be of type %s", keyType)
		}
		key = reflect.Zero(keyType)
	}
	if key.Type().AssignableTo(keyType) {
		return key, nil
	}
	if isIntegerKind(key.Kind()) && isIntegerKind(keyType.Kind()) && key.Type().ConvertibleTo(keyType) {
		return key.Convert(keyType), nil
	}
	return reflect.Value{}, fmt.Errorf("value has type %s; should be %s", key.Type(), keyType)
}

// reflectValueType is the type the engine passes reflect.Value arguments as.
var reflectValueType = reflect.TypeFor[reflect.Value]()

// admitsNil reports whether an untyped nil can stand for a value of the type.
func admitsNil(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return true
	case reflect.Struct:
		return t == reflectValueType
	}
	return false
}

// isIntegerKind reports whether a kind is one of Go's integer kinds.
func isIntegerKind(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true
	}
	return false
}
