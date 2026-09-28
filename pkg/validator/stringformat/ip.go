// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package stringformat

import (
	"fmt"
	"strings"
)

// checkIPv4 is the ipv4 format. The JSON Schema specification defines it as the
// dotted-quad production of RFC 2673, section 3.2:
//
//	dotted-quad = decbyte "." decbyte "." decbyte "." decbyte
//	decbyte     = 1*3DIGIT   ; a decimal value from 0 to 255
//
// The production admits one to three digits per part, so a part written with a
// leading zero ("010") is accepted as the decimal value it spells; nothing else
// about the address — its class, whether it is routable — is judged.
func checkIPv4(s string) string {
	return dottedQuad(s)
}

// dottedQuad checks RFC 2673's dotted-quad production, reporting what is wrong.
// It is shared with the IPv6 form that ends in an IPv4 address, which RFC 4291
// writes in the same notation.
func dottedQuad(s string) string {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return "an IPv4 address is four decimal numbers separated by dots"
	}
	for _, p := range parts {
		if len(p) < 1 || len(p) > 3 || !allDigits(p) {
			return fmt.Sprintf("%q is not a decimal number of one to three digits", p)
		}
		if atoiDigits(p) > 255 {
			return fmt.Sprintf("%s is greater than 255", p)
		}
	}
	return ""
}

// checkIPv6 is the ipv6 format. The JSON Schema specification defines it as an
// IPv6 address in the text representation of RFC 4291, section 2.2, which admits
// three forms:
//
//   - eight groups of one to four hexadecimal digits separated by colons;
//   - the same with one run of one or more all-zero groups written "::", which may
//     appear once;
//   - either of the above with the last two groups written as an IPv4 address in
//     dotted-quad notation.
//
// A zone suffix ("%eth0") and a prefix length ("/64") are not part of an address
// in that section — the one names an interface, the other a set of addresses — so
// both are refused.
func checkIPv6(s string) string {
	if s == "" {
		return "an IPv6 address is not empty"
	}
	if strings.ContainsAny(s, "%/") {
		return "an IPv6 address carries no zone (%) and no prefix length (/)"
	}
	groupsWanted := 8
	body := s
	if strings.Contains(s, ".") {
		i := strings.LastIndex(s, ":")
		if i < 0 {
			return "an IPv6 address is groups of hexadecimal digits separated by colons"
		}
		if reason := dottedQuad(s[i+1:]); reason != "" {
			return "the IPv4 address ending it is not valid: " + reason
		}
		groupsWanted = 6
		if strings.HasSuffix(s[:i+1], "::") {
			body = s[:i+1]
		} else {
			body = s[:i]
		}
	}
	if strings.Contains(body, ":::") {
		return "an IPv6 address has at most two colons in a row"
	}
	if n := strings.Count(body, "::"); n > 1 {
		return `"::" may appear only once in an IPv6 address`
	}
	if left, right, compressed := strings.Cut(body, "::"); compressed {
		count := 0
		for _, half := range []string{left, right} {
			if half == "" {
				continue
			}
			groups := strings.Split(half, ":")
			for _, g := range groups {
				if reason := hexGroup(g); reason != "" {
					return reason
				}
			}
			count += len(groups)
		}
		if count > groupsWanted-1 {
			return fmt.Sprintf(`an IPv6 address using "::" writes at most %d other groups`, groupsWanted-1)
		}
		return ""
	}
	groups := strings.Split(body, ":")
	if len(groups) != groupsWanted {
		return fmt.Sprintf("an IPv6 address without \"::\" writes exactly %d groups; this one writes %d", groupsWanted, len(groups))
	}
	for _, g := range groups {
		if reason := hexGroup(g); reason != "" {
			return reason
		}
	}
	return ""
}

// hexGroup checks one colon-separated group of an IPv6 address.
func hexGroup(g string) string {
	if len(g) < 1 || len(g) > 4 {
		return fmt.Sprintf("group %q is not one to four hexadecimal digits", g)
	}
	for i := 0; i < len(g); i++ {
		if !isHexDigit(g[i]) {
			return fmt.Sprintf("group %q is not one to four hexadecimal digits", g)
		}
	}
	return ""
}
