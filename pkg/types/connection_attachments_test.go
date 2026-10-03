// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// enumerated is one attachment an enumeration handed out, flattened for
// comparison: the connection it was written on and the type it names.
type enumerated struct {
	Connection AttachmentConnection
	Type       string
}

// TestComponentConnectionAttachmentsEnumerateEverySlotInAuthorOrder holds the
// Aggregate-side enumeration to the set and the order the lowering and the
// lock-time walk both read: every attachment every connection carries, socket
// then alias then slot as written, and none for a compact-form socket.
func TestComponentConnectionAttachmentsEnumerateEverySlotInAuthorOrder(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		uses string
		want []enumerated
	}{
		{
			name: "two sockets, connections and slots in the order written",
			uses: `
models:
  gpt:
    target: gpt
    features:
      credential: { type: sec/token }
      audit: { type: obs/audit }
  nim:
    features:
      credential: { type: sec/token }
backups:
  spare:
    target: store
    features:
      credential: { type: sec/badge }
`,
			want: []enumerated{
				{AttachmentConnection{Socket: "models", Alias: "gpt", Attachment: "credential"}, "sec/token"},
				{AttachmentConnection{Socket: "models", Alias: "gpt", Attachment: "audit"}, "obs/audit"},
				{AttachmentConnection{Socket: "models", Alias: "nim", Attachment: "credential"}, "sec/token"},
				{AttachmentConnection{Socket: "backups", Alias: "spare", Attachment: "credential"}, "sec/badge"},
			},
		},
		{
			name: "a connection carrying none beside one carrying one",
			uses: `
models:
  plain: { target: plain }
  gpt:
    features:
      credential: { type: sec/token }
`,
			want: []enumerated{
				{AttachmentConnection{Socket: "models", Alias: "gpt", Attachment: "credential"}, "sec/token"},
			},
		},
		{name: "a compact-form socket carries none", uses: "models: [gpt, nim]\n"},
		{name: "no uses block carries none", uses: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var ci ComponentInstance
			if err := yaml.Unmarshal([]byte(withUses("type: app/gw\n", tc.uses)), &ci); err != nil {
				t.Fatalf("unmarshal component instance: %v", err)
			}
			var got []enumerated
			for conn, att := range ci.ConnectionAttachments() {
				got = append(got, enumerated{conn, att.Type})
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ConnectionAttachments() =\n  %v\nwant\n  %v", got, tc.want)
			}
		})
	}
}

// TestReleaseConnectionAttachmentsEnumerateEverySlotInAuthorOrder holds the
// Release-side enumeration to the same rule, an entry naming no type included:
// it is enumerated, and what an empty type means is the consumer's question.
func TestReleaseConnectionAttachmentsEnumerateEverySlotInAuthorOrder(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		uses string
		want []enumerated
	}{
		{
			name: "a typed attachment and a sizing entry, in the order written",
			uses: `
models:
  plain:
    features:
      credential: { type: sec/badge }
  gpt:
    infra: { region: eu }
    features:
      credential: { infra: { rotation: hourly } }
`,
			want: []enumerated{
				{AttachmentConnection{Socket: "models", Alias: "plain", Attachment: "credential"}, "sec/badge"},
				{AttachmentConnection{Socket: "models", Alias: "gpt", Attachment: "credential"}, ""},
			},
		},
		{name: "a connection entry carrying only infra carries none", uses: "models:\n  gpt: { infra: { region: eu } }\n"},
		{name: "no uses block carries none", uses: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var bc BoundaryComponent
			if err := yaml.Unmarshal([]byte(withUses("infra: { replicas: 1 }\n", tc.uses)), &bc); err != nil {
				t.Fatalf("unmarshal boundary component: %v", err)
			}
			var got []enumerated
			for conn, att := range bc.ConnectionAttachments() {
				got = append(got, enumerated{conn, att.Type})
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ConnectionAttachments() =\n  %v\nwant\n  %v", got, tc.want)
			}
		})
	}
}

// TestConnectionAttachmentsStopWhenTheConsumerDoes holds both enumerations to
// the iterator contract: a range that breaks after the first attachment receives
// exactly one, rather than the enumeration calling on past a finished loop.
func TestConnectionAttachmentsStopWhenTheConsumerDoes(t *testing.T) {
	t.Parallel()
	const uses = "uses:\n  models:\n    a: { features: { x: { type: f/x }, y: { type: f/y } } }\n    b: { features: { z: { type: f/z } } }\n"
	var ci ComponentInstance
	if err := yaml.Unmarshal([]byte(uses), &ci); err != nil {
		t.Fatalf("unmarshal component instance: %v", err)
	}
	var bc BoundaryComponent
	if err := yaml.Unmarshal([]byte(uses), &bc); err != nil {
		t.Fatalf("unmarshal boundary component: %v", err)
	}
	seen := 0
	for range ci.ConnectionAttachments() {
		seen++
		break
	}
	for range bc.ConnectionAttachments() {
		seen++
		break
	}
	if seen != 2 {
		t.Errorf("each enumeration must stop at the first break; saw %d attachments over the two", seen)
	}
}

// withUses returns head followed by a `uses:` block holding block (written at
// column zero), or head alone when block is empty — the shape with no `uses:` key
// at all.
func withUses(head, block string) string {
	if strings.TrimSpace(block) == "" {
		return head
	}
	var b strings.Builder
	b.WriteString(head + "uses:\n")
	for _, line := range strings.Split(strings.TrimLeft(block, "\n"), "\n") {
		if line != "" {
			b.WriteString("  " + line + "\n")
		}
	}
	return b.String()
}
