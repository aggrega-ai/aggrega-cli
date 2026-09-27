// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package quantity parses the resource quantities a Workload carries — CPU,
// memory, extended-resource values and volume sizes — in the grammar Kubernetes
// defines for them, and answers exact questions about the parsed value.
//
// It is the single home of that grammar for the plugins that synthesize a
// Workload. The Kubernetes-shaped target checks every quantity it emits with
// [Parse], and the Compose target converts from the same parsed value, so the
// two cannot disagree about what a quantity is. Parsing is kept apart from
// converting: [Parse] says whether a string is a quantity, and whether a target
// can hold the value is a question that target's own core asks of the result
// through [Quantity.ExactInt64].
//
// The accepted strings are exactly these:
//
//	quantity  ::= [ sign ] number [ suffix ]
//	sign      ::= "+" | "-"
//	number    ::= digits | digits "." digits | digits "." | "." digits
//	digits    ::= one or more of 0 1 2 3 4 5 6 7 8 9
//	suffix    ::= binary | decimal | exponent
//	binary    ::= "Ki" | "Mi" | "Gi" | "Ti" | "Pi" | "Ei"
//	decimal   ::= "n" | "u" | "m" | "k" | "M" | "G" | "T" | "P" | "E"
//	exponent  ::= ( "e" | "E" ) [ sign ] digits
//
// A lone "E" after a number is the decimal suffix for 10^18; followed by digits
// it opens an exponent.
//
// The platform's own comment on its quantity type describes a grammar that
// differs from this one in two places, and in both this package follows the
// platform's parser instead: the comment lets an exponent carry a fraction,
// which the parser refuses, and it leaves n and u out of the decimal suffixes,
// which the parser and the platform's own tests accept.
//
// The platform's parser in turn accepts one family of strings this package
// refuses: a string with no digit in its number — "m", "Ki", "+", ".", "e5" —
// which the parser reads as zero. That is exactly what a template leaves behind
// when the value it interpolates is empty, so reading it as zero would turn an
// empty input into a zero-sized request that nothing reports. Surrounding
// whitespace, and an exponent that does not fit in 64 bits, are refused here
// as the platform's parser refuses them.
//
// Every accepted string has an exact value — a whole number of digits scaled by
// a power of ten and, for a binary suffix, a power of two — and [Parse] keeps
// it exactly: nothing here rounds, and nothing here caps a magnitude.
//
// The package imports nothing outside the standard library, because it is
// compiled into the plugins' WebAssembly guests along with the cores that use
// it.
package quantity
