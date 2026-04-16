// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"reflect"
	"testing"
)

func TestExtractProvidesKeys_Basic(t *testing.T) {
	t.Parallel()
	schema := `values: { port: int }
provides: { host: string, service_port: string, region: string }`

	keys, errs := ExtractProvidesKeys(schema)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	want := []string{"host", "region", "service_port"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("keys: got %v, want %v", keys, want)
	}
}

func TestExtractProvidesKeys_EmptySchema(t *testing.T) {
	t.Parallel()
	keys, errs := ExtractProvidesKeys("")
	if len(errs) != 0 || keys != nil {
		t.Errorf("expected nil/empty, got keys=%v errs=%v", keys, errs)
	}
}

func TestExtractProvidesKeys_NoProvidesPillar(t *testing.T) {
	t.Parallel()
	schema := `values: { port: int }`
	keys, errs := ExtractProvidesKeys(schema)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if keys != nil {
		t.Errorf("expected nil keys for missing provides, got %v", keys)
	}
}

func TestExtractProvidesKeys_OptionalFieldsIncluded(t *testing.T) {
	t.Parallel()
	schema := `provides: { host: string, port?: string }`
	keys, _ := ExtractProvidesKeys(schema)
	if len(keys) != 2 {
		t.Errorf("expected 2 keys (host + optional port), got %v", keys)
	}
}

func TestExtractProvidesKeys_InvalidCUE(t *testing.T) {
	t.Parallel()
	_, errs := ExtractProvidesKeys("{ not valid CUE !!!")
	if len(errs) == 0 {
		t.Fatal("expected compilation error")
	}
	if errs[0].Constraint == "" {
		t.Error("expected non-empty constraint message")
	}
}

func TestExtractProvidesKeys_Deterministic(t *testing.T) {
	t.Parallel()
	schema := `provides: { z_key: string, a_key: string, m_key: string }`
	for i := 0; i < 5; i++ {
		keys, _ := ExtractProvidesKeys(schema)
		want := []string{"a_key", "m_key", "z_key"}
		if !reflect.DeepEqual(keys, want) {
			t.Errorf("run %d: got %v, want %v", i, keys, want)
		}
	}
}
