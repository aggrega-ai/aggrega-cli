// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package armexpr reads, prints and walks Azure Resource Manager template
// expressions: the bracketed text, such as [concat('a', parameters('p'))], that
// ARM evaluates when a template is deployed.
//
// The grammar is the one the template syntax reference documents: function
// calls with an optional namespace prefix, single-quoted strings in which two
// quotes stand for one, integers, member access (.name), index access ([expr])
// and whitespace between tokens. A string is an expression when it starts with
// "[", does not start with "[[", and ends with "]"; "[[" at the start keeps a
// literal bracket.
//
// The package performs no evaluation and attaches no meaning to a function name
// or an argument. It imports no WebAssembly-only API, so it runs on the host.
package armexpr
