// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package stringformat

import (
	"fmt"
	"strings"
)

// checkURI is the uri format. The JSON Schema specification defines it as a URI
// according to RFC 3986 — the URI production of section 3, which requires a
// scheme; a relative reference is the separate uri-reference format and is not
// accepted here:
//
//	URI           = scheme ":" hier-part [ "?" query ] [ "#" fragment ]
//	hier-part     = "//" authority path-abempty / path-absolute
//	              / path-rootless / path-empty
//	scheme        = ALPHA *( ALPHA / DIGIT / "+" / "-" / "." )
//	authority     = [ userinfo "@" ] host [ ":" port ]
//	userinfo      = *( unreserved / pct-encoded / sub-delims / ":" )
//	host          = IP-literal / IPv4address / reg-name
//	IP-literal    = "[" ( IPv6address / IPvFuture ) "]"
//	IPvFuture     = "v" 1*HEXDIG "." 1*( unreserved / sub-delims / ":" )
//	reg-name      = *( unreserved / pct-encoded / sub-delims )
//	port          = *DIGIT
//	pchar         = unreserved / pct-encoded / sub-delims / ":" / "@"
//	query         = *( pchar / "/" / "?" )
//	fragment      = *( pchar / "/" / "?" )
//	pct-encoded   = "%" HEXDIG HEXDIG
//	unreserved    = ALPHA / DIGIT / "-" / "." / "_" / "~"
//	sub-delims    = "!" / "$" / "&" / "'" / "(" / ")" / "*" / "+" / "," / ";" / "="
//
// An IPv4address is also a reg-name, so a host that is not bracketed is checked
// as a reg-name. The IPv6address inside brackets is judged by the ipv6 format's
// rules, which are RFC 4291's text forms that RFC 3986 reproduces. The grammar is
// ASCII-only, so a character outside it must be percent-encoded; an unencoded
// space or non-ASCII character is refused. Nothing about the URI is dereferenced:
// its scheme need not be registered, and its host is never resolved.
func checkURI(s string) string {
	colon := strings.IndexByte(s, ':')
	if colon <= 0 {
		return "a URI begins with a scheme followed by a colon, such as https:"
	}
	scheme := s[:colon]
	if !isAlpha(scheme[0]) {
		return fmt.Sprintf("scheme %q does not begin with a letter", scheme)
	}
	for i := 1; i < len(scheme); i++ {
		c := scheme[i]
		if !isLetDig(c) && c != '+' && c != '-' && c != '.' {
			return fmt.Sprintf("scheme %q carries %q; a scheme is letters, digits, +, - and .", scheme, rune(c))
		}
	}
	rest := s[colon+1:]

	var fragment, query string
	hasFragment, hasQuery := false, false
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		fragment, rest, hasFragment = rest[i+1:], rest[:i], true
	}
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		query, rest, hasQuery = rest[i+1:], rest[:i], true
	}

	if strings.HasPrefix(rest, "//") {
		authority, path := rest[2:], ""
		if i := strings.IndexByte(authority, '/'); i >= 0 {
			authority, path = authority[:i], authority[i:]
		}
		if reason := uriAuthority(authority); reason != "" {
			return reason
		}
		if reason := uriPath(path, "the path"); reason != "" {
			return reason
		}
	} else if reason := uriPath(rest, "the path"); reason != "" {
		return reason
	}
	if hasQuery {
		if reason := uriChars(query, "/?", "the query"); reason != "" {
			return reason
		}
	}
	if hasFragment {
		if reason := uriChars(fragment, "/?", "the fragment"); reason != "" {
			return reason
		}
	}
	return ""
}

// uriAuthority checks RFC 3986's authority component.
func uriAuthority(authority string) string {
	hostport := authority
	if i := strings.LastIndexByte(authority, '@'); i >= 0 {
		if reason := uriCharsOf(authority[:i], ":", "the user information"); reason != "" {
			return reason
		}
		hostport = authority[i+1:]
	}
	host, port := hostport, ""
	hasPort := false
	if strings.HasPrefix(hostport, "[") {
		end := strings.IndexByte(hostport, ']')
		if end < 0 {
			return "a bracketed host is closed by ]"
		}
		host = hostport[:end+1]
		after := hostport[end+1:]
		if after != "" {
			if after[0] != ':' {
				return "a bracketed host is followed only by a port"
			}
			port, hasPort = after[1:], true
		}
		if reason := uriIPLiteral(host[1 : len(host)-1]); reason != "" {
			return reason
		}
	} else {
		if i := strings.IndexByte(hostport, ':'); i >= 0 {
			host, port, hasPort = hostport[:i], hostport[i+1:], true
		}
		if reason := uriCharsOf(host, "", "the host"); reason != "" {
			return reason
		}
	}
	if hasPort {
		for i := 0; i < len(port); i++ {
			if !isDigit(port[i]) {
				return fmt.Sprintf("port %q is not made of digits", port)
			}
		}
	}
	return ""
}

// uriIPLiteral checks the inside of a bracketed host: an IPv6 address, or an
// IPvFuture literal.
func uriIPLiteral(inner string) string {
	if len(inner) > 0 && (inner[0] == 'v' || inner[0] == 'V') {
		version, rest, ok := strings.Cut(inner[1:], ".")
		if !ok || version == "" || rest == "" {
			return "a future-version address literal is v, a hexadecimal version, a dot, then its address"
		}
		for i := 0; i < len(version); i++ {
			if !isHexDigit(version[i]) {
				return "a future-version address literal is v, a hexadecimal version, a dot, then its address"
			}
		}
		for _, r := range rest {
			if r > 127 || !(isUnreserved(byte(r)) || isSubDelim(byte(r)) || r == ':') {
				return fmt.Sprintf("a future-version address literal cannot carry %q", r)
			}
		}
		return ""
	}
	if reason := checkIPv6(inner); reason != "" {
		return "the bracketed host is not a valid IPv6 address: " + reason
	}
	return ""
}

// uriPath checks a path component: segments of pchar separated by slashes. Where
// an authority is present the caller has already split the path at its first
// slash, so the path is empty or begins with one, as path-abempty requires; where
// none is present a path beginning "//" would have been read as an authority, so
// path-absolute's one restriction holds too.
func uriPath(path, what string) string {
	return uriChars(path, "/", what)
}

// uriChars checks that s is made of pchar plus the extra characters given.
func uriChars(s, extra, what string) string {
	return uriCharsOf(s, ":@"+extra, what)
}

// uriCharsOf checks that s is made of unreserved characters, sub-delims,
// percent-encoded octets and the extra characters given, every percent sign
// opening a two-digit hexadecimal escape. pchar is the set with ":" and "@" among
// the extras.
func uriCharsOf(s, extra, what string) string {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '%':
			if i+2 >= len(s) || !isHexDigit(s[i+1]) || !isHexDigit(s[i+2]) {
				return fmt.Sprintf("%s carries a %% that does not open a two-digit hexadecimal escape", what)
			}
			i += 2
		case c > 127:
			return fmt.Sprintf("%s carries a character outside ASCII, which a URI percent-encodes", what)
		case isUnreserved(c) || isSubDelim(c) || strings.IndexByte(extra, c) >= 0:
		default:
			return fmt.Sprintf("%s cannot carry %q unencoded", what, rune(c))
		}
	}
	return ""
}

func isUnreserved(c byte) bool {
	return isLetDig(c) || c == '-' || c == '.' || c == '_' || c == '~'
}

func isSubDelim(c byte) bool {
	return strings.IndexByte("!$&'()*+,;=", c) >= 0
}
