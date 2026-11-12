// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package stringformat

// checkUUID is the uuid format. The JSON Schema specification defines it as the
// string representation of a UUID in RFC 4122, section 3:
//
//	UUID = time-low "-" time-mid "-" time-high-and-version "-"
//	       clock-seq-and-reserved clock-seq-low "-" node
//
// which is 32 hexadecimal digits in groups of 8, 4, 4, 4 and 12 separated by
// hyphens. RFC 4122 makes the digits case-insensitive on input, so upper and lower
// case are both accepted. The grammar does not constrain the version or variant
// digits, so neither is judged: the nil UUID and a version the RFC does not define
// are both valid representations.
func checkUUID(s string) string {
	const shape = "a UUID is 32 hexadecimal digits in groups of 8-4-4-4-12 separated by hyphens"
	if len(s) != 36 {
		return shape
	}
	for i := 0; i < len(s); i++ {
		switch i {
		case 8, 13, 18, 23:
			if s[i] != '-' {
				return shape
			}
		default:
			if !isHexDigit(s[i]) {
				return shape
			}
		}
	}
	return ""
}
