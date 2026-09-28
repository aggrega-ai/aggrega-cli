// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package stringformat

import (
	"fmt"
	"strings"
)

// checkEmail is the email format. The JSON Schema specification defines it as the
// Mailbox production of RFC 5321, section 4.1.2:
//
//	Mailbox        = Local-part "@" ( Domain / address-literal )
//	Local-part     = Dot-string / Quoted-string
//	Dot-string     = Atom *("." Atom)
//	Atom           = 1*atext                        ; atext per RFC 5322
//	Quoted-string  = DQUOTE *QcontentSMTP DQUOTE
//	QcontentSMTP   = qtextSMTP / quoted-pairSMTP
//	qtextSMTP      = %d32-33 / %d35-91 / %d93-126
//	quoted-pairSMTP = %d92 %d32-126
//	Domain         = sub-domain *("." sub-domain)
//	sub-domain     = Let-dig [Ldh-str]
//	address-literal = "[" ( IPv4-address-literal / IPv6-address-literal /
//	                        General-address-literal ) "]"
//
// Only that production is checked. It carries no length limit, so the size
// limits RFC 5321 sets elsewhere (section 4.5.3.1) are not applied; a Domain needs
// no dot, so a single-label domain is valid; and it is ASCII-only, so a non-ASCII
// address is refused. Section 4.1.3's IPv6 address literal is judged by the ipv6
// format's own rules. A General-address-literal's tag is checked for its syntax
// only — whether it is registered is not a syntactic question — except that the tag
// "IPv6" introduces the IPv6 literal, whose content is checked as an address.
func checkEmail(s string) string {
	rest, reason := mailboxLocalPart(s)
	if reason != "" {
		return reason
	}
	if !strings.HasPrefix(rest, "@") {
		return "a mailbox is a local part, then @, then a domain"
	}
	domain := rest[1:]
	if domain == "" {
		return "a mailbox names a domain after the @"
	}
	if strings.HasPrefix(domain, "[") {
		return addressLiteral(domain)
	}
	return mailDomain(domain)
}

// mailboxLocalPart consumes the Local-part at the start of s, returning the text
// after it, and what is wrong when s does not begin with a Local-part.
func mailboxLocalPart(s string) (rest, reason string) {
	if s == "" {
		return "", "a mailbox is not empty"
	}
	if s[0] == '"' {
		for i := 1; i < len(s); i++ {
			switch c := s[i]; {
			case c == '"':
				return s[i+1:], ""
			case c == '\\':
				if i+1 >= len(s) || s[i+1] < 32 || s[i+1] > 126 {
					return "", "a backslash in a quoted local part escapes one printable ASCII character"
				}
				i++
			case c == 32 || c == 33 || (c >= 35 && c <= 91) || (c >= 93 && c <= 126):
			default:
				return "", fmt.Sprintf("a quoted local part cannot carry byte %#02x unescaped", c)
			}
		}
		return "", "a quoted local part is closed by a double quote"
	}
	end := strings.IndexByte(s, '@')
	if end < 0 {
		return "", "a mailbox is a local part, then @, then a domain"
	}
	for _, atom := range strings.Split(s[:end], ".") {
		if atom == "" {
			return "", "the local part has no empty atom: it does not begin or end with a dot, or carry two in a row"
		}
		for _, r := range atom {
			if !isAtext(r) {
				return "", fmt.Sprintf("the local part cannot carry %q outside quotes", r)
			}
		}
	}
	return s[end:], ""
}

// isAtext is RFC 5322's atext: the characters an unquoted local part is made of.
func isAtext(r rune) bool {
	if r > 127 {
		return false
	}
	b := byte(r)
	return isLetDig(b) || strings.IndexByte("!#$%&'*+-/=?^_`{|}~", b) >= 0
}

// mailDomain checks RFC 5321's Domain: dot-separated sub-domains, each beginning
// and ending with a letter or digit, with hyphens allowed between.
func mailDomain(domain string) string {
	for _, label := range strings.Split(domain, ".") {
		if label == "" {
			return "the domain has no empty label: it does not begin or end with a dot, or carry two in a row"
		}
		for _, r := range label {
			if r > 127 || (!isLetDig(byte(r)) && r != '-') {
				return fmt.Sprintf("domain label %q carries %q; a label is ASCII letters, digits and hyphens", label, r)
			}
		}
		if !isLetDig(label[0]) || !isLetDig(label[len(label)-1]) {
			return fmt.Sprintf("domain label %q begins or ends with a hyphen", label)
		}
	}
	return ""
}

// addressLiteral checks RFC 5321's address-literal: an IPv4 address, an IPv6
// address introduced by "IPv6:", or a tagged literal, in square brackets.
func addressLiteral(lit string) string {
	if len(lit) < 2 || lit[len(lit)-1] != ']' {
		return "an address literal is closed by ]"
	}
	inner := lit[1 : len(lit)-1]
	tag, content, tagged := strings.Cut(inner, ":")
	if !tagged {
		// IPv4-address-literal = Snum 3("." Snum), Snum being one to three digits
		// valued 0 to 255 — the same rule as the ipv4 format's dotted-quad.
		if reason := dottedQuad(inner); reason != "" {
			return "the IPv4 address literal is not valid: " + reason
		}
		return ""
	}
	if strings.EqualFold(tag, "IPv6") {
		if reason := checkIPv6(content); reason != "" {
			return "the IPv6 address literal is not valid: " + reason
		}
		return ""
	}
	if tag == "" || !isLetDig(tag[len(tag)-1]) {
		return fmt.Sprintf("address literal tag %q is not letters, digits and hyphens ending in a letter or digit", tag)
	}
	for i := 0; i < len(tag); i++ {
		if !isLetDig(tag[i]) && tag[i] != '-' {
			return fmt.Sprintf("address literal tag %q is not letters, digits and hyphens ending in a letter or digit", tag)
		}
	}
	if content == "" {
		return "a tagged address literal carries content after the colon"
	}
	for _, r := range content {
		if r < 33 || r > 126 || r == '[' || r == '\\' || r == ']' {
			return fmt.Sprintf("an address literal cannot carry %q", r)
		}
	}
	return ""
}
