// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package quantity

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Shape describes the accepted grammar in one sentence, for a refusal to quote
// to the author who wrote the value. The cores that refuse a value quote this
// rather than each describing the grammar in its own words, so the description
// has one home beside the parser it describes.
const Shape = "a quantity is a number such as 2, 0.5 or 1.5, with an optional sign and at most one of " +
	"an integer exponent (1e3, 5E-2) or a unit suffix (n, u, m, k, M, G, T, P, E, Ki, Mi, Gi, Ti, Pi, Ei)"

// SyntaxError is the refusal [Parse] returns, and every refusal it returns is
// one; match it with errors.As. Value is the string Parse was given and Reason
// is a clause saying what made it not a quantity.
//
// It is an ordinary error rather than a halting condition: a core that refuses
// a quantity reports the condition that fits its own output and carries this
// sentence inside its own.
type SyntaxError struct {
	Value  string
	Reason string
}

// Error renders the refusal as one sentence naming the value and the reason.
func (e *SyntaxError) Error() string {
	return fmt.Sprintf("%q is not a quantity: %s", e.Value, e.Reason)
}

// Quantity is a parsed quantity. Its value is exactly
//
//	sign × coefficient × 10^(exponent + scale) × 2^binary
//
// where the coefficient is the digits of the number with its decimal point
// removed, scale accounts for the decimal suffix and for the digits written
// after the point, exponent is the written exponent, and binary is the power
// of two a binary suffix contributes. The written exponent is held apart from
// the scale rather than summed into it, so a value whose exponent is at the
// edge of 64 bits is still held exactly.
//
// The zero Quantity is the value zero, written as nothing; [Parse] never
// returns it for a string it accepted.
type Quantity struct {
	text     string
	negative bool
	coef     string // decimal digits, leading zeros removed; empty when the value is zero
	exp      int64  // the written exponent; zero when none was written
	scale    int    // the power of ten from the decimal suffix, less the count of fractional digits
	binary   uint   // the power of two from a binary suffix: 0, 10, 20, … 60
	plain    bool   // written as an unsigned number with no suffix
}

var binarySuffixes = map[string]uint{
	"Ki": 10, "Mi": 20, "Gi": 30, "Ti": 40, "Pi": 50, "Ei": 60,
}

var decimalSuffixes = map[string]int{
	"n": -9, "u": -6, "m": -3, "k": 3, "M": 6, "G": 9, "T": 12, "P": 15, "E": 18,
}

// Parse reads s as a quantity in the grammar the package documents, and
// returns its exact value. A string outside that grammar returns a
// [*SyntaxError].
func Parse(s string) (Quantity, error) {
	refuse := func(reason string) (Quantity, error) {
		return Quantity{}, &SyntaxError{Value: s, Reason: reason}
	}
	if s == "" {
		return refuse("it is empty")
	}
	if first, _ := utf8.DecodeRuneInString(s); unicode.IsSpace(first) {
		return refuse("it begins with whitespace")
	}
	if last, _ := utf8.DecodeLastRuneInString(s); unicode.IsSpace(last) {
		return refuse("it ends with whitespace")
	}

	q := Quantity{text: s}
	i := 0
	signed := false
	if s[0] == '+' || s[0] == '-' {
		q.negative = s[0] == '-'
		signed = true
		i++
	}

	wholeStart := i
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	whole := s[wholeStart:i]
	frac := ""
	if i < len(s) && s[i] == '.' {
		i++
		fracStart := i
		for i < len(s) && isDigit(s[i]) {
			i++
		}
		frac = s[fracStart:i]
	}
	if whole == "" && frac == "" {
		return refuse("it does not begin with a number")
	}

	suffix := s[i:]
	if power, ok := binarySuffixes[suffix]; ok {
		q.binary = power
	} else if power, ok := decimalSuffixes[suffix]; ok {
		q.scale = power
	} else if suffix == "" {
		q.plain = !signed
	} else if suffix[0] == 'e' || suffix[0] == 'E' {
		exp, reason := parseExponent(suffix)
		if reason != "" {
			return refuse(reason)
		}
		q.exp = exp
	} else {
		return refuse(fmt.Sprintf("%q is not a unit suffix or an exponent", suffix))
	}

	q.scale -= len(frac)
	q.coef = strings.TrimLeft(whole+frac, "0")
	return q, nil
}

// parseExponent reads an exponent suffix — "e" or "E", an optional sign, then
// digits — and returns its value, or a reason it is not one.
func parseExponent(suffix string) (int64, string) {
	body := suffix[1:]
	j := 0
	if j < len(body) && (body[j] == '+' || body[j] == '-') {
		j++
	}
	digitsStart := j
	for j < len(body) && isDigit(body[j]) {
		j++
	}
	if j == digitsStart {
		return 0, fmt.Sprintf("%q is not a unit suffix or an exponent", suffix)
	}
	if j < len(body) {
		if body[j] == '.' {
			return 0, "an exponent is a whole number"
		}
		return 0, fmt.Sprintf("%q is not a unit suffix or an exponent", suffix)
	}
	exp, err := strconv.ParseInt(body, 10, 64)
	if err != nil {
		return 0, "its exponent does not fit in 64 bits"
	}
	return exp, ""
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// String returns the quantity exactly as it was written.
func (q Quantity) String() string { return q.text }

// Sign returns -1, 0 or +1 as the value is negative, zero or positive. A zero
// written with a minus sign is zero.
func (q Quantity) Sign() int {
	switch {
	case q.coef == "":
		return 0
	case q.negative:
		return -1
	default:
		return 1
	}
}

// Plain reports whether the quantity was written as an unsigned number with no
// suffix and no exponent, such as "2", "0.5" or ".5".
func (q Quantity) Plain() bool { return q.plain }

// maxInt64Digits is one more than the number of decimal digits in the largest
// int64, so any positive whole number with at least this many digits overflows.
const maxInt64Digits = 20

// ExactInt64 returns the value counted in units of 10^scale — scale 0 counts
// ones, scale -9 counts billionths — when that count is a whole number that
// fits in an int64. It reports false when the value is not a whole number of
// those units, or when the count does not fit; it never rounds.
func (q Quantity) ExactInt64(scale int) (int64, bool) {
	if q.coef == "" {
		return 0, true
	}
	n, _ := new(big.Int).SetString(q.coef, 10)
	n.Lsh(n, q.binary)

	// shift is the power of ten still to apply to n. It is summed as a big
	// integer because the written exponent may sit at the edge of 64 bits.
	shift := big.NewInt(q.exp)
	shift.Add(shift, big.NewInt(int64(q.scale)-int64(scale)))

	switch shift.Sign() {
	case 1:
		// n is at least 1, so n × 10^shift overflows once shift reaches the
		// digit count of the largest int64.
		if !shift.IsInt64() || shift.Int64() >= maxInt64Digits-1 {
			return 0, false
		}
		n.Mul(n, new(big.Int).Exp(big.NewInt(10), shift, nil))
	case -1:
		// n is divisible by 10^k only when k is below n's own digit count, so a
		// larger k is a fraction without computing 10^k.
		k := new(big.Int).Neg(shift)
		if !k.IsInt64() || k.Int64() >= int64(len(n.String())) {
			return 0, false
		}
		var rem big.Int
		n.QuoRem(n, new(big.Int).Exp(big.NewInt(10), k, nil), &rem)
		if rem.Sign() != 0 {
			return 0, false
		}
	}
	if q.negative {
		n.Neg(n)
	}
	if !n.IsInt64() {
		return 0, false
	}
	return n.Int64(), true
}
