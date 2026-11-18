// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validateops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
)

// mandatingComponent is the probe Component declaring the given annotations,
// each written as one `key: value` line under metadata.annotations.
func mandatingComponent(annotations ...string) string {
	block := ""
	if len(annotations) > 0 {
		block = "  annotations:\n"
		for _, line := range annotations {
			block += "    " + line + "\n"
		}
	}
	return strings.Replace(probeComponent, "spec:\n", block+"spec:\n", 1)
}

// writeMandatingCatalog writes a probe Component declaring the given annotations
// and a probe Recipe that renders, and returns the catalog directory and the
// Recipe's path.
func writeMandatingCatalog(t *testing.T, annotations ...string) (string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "catalog")
	files := map[string]string{
		"component/example/compute/probe/1.0.0/component.yaml": mandatingComponent(annotations...),
		"recipe/helm/probe/1.0.0/recipe.yaml":                  probeRecipe("{{ .infra.image }}:{{ .infra.tag }}", ""),
	}
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return dir, filepath.Join(dir, "recipe/helm/probe/1.0.0/recipe.yaml")
}

// TestValidateRecipeDeclinesAPresenceMandateAndStillRefusesAMalformedOne drives a
// Recipe whose Component declares presence mandates through the one single-entity
// check, end to end through the embedded provider.
//
// A build of one attaches no Feature, so a mandate it evaluated would fail every
// Recipe whose Component declares one, whatever the Recipe says. The check
// declines each evaluable mandate, names it with its label and the type it
// demands on the account of what it did not check, and goes on to resolve and
// render. Without the decline the first row halts at resolution on the missing
// required feature, and its error is that condition.
//
// A mandate whose declaration cannot be evaluated is a fault of the Component's
// own, which no attachment repairs, so it still fails, matched by the condition
// a malformed declaration raises — beside an evaluable mandate as well, which is
// declined there exactly as it is alone.
func TestValidateRecipeDeclinesAPresenceMandateAndStillRefusesAMalformedOne(t *testing.T) {
	embeddedentities.Require(t)
	t.Parallel()
	monitoring := types.RequiredAttachmentTypeAnnotationPrefix + "monitoring: core/prometheus-exporter"
	audit := types.RequiredAttachmentTypeAnnotationPrefix + "audit: core/audit-logging"
	broken := types.RequiredAttachmentTypeAnnotationPrefix + "broken: Not A Feature Type"
	cases := []struct {
		name        string
		annotations []string
		// wantFault is the condition the check must fail with, or nil where it
		// must pass and render.
		wantFault error
		// wantDeclined is the account line naming the mandates declined, or empty
		// where none is.
		wantDeclined string
	}{
		{
			name:         "a presence mandate is declined and named, and the Recipe renders",
			annotations:  []string{monitoring},
			wantDeclined: "Attachments: presence mandates not checked (monitoring: core/prometheus-exporter)",
		},
		{
			name:         "every mandate is named, in the order the Component declares them",
			annotations:  []string{audit, "example.com/owner: payments", monitoring},
			wantDeclined: "Attachments: presence mandates not checked (audit: core/audit-logging, monitoring: core/prometheus-exporter)",
		},
		{
			name:        "a malformed mandate still fails",
			annotations: []string{broken},
			wantFault:   analyzer.ErrMalformedAttachmentRequirement(),
		},
		{
			name:         "a malformed mandate fails beside an evaluable one, which is declined",
			annotations:  []string{monitoring, broken},
			wantFault:    analyzer.ErrMalformedAttachmentRequirement(),
			wantDeclined: "Attachments: presence mandates not checked (monitoring: core/prometheus-exporter)",
		},
		{
			name: "a Component declaring no mandate is given no account",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir, recipe := writeMandatingCatalog(t, tc.annotations...)
			root := filepath.Dir(dir)
			res, err := ValidateEntity(context.Background(), root, filepath.Join(root, "cache"),
				[]string{dir}, nil, recipe, false)
			report := strings.Join(res.Diagnostics, "\n")

			if tc.wantFault == nil {
				if err != nil {
					t.Fatalf("ValidateEntity: %v\nreport:\n%s", err, report)
				}
				if !res.Valid || len(res.Artifacts) == 0 {
					t.Errorf("Valid = %v with %d artifact(s); want a rendered Recipe\nreport:\n%s", res.Valid, len(res.Artifacts), report)
				}
			} else {
				if !errors.Is(err, tc.wantFault) {
					t.Fatalf("ValidateEntity returned %v; want the condition %q\nreport:\n%s", err, tc.wantFault, report)
				}
				if res.Valid {
					t.Errorf("Valid = true beside the failure %v", err)
				}
			}

			var accounts []string
			for _, line := range res.Diagnostics {
				if strings.HasPrefix(line, "Attachments: presence mandates not checked") {
					accounts = append(accounts, line)
				}
			}
			switch {
			case tc.wantDeclined == "" && len(accounts) != 0:
				t.Errorf("no mandate is declined, yet the report carries %q", accounts)
			case tc.wantDeclined != "" && (len(accounts) != 1 || accounts[0] != tc.wantDeclined):
				t.Errorf("the account of the declined mandates is %q, want exactly %q\nreport:\n%s", accounts, tc.wantDeclined, report)
			}
		})
	}
}
