// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"encoding/json"
	"strconv"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
)

// numbersAsNumbers replaces, in a converted template data tree, every number
// carried as its text with the number itself, so a value template treats a
// number an author wrote as a number: `default` and `empty` take a zero as empty,
// `if` and `with` take it as false, and a comparison with a number written in the
// template compares the two by value.
//
// An authored number reaches the render context as its text, which the template
// engine reads as text: a zero is then true and kept by `default`, and comparing
// it with a number literal stops the evaluation. The text is read into a number by
// rendercore.PlainNumber, the reading the render cores use for the same values,
// and given the print form templateInteger or templateDecimal describes. A
// floating-point value the context already carries as a number rather than as
// its text is given templateDecimal's print form too, so a number prints in
// exponent form on no path that carried it.
//
// The tree is the conversion's own fresh copy, made for one execution, so it is
// rewritten in place and nothing another evaluation reads is touched. Iterating a
// map is safe here where it usually would not be: each step writes only the key it
// is visiting, so no iteration order can reach the result.
func numbersAsNumbers(v any) any {
	switch t := v.(type) {
	case json.Number:
		return templateNumber(t)
	case float64:
		return templateDecimal(t)
	case map[string]any:
		for k, x := range t {
			t[k] = numbersAsNumbers(x)
		}
	case []any:
		for i, x := range t {
			t[i] = numbersAsNumbers(x)
		}
	}
	return v
}

// templateNumber is the number a value template reads for a number carried as
// its text: an integral value as a templateInteger, any other as a
// templateDecimal, and a text that is no number as it stands.
func templateNumber(n json.Number) any {
	switch number := rendercore.PlainNumber(n).(type) {
	case int64:
		return templateInteger(number)
	case float64:
		return templateDecimal(number)
	default:
		return number
	}
}

// templateInteger is an integral number as a value template reads it. Its kind is
// int64, so the template engine and every function reading a value by its kind —
// `if`, `default`, `empty`, the comparisons, arithmetic — take it as an integer.
//
// It prints as its digits wherever a template turns it into text, a `%s` verb
// included: a number an author wrote printed through `printf "%s"` before it was
// read as a number, and still does.
type templateInteger int64

// String is the integer's digits.
func (n templateInteger) String() string {
	return strconv.FormatInt(int64(n), 10)
}

// templateDecimal is a number with no integral reading as a value template reads
// it. Its kind is float64, so the template engine and every function reading a
// value by its kind take it as a number.
//
// It prints in its shortest exact decimal form — the fewest digits that read back
// as the same number, never in exponent form — wherever a template prints it
// directly, turns it into text with `toString`, `quote`, `print` or a `%v` or `%s`
// verb, or writes it as JSON. A floating-point value's own default form switches
// to exponent form for a small or a large magnitude, so 0.00001 would print as
// 1e-05, which is not what the author wrote and not what every consumer reads as
// a number. A number a function computes from it is that function's own value and
// prints in that function's form.
type templateDecimal float64

// String is the number in its shortest exact decimal form.
func (d templateDecimal) String() string {
	return strconv.FormatFloat(float64(d), 'f', -1, 64)
}

// MarshalJSON writes the number in the form String gives, which is a JSON number.
func (d templateDecimal) MarshalJSON() ([]byte, error) {
	return []byte(d.String()), nil
}
