// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package quantity

import (
	"errors"
	"math"
	"strings"
	"testing"
)

// parseNoPanic calls Parse and fails the test, rather than crashing it, if Parse
// panics: the property under test for a refused input is a returned error.
func parseNoPanic(t *testing.T, s string) (q Quantity, err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Parse(%q) panicked: %v", s, r)
		}
	}()
	return Parse(s)
}

// TestParseAccepts is the accept half of the grammar corpus: every row is a
// string the grammar admits, and each is read back exactly as written.
func TestParseAccepts(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"plain integer", "2"},
		{"plain zero", "0"},
		{"leading zeros", "007"},
		{"plain decimal", "0.5"},
		{"leading point", ".5"},
		{"trailing point", "1."},
		{"trailing point before a suffix", "1.G"},
		{"millicpu", "100m"},
		{"fractional binary", "1.5Gi"},
		{"binary kibi", "2Ki"},
		{"every binary suffix", "1Ei"},
		{"decimal kilo", "1k"},
		{"decimal exa", "12E"},
		{"nano suffix", "5n"},
		{"micro suffix", "4u"},
		{"exponent", "1e3"},
		{"exponent upper case", "1E3"},
		{"exponent with plus", "1E+3"},
		{"negative exponent", "1e-3"},
		{"fractional mantissa with exponent", "1.5e3"},
		{"trailing point with exponent", "5.e3"},
		{"negative", "-1"},
		{"explicit plus", "+1"},
		{"negative millicpu", "-500m"},
		{"negative zero", "-0"},
		{"sub-nano precision", "0.1n"},
		{"exponent at the edge of 64 bits", "1e9223372036854775807"},
		{"long coefficient", "99999999999999999999"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q, err := parseNoPanic(t, c.in)
			if err != nil {
				t.Fatalf("Parse(%q) refused a quantity: %v", c.in, err)
			}
			if q.String() != c.in {
				t.Errorf("Parse(%q).String() = %q, want the input as written", c.in, q.String())
			}
		})
	}
}

// TestParseRefuses is the refuse half: one fault per row, each asserted to be
// a *SyntaxError carrying the reason naming that fault. The first
// row is the positive control that keeps the table honest — the same shape,
// fault removed, is accepted.
func TestParseRefuses(t *testing.T) {
	if _, err := parseNoPanic(t, "500m"); err != nil {
		t.Fatalf("positive control: Parse(%q) refused: %v", "500m", err)
	}

	cases := []struct {
		name   string
		in     string
		reason string
	}{
		{"a word", "lots", "does not begin with a number"},
		{"a unit word", "1.5 cores", `" cores" is not a unit suffix or an exponent`},
		{"a spelled unit", "1 gigabyte", `" gigabyte" is not a unit suffix or an exponent`},
		{"a brace and a newline", "{\n}", "does not begin with a number"},
		{"a fractional exponent", "1e1.5", "an exponent is a whole number"},
		{"a fractional exponent with no whole part", "1e.5", "is not a unit suffix or an exponent"},
		{"a trailing-point exponent", "1e2.", "an exponent is a whole number"},
		{"a suffix with no number", "m", "does not begin with a number"},
		{"a binary suffix with no number", "Ki", "does not begin with a number"},
		{"a sign alone", "+", "does not begin with a number"},
		{"a point alone", ".", "does not begin with a number"},
		{"a point before a suffix", ".Ki", "does not begin with a number"},
		{"an exponent with no number", "e5", "does not begin with a number"},
		{"what an empty template leaves before a literal suffix", "Mi", "does not begin with a number"},
		{"empty", "", "it is empty"},
		{"leading whitespace", " 1", "begins with whitespace"},
		{"trailing whitespace", "1 ", "ends with whitespace"},
		{"a trailing newline", "1Gi\n", "ends with whitespace"},
		{"an exponent with no digits", "1e", `"e" is not a unit suffix or an exponent`},
		{"an exponent sign with no digits", "1E+", `"E+" is not a unit suffix or an exponent`},
		{"an exponent that does not fit in 64 bits", "1e9223372036854775808", "does not fit in 64 bits"},
		{"an upper-case kilo", "1K", `"K" is not a unit suffix or an exponent`},
		{"a lower-case binary suffix", "1ki", `"ki" is not a unit suffix or an exponent`},
		{"two suffixes", "1Kim", `"Kim" is not a unit suffix or an exponent`},
		{"an exponent and a suffix", "1e3m", `"e3m" is not a unit suffix or an exponent`},
		{"two signs", "--1", "does not begin with a number"},
		{"two points", "1.5.5", `".5" is not a unit suffix or an exponent`},
		{"a non-ASCII digit", "１", "does not begin with a number"},
		{"a hexadecimal number", "0x10", `"x10" is not a unit suffix or an exponent`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseNoPanic(t, c.in)
			if err == nil {
				t.Fatalf("Parse(%q) accepted a string outside the grammar", c.in)
			}
			var se *SyntaxError
			if !errors.As(err, &se) {
				t.Fatalf("Parse(%q) error is not a *SyntaxError: %T", c.in, err)
			}
			if se.Value != c.in {
				t.Errorf("SyntaxError.Value = %q, want %q", se.Value, c.in)
			}
			if !strings.Contains(se.Reason, c.reason) {
				t.Errorf("Parse(%q) reason = %q, want it to contain %q", c.in, se.Reason, c.reason)
			}
		})
	}
}

