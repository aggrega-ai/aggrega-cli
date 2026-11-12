// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func annotations(pairs ...string) types.OrderedMap[string] {
	m := types.OrderedMap[string]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i], pairs[i+1])
	}
	return m
}

func TestRequiredAttachmentTypes_WellFormed(t *testing.T) {
	t.Parallel()
	got := types.RequiredAttachmentTypes(annotations(
		"component.aggrega.ai/type", "server",
		"features.aggrega.ai/requiredType.monitoring", "core/prometheus-exporter",
		"features.aggrega.ai/requiredType.database", "example/db-binding",
	))
	if len(got) != 2 {
		t.Fatalf("want 2 mandates, got %d: %+v", len(got), got)
	}
	// Author order is preserved so a derived diagnostic set is deterministic.
	if got[0].Label != "monitoring" || got[0].Type != "core/prometheus-exporter" {
		t.Errorf("first mandate wrong: %+v", got[0])
	}
	if got[1].Label != "database" || got[1].Type != "example/db-binding" {
		t.Errorf("second mandate wrong: %+v", got[1])
	}
	for _, m := range got {
		if m.Malformed != "" {
			t.Errorf("%s: unexpected malformed reason %q", m.Key, m.Malformed)
		}
	}
}

func TestRequiredAttachmentTypes_NoneDeclared(t *testing.T) {
	t.Parallel()
	if got := types.RequiredAttachmentTypes(annotations("a", "b")); len(got) != 0 {
		t.Fatalf("want no mandates, got %+v", got)
	}
	if got := types.RequiredAttachmentTypes(types.OrderedMap[string]{}); len(got) != 0 {
		t.Fatalf("want no mandates from an empty map, got %+v", got)
	}
}

// A key that names the mandate family but carries no label must surface as
// malformed rather than being skipped: skipping it turns a declaration the
// author wrote into an absent constraint, which is a broken requirement
// reading as a satisfied one.
func TestRequiredAttachmentTypes_LabellessKeyIsMalformedNotIgnored(t *testing.T) {
	t.Parallel()
	for _, key := range []string{
		"features.aggrega.ai/requiredType",
		"features.aggrega.ai/requiredType.",
	} {
		got := types.RequiredAttachmentTypes(annotations(key, "core/prometheus-exporter"))
		if len(got) != 1 {
			t.Fatalf("%q: want 1 entry, got %d", key, len(got))
		}
		if got[0].Malformed == "" {
			t.Errorf("%q: want a malformed reason, got none", key)
		}
	}
}

func TestRequiredAttachmentTypes_MalformedValue(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"",
		"prometheus-exporter",
		"core/datastore/mariadb",
		"core/prometheus-exporter:1.0.0",
		"component.aggrega.ai/type=server",
	} {
		got := types.RequiredAttachmentTypes(annotations(
			"features.aggrega.ai/requiredType.monitoring", value))
		if len(got) != 1 {
			t.Fatalf("%q: want 1 entry, got %d", value, len(got))
		}
		if got[0].Malformed == "" {
			t.Errorf("value %q: want a malformed reason, got none", value)
		}
		if got[0].Label != "monitoring" {
			t.Errorf("value %q: label lost: %+v", value, got[0])
		}
	}
}
