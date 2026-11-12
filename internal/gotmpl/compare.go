// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package gotmpl

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"strconv"
	"text/template"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredshape"
)

// AssertionComparisons returns the bindings of `eq`, `ne`, `lt`, `le`, `gt` and
// `ge` that the assertion surface and the value-template surfaces install over
// the template engine's built-ins of the same names. A template's own functions
// are looked up before the engine's, so a surface merging this set over its own
// reads every comparison the way described here, and a surface that does not
// keeps the engine's.
//
// They differ from the engine's in one respect: every number is a number. A
// number an author wrote reaches a template as an integer or a decimal of its own
// kind, while a number written in the template itself arrives as an integer or a
// floating-point value, and the engine refuses to compare across those kinds at
// all — an integer with a decimal included. So `lt
// .values.replicas 2` either stops the evaluation or, against a quoted literal,
// compares digits as text. Here two numbers compare by
// value whatever kind carries them — exactly, so 3 equals 3.0 and 10 is greater
// than 9 — and a number never compares with text: `eq .values.replicas "3"` is a
// comparison that cannot be made, refused naming both kinds.
//
// Everything else answers as the engine's built-ins do: text with text, true or
// false with true or false, no value with no value, and `eq` taking several
// values to compare the first against. Where the engine would refuse, these
// refuse too, and every refusal names the two kinds of value in an author's
// words — a number, text, a true/false value, a list, a map, no value — never a
// Go type. The engine prefixes the refusal with the action that made it, so the
// path the value was read from stands beside the sentence.
//
// The comparison involves no formatting and no locale, so the same values give
// the same answer on every machine. A fresh map is returned on each call.
func AssertionComparisons() template.FuncMap {
	return template.FuncMap{
		"eq": compareEq,
		"ne": compareNe,
		"lt": compareLt,
		"le": compareLe,
		"gt": compareGt,
		"ge": compareGe,
	}
}

// valueKind is the kind of value a comparison sees, in the terms an author
// writes values in. The kinds and the words a refusal names them by have one
// home, shared with every other message that names the shape of an authored
// value.
type valueKind = authoredshape.Kind

const (
	kindAbsent = authoredshape.Absent
	kindNumber = authoredshape.Number
	kindText   = authoredshape.Text
	kindBool   = authoredshape.Bool
	kindList   = authoredshape.List
	kindMap    = authoredshape.Map
	kindOther  = authoredshape.Other
)

var jsonNumberType = reflect.TypeFor[json.Number]()

// operand is one value a comparison is given: its kind, and the value the
// comparison reads.
type operand struct {
	kind valueKind
	v    reflect.Value
}

// operandOf classifies one argument the engine passes.
func operandOf(arg reflect.Value) operand {
	v, isNil := dereference(concreteValue(arg))
	if !v.IsValid() || isNil {
		return operand{kind: kindAbsent}
	}
	return operand{kind: authoredshape.KindOf(v), v: v}
}

// numberValue returns a number operand exactly, as a rational, or false where
// the value has no exact form — a floating-point infinity or not-a-number.
func numberValue(o operand) (*big.Rat, bool) {
	v := o.v
	if v.Type() == jsonNumberType {
		r, ok := new(big.Rat).SetString(v.String())
		return r, ok
	}
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return new(big.Rat).SetInt64(v.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return new(big.Rat).SetUint64(v.Uint()), true
	case reflect.Float32, reflect.Float64:
		r := new(big.Rat).SetFloat64(v.Float())
		return r, r != nil
	}
	return nil, false
}

// floatValue returns a number operand as a floating-point value, the form two
// numbers are compared in when either has no exact form.
func floatValue(o operand) float64 {
	v := o.v
	if v.Type() == jsonNumberType {
		f, _ := strconv.ParseFloat(v.String(), 64)
		return f
	}
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return float64(v.Uint())
	}
	return v.Float()
}

// orderNumbers compares two number operands: negative, zero or positive as a is
// less than, equal to or greater than b. unordered reports a comparison with a
// not-a-number, which is neither.
func orderNumbers(a, b operand) (order int, unordered bool) {
	ra, okA := numberValue(a)
	rb, okB := numberValue(b)
	if okA && okB {
		return ra.Cmp(rb), false
	}
	fa, fb := floatValue(a), floatValue(b)
	switch {
	case fa < fb:
		return -1, false
	case fa > fb:
		return 1, false
	case fa == fb:
		return 0, false
	}
	return 0, true
}