// TestExactInt64 reads the parsed value back in a chosen unit. Each row checks
// one value in one unit; the refusals are values that are not a whole number of
// that unit, or whose count does not fit in an int64.
func TestExactInt64(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		scale int
		want  int64
		ok    bool
	}{
		{"plain bytes", "1048576", 0, 1048576, true},
		{"mebibytes", "128Mi", 0, 134217728, true},
		{"fractional gibibytes are whole bytes", "1.5Gi", 0, 1610612736, true},
		{"decimal megabytes", "128M", 0, 128000000, true},
		{"exponent", "1e3", 0, 1000, true},
		{"exponent on a fraction", "1.5e3", 0, 1500, true},
		{"milli that is whole", "1000m", 0, 1, true},
		{"negative whole", "-5", 0, -5, true},
		{"negative zero is zero", "-0", 0, 0, true},
		{"zero with a huge exponent is zero", "0e9223372036854775807", 0, 0, true},
		{"largest int64", "9223372036854775807", 0, math.MaxInt64, true},
		{"smallest int64", "-9223372036854775808", 0, math.MinInt64, true},
		{"millicpu in nanocores", "500m", -9, 500000000, true},
		{"cores in nanocores", "2", -9, 2000000000, true},
		{"nano in nanocores", "5n", -9, 5, true},
		{"micro in nanocores", "4u", -9, 4000, true},
		{"binary cpu in nanocores", "1Ki", -9, 1024000000000, true},

		{"half a byte", "0.5", 0, 0, false},
		{"a milli byte", "1500m", 0, 0, false},
		{"micro bytes", "4u", 0, 0, false},
		{"a fraction too fine for its digits", "10m", 0, 0, false},
		{"a tenth of a nanocore", "0.1n", -9, 0, false},
		{"one past the largest int64", "9223372036854775808", 0, 0, false},
		{"eight exbibytes", "8Ei", 0, 0, false},
		{"an exponent past int64", "1e19", 0, 0, false},
		{"a huge exponent", "1e9223372036854775807", 0, 0, false},
		{"a huge negative exponent", "1e-9223372036854775808", 0, 0, false},
		{"more cores than int64 nanocores", "9223372037", -9, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q, err := parseNoPanic(t, c.in)
			if err != nil {
				t.Fatalf("Parse(%q): %v", c.in, err)
			}
			got, ok := q.ExactInt64(c.scale)
			if ok != c.ok || got != c.want {
				t.Errorf("Parse(%q).ExactInt64(%d) = (%d, %v), want (%d, %v)", c.in, c.scale, got, ok, c.want, c.ok)
			}
		})
	}
}

// TestSignAndPlain pins the two readings a converter takes off the written form.
func TestSignAndPlain(t *testing.T) {
	cases := []struct {
		in    string
		sign  int
		plain bool
	}{
		{"2", 1, true},
		{".5", 1, true},
		{"1.", 1, true},
		{"0", 0, true},
		{"-0", 0, false},
		{"+1", 1, false},
		{"-1", -1, false},
		{"500m", 1, false},
		{"1e3", 1, false},
		{"2Ki", 1, false},
	}
	for _, c := range cases {
		q, err := parseNoPanic(t, c.in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.in, err)
		}
		if q.Sign() != c.sign || q.Plain() != c.plain {
			t.Errorf("Parse(%q): Sign()=%d Plain()=%v, want %d %v", c.in, q.Sign(), q.Plain(), c.sign, c.plain)
		}
	}
}

// FuzzParse holds the two properties every input must keep: Parse returns
// rather than panics, and an accepted string reads back exactly as written.
func FuzzParse(f *testing.F) {
	for _, seed := range []string{"500m", "1.5Gi", "1e3", "lots", "1e1.5", "m", "", " 1", "9Ei", "-0", "1e9223372036854775808"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		q, err := parseNoPanic(t, s)
		if err != nil {
			var se *SyntaxError
			if !errors.As(err, &se) || se.Value != s {
				t.Fatalf("Parse(%q) returned an error that is not its refusal: %v", s, err)
			}
			return
		}
		if q.String() != s {
			t.Fatalf("Parse(%q).String() = %q", s, q.String())
		}
		for _, scale := range []int{0, -9} {
			_, _ = q.ExactInt64(scale)
		}
	})
}
