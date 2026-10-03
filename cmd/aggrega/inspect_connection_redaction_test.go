// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestInspectConnectionAttachment_WithholdsLikeAnOwnAttachment holds the Release
// layout's two attachment views to one withholding: an attachment written on a
// connection, printed beneath that connection's line, withholds exactly the leaves
// the same Feature withholds when attached to the node itself. Each row is one
// layout; both read the same entry and the same declarations.
func TestInspectConnectionAttachment_WithholdsLikeAnOwnAttachment(t *testing.T) {
	t.Parallel()
	entry := func(connection *types.AttachmentConnection) types.IRFeatureEntry {
		values := types.OrderedMap[any]{}
		values.Set("secretName", "conn-locator-public")
		values.Set("literalPassword", "conn-password-plaintext")
		// Heterogeneous provenance forces the per-leaf layout, the only layout that
		// prints leaf values at all.
		prov := types.OrderedMap[types.FieldProvenance]{}
		prov.Set("secretName", explicitProvenance())
		prov.Set("literalPassword", annotationProvenance("feature.test/pw", "aggregate.yaml:x", "aggregate"))
		return types.IRFeatureEntry{
			Type:             "feature.test/db-credential",
			Sensitive:        true,
			Values:           values,
			ValuesProvenance: prov,
			Connection:       connection,
		}
	}
	declared := resolver.SensitiveLeaves{Values: map[string]bool{"literalPassword": true}}

	cases := []struct {
		name   string
		render func(*bytes.Buffer)
	}{
		{"an attachment the node holds itself", func(buf *bytes.Buffer) {
			renderFeatureAttachment(buf, "credentials", entry(nil), declared)
		}},
		{"an attachment written on a connection", func(buf *bytes.Buffer) {
			renderConnectionAttachment(buf, "gpt.credential",
				entry(&types.AttachmentConnection{Socket: "models", Alias: "gpt", Attachment: "credential"}),
				"conn-token-compose:1.0.0", declared)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			c.render(&buf)
			got := buf.String()
			if !strings.Contains(got, "values.literalPassword: "+redact.Placeholder()) {
				t.Errorf("the Feature's declared leaf must be withheld, keeping its key\n--- output ---\n%s", got)
			}
			if strings.Contains(got, "conn-password-plaintext") {
				t.Errorf("the layout leaked the declared value\n--- output ---\n%s", got)
			}
			if !strings.Contains(got, "values.secretName: conn-locator-public") {
				t.Errorf("an undeclared leaf must print verbatim\n--- output ---\n%s", got)
			}
		})
	}
}