// orderingRefusal is the refusal of an ordering comparison between two values
// it cannot order. It does not repeat the function's name: the engine writes
// "error calling <name>:" ahead of it.
func orderingRefusal(a, b operand) error {
	return fmt.Errorf("cannot compare %s with %s: it orders two numbers or two pieces of text",
		a.kind.Phrase(), b.kind.Phrase())
}

// order answers an ordering comparison: the sign of a against b, refusing a pair
// it cannot order.
func order(arg1, arg2 reflect.Value) (int, error) {
	a, b := operandOf(arg1), operandOf(arg2)
	switch {
	case a.kind == kindNumber && b.kind == kindNumber:
		o, unordered := orderNumbers(a, b)
		if unordered {
			return 0, errors.New("cannot order a floating-point not-a-number value")
		}
		return o, nil
	case a.kind == kindText && b.kind == kindText:
		x, y := a.v.String(), b.v.String()
		switch {
		case x < y:
			return -1, nil
		case x > y:
			return 1, nil
		}
		return 0, nil
	}
	return 0, orderingRefusal(a, b)
}

// equal answers whether two values are equal, refusing a pair it cannot compare.
func equal(a, b operand) (bool, error) {
	if a.kind == kindAbsent || b.kind == kindAbsent {
		return a.kind == b.kind, nil
	}
	if a.kind != b.kind {
		return false, equalityRefusal(a, b)
	}
	switch a.kind {
	case kindNumber:
		o, unordered := orderNumbers(a, b)
		return !unordered && o == 0, nil
	case kindText:
		return a.v.String() == b.v.String(), nil
	case kindBool:
		return a.v.Bool() == b.v.Bool(), nil
	case kindOther:
		if a.v.Type() == b.v.Type() && a.v.Type().Comparable() {
			return a.v.Interface() == b.v.Interface(), nil
		}
	}
	return false, equalityRefusal(a, b)
}

// equalityRefusal is the refusal of an equality comparison between two values it
// cannot compare. Like [orderingRefusal] it leaves the function's name to the
// engine's prefix.
func equalityRefusal(a, b operand) error {
	return fmt.Errorf("cannot compare %s with %s: it compares a number with a number, text with text, "+
		"or a true/false value with a true/false value", a.kind.Phrase(), b.kind.Phrase())
}

// errNoComparand is the refusal of `eq` given one value and nothing to compare it
// with.
var errNoComparand = errors.New("needs a second value to compare the first with")

// compareEq reports whether arg1 equals any of the values after it.
func compareEq(arg1 reflect.Value, arg2 ...reflect.Value) (bool, error) {
	if len(arg2) == 0 {
		return false, errNoComparand
	}
	a := operandOf(arg1)
	for _, arg := range arg2 {
		same, err := equal(a, operandOf(arg))
		if err != nil {
			return false, err
		}
		if same {
			return true, nil
		}
	}
	return false, nil
}

// compareNe reports whether arg1 differs from arg2.
func compareNe(arg1, arg2 reflect.Value) (bool, error) {
	same, err := equal(operandOf(arg1), operandOf(arg2))
	return !same, err
}

// compareLt reports whether arg1 is less than arg2.
func compareLt(arg1, arg2 reflect.Value) (bool, error) {
	o, err := order(arg1, arg2)
	return err == nil && o < 0, err
}

// compareLe reports whether arg1 is less than or equal to arg2.
func compareLe(arg1, arg2 reflect.Value) (bool, error) {
	o, err := order(arg1, arg2)
	return err == nil && o <= 0, err
}

// compareGt reports whether arg1 is greater than arg2.
func compareGt(arg1, arg2 reflect.Value) (bool, error) {
	o, err := order(arg1, arg2)
	return err == nil && o > 0, err
}

// compareGe reports whether arg1 is greater than or equal to arg2.
func compareGe(arg1, arg2 reflect.Value) (bool, error) {
	o, err := order(arg1, arg2)
	return err == nil && o >= 0, err
}
