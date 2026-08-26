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
		// The case is kept and its expectation is unchanged, because it is still
		// true of this function; only its former framing was wrong. It no longer
		// names a caller: the boundary-attached feature identity is composed from
		// its authored parts rather than scrubbed out of a marker string, so
		// nothing hands this function a marker any more. What it exercises is the
		// substitution and the edge trim over a string dense in refused
		// characters.
		{"refused_characters_replaced_and_edges_trimmed", "<boundary-fr>:bridge@cluster", "boundary-fr-bridge-cluster"},
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

// TestSanitizeForK8sLabelValue_IsManyToOne pins the weakness the godoc states,
// so the statement is asserted rather than merely written down. Three inputs an
// author can tell apart arrive at one value, which is why a caller needing
// distinct values composes them from parts instead of passing a joined string
// through here.
func TestSanitizeForK8sLabelValue_IsManyToOne(t *testing.T) {
	t.Parallel()
	const want = "a-b"
	for _, in := range []string{"a-b", "a:b", "a/b", "a b", "a@b", "a--b", "a:::b"} {
		if got := SanitizeForK8sLabelValue(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q — the many-to-one statement in the godoc is wrong", in, got, want)
		}
	}
}
