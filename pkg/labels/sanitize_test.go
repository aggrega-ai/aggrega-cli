// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package labels

import "testing"

func TestSanitizeForK8sLabelValue(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"alphanumeric_unchanged", "api", "api"},
		{"with_dash_unchanged", "my-service", "my-service"},
		{"with_dot_unchanged", "v1.0.0", "v1.0.0"},
		{"with_underscore_unchanged", "DB_HOST", "DB_HOST"},
		{"fr_marker_syntax", "<boundary-fr>:bridge@cluster", "boundary-fr-bridge-cluster"},
		{"colon_replaced", "foo:bar", "foo-bar"},
		{"slash_replaced", "foo/bar", "foo-bar"},
		{"multiple_invalid_collapsed", "foo:::bar", "foo-bar"},
		{"leading_invalid_trimmed", "<api>", "api"},
		{"trailing_invalid_trimmed", "api@", "api"},
		{"only_invalid_chars", "!@#$%", ""},
		{"path_like", "boundaries/cluster/services-net", "boundaries-cluster-services-net"},
		{"max_63", "abcdefghij" + "abcdefghij" + "abcdefghij" + "abcdefghij" + "abcdefghij" + "abcdefghij" + "abc", "abcdefghij" + "abcdefghij" + "abcdefghij" + "abcdefghij" + "abcdefghij" + "abcdefghij" + "abc"},
		{"over_63_truncated", "abcdefghij" + "abcdefghij" + "abcdefghij" + "abcdefghij" + "abcdefghij" + "abcdefghij" + "abcdefghij", "abcdefghij" + "abcdefghij" + "abcdefghij" + "abcdefghij" + "abcdefghij" + "abcdefghij" + "abc"},
		{"truncated_then_trimmed", "aaaaaaaaaa" + "aaaaaaaaaa" + "aaaaaaaaaa" + "aaaaaaaaaa" + "aaaaaaaaaa" + "aaaaaaaaaa" + "aa-bbbbbbb", "aaaaaaaaaa" + "aaaaaaaaaa" + "aaaaaaaaaa" + "aaaaaaaaaa" + "aaaaaaaaaa" + "aaaaaaaaaa" + "aa"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := SanitizeForK8sLabelValue(tc.in)
			if got != tc.want {
				t.Errorf("Sanitize(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if len(got) > 63 {
				t.Errorf("Sanitize(%q) length %d exceeds 63", tc.in, len(got))
			}
		})
	}
}
