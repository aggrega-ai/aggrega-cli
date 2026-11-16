// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"strings"
	"testing"
)

func deployed() map[string]any {
	return map[string]any{"Db": map[string]any{"type": "T"}}
}

func TestStampProvenance(t *testing.T) {
	t.Parallel()
	set := map[string]string{"aggrega.ai/component-id": "payments-db", "aggrega.ai/release": "orders"}

	t.Run("stamps under the key and keeps authored metadata", func(t *testing.T) {
		t.Parallel()
		f := map[string]any{"resources": deployed(), "metadata": map[string]any{"author": "x"}}
		if adv := StampProvenance(f, set); adv != "" {
			t.Fatalf("advisory = %q", adv)
		}
		md := f["metadata"].(map[string]any)
		if md["author"] != "x" {
			t.Errorf("authored metadata lost: %v", md)
		}
		block := md[ProvenanceMetadataKey].(map[string]any)
		if block["aggrega.ai/component-id"] != "payments-db" || len(block) != 2 {
			t.Errorf("block = %v", block)
		}
	})
	t.Run("creates metadata when absent", func(t *testing.T) {
		t.Parallel()
		f := map[string]any{"resources": deployed()}
		if adv := StampProvenance(f, set); adv != "" {
			t.Fatalf("advisory = %q", adv)
		}
		if _, ok := f["metadata"].(map[string]any)[ProvenanceMetadataKey]; !ok {
			t.Errorf("metadata = %v", f["metadata"])
		}
	})
	t.Run("a metadata that is not an object is left and named", func(t *testing.T) {
		t.Parallel()
		f := map[string]any{"resources": deployed(), "metadata": "text"}
		adv := StampProvenance(f, set)
		if f["metadata"] != "text" {
			t.Errorf("metadata rewritten: %v", f["metadata"])
		}
		if !strings.Contains(adv, "metadata") || !strings.Contains(adv, "not an object") {
			t.Errorf("advisory = %q", adv)
		}
	})
	t.Run("an existing resource is skipped", func(t *testing.T) {
		t.Parallel()
		f := map[string]any{"resources": map[string]any{"Old": map[string]any{"existing": true}}}
		adv := StampProvenance(f, set)
		if _, ok := f["metadata"]; ok {
			t.Errorf("stamped for an existing resource: %v", f["metadata"])
		}
		if adv == "" {
			t.Error("want an advisory")
		}
	})
	t.Run("a fragment with no resource gets an advisory", func(t *testing.T) {
		t.Parallel()
		f := map[string]any{"parameters": map[string]any{"p": map[string]any{}}}
		if adv := StampProvenance(f, set); !strings.Contains(adv, "no resource") {
			t.Errorf("advisory = %q", adv)
		}
		if _, ok := f["metadata"]; ok {
			t.Error("stamped a fragment with no resource")
		}
	})
	t.Run("an empty set stamps nothing and says nothing", func(t *testing.T) {
		t.Parallel()
		f := map[string]any{"resources": deployed()}
		if adv := StampProvenance(f, nil); adv != "" {
			t.Errorf("advisory = %q", adv)
		}
		if _, ok := f["metadata"]; ok {
			t.Error("stamped an empty set")
		}
	})
}
