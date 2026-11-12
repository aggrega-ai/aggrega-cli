// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package stringformat

import (
	"fmt"
	"strings"
)

// maxHostnameLength is the longest host name, in characters, that fits the
// 255-octet limit a name has in the domain system once its labels are length-
// prefixed and terminated.
const maxHostnameLength = 253

// maxLabelLength is the longest label RFC 1123 section 2.1 requires a host to
// handle, and the most a label can be in the domain system.
const maxLabelLength = 63

// checkHostname is the hostname format. The JSON Schema specification defines it
// as a host name per RFC 1123, section 2.1, including host names produced using
// the Punycode algorithm of RFC 5891, section 4.4. That section keeps the label
// syntax of RFC 952 — letters, digits and hyphens, beginning and ending with a
// letter or digit — relaxing only its first character to allow a digit, and sets
// labels of up to 63 characters and names of up to 255 octets.
//
// So a host name here is one or more labels separated by dots, each of one to 63
// letters, digits and hyphens that neither begins nor ends with a hyphen, and the
// whole at most 253 characters. A Punycode-encoded label ("xn--…") is such a label
// and is accepted as one; whether its encoded part decodes is not judged. A name
// ending in a dot is the domain system's absolute form rather than a host name,
// and is refused, as are characters outside that alphabet — an underscore, a
// space, anything non-ASCII.
func checkHostname(s string) string {
	if s == "" {
		return "a host name is not empty"
	}
	if len(s) > maxHostnameLength {
		return fmt.Sprintf("a host name is at most %d characters; this one is %d", maxHostnameLength, len(s))
	}
	for _, label := range strings.Split(s, ".") {
		if reason := hostnameLabel(label); reason != "" {
			return reason
		}
	}
	return ""
}

// hostnameLabel checks one dot-separated label of a host name.
func hostnameLabel(label string) string {
	if label == "" {
		return "a host name has no empty label: it does not begin or end with a dot, or carry two in a row"
	}
	if len(label) > maxLabelLength {
		return fmt.Sprintf("label %q is longer than %d characters", label, maxLabelLength)
	}
	for _, r := range label {
		if r > 127 || (!isLetDig(byte(r)) && r != '-') {
			return fmt.Sprintf("label %q carries %q; a label is ASCII letters, digits and hyphens", label, r)
		}
	}
	if label[0] == '-' || label[len(label)-1] == '-' {
		return fmt.Sprintf("label %q begins or ends with a hyphen", label)
	}
	return ""
}
