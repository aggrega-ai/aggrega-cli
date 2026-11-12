// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package publishops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestContentBesideBatchNamesEverySentFieldInOneOrder holds which content fields
// count as sent beside a batch file and the order they are named in, which every
// surface refusing the combination reads.
func TestContentBesideBatchNamesEverySentFieldInOneOrder(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                                   string
		severity, reason, replacement, message string
		want                                   string
	}{
		{"none sent", "", "", "", "", ""},
		{"white space only counts as absent", " ", "\t", "\n", "  ", ""},
		{"one field", "", "", "x:2", "", "replacement"},
		{"every field, in the fixed order", "warning", "r", "x:2", "m", "severity,reason,replacement,message"},
		{"a later field alone", "", "", "", "m", "message"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(ContentBesideBatch(tc.severity, tc.reason, tc.replacement, tc.message), ",")
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDeprecationBatchOfTheWrongShapeIsRefusedInTheAuthorsWords holds the refusal
// of a batch file written in the wrong shape to the line, the field and the two
// shapes, never the decoder's own phrasing or the program's types. One fault per
// row.
func TestDeprecationBatchOfTheWrongShapeIsRefusedInTheAuthorsWords(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"the batch written as a map", "reference: registry.example.invalid/x:1.0.0\nseverity: warning\nreason: probe\n",
			"line 1: a map is written where a list of deprecation batch entries is expected"},
		{"an entry's reference written as a list", "- reference: [registry.example.invalid/x:1.0.0]\n  severity: warning\n  reason: probe\n",
			"line 1 ([0].reference): a list is written where text is expected"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "batch.yaml")
			if err := os.WriteFile(path, []byte(tc.doc), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := loadDeprecationBatch(path)
			if err == nil {
				t.Fatal("a batch of the wrong shape was accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not say %q", err, tc.want)
			}
			for _, leak := range []string{"cannot unmarshal", "publishops.", "!!"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("error %q carries %q, which names nothing the author wrote", err, leak)
				}
			}
		})
	}
}
