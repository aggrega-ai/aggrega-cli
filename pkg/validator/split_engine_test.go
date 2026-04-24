// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"errors"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestGenerateBaseUsesCueSchema_Simple(t *testing.T) {
	schema := types.OrderedMap[types.UsesSchemaEntry]{}
	schema.Set("databases", types.UsesSchemaEntry{Selector: `component.aggrega.ai/type in (resource, stateful-server)`, Inject: []string{"env", "file"}})

	cue := GenerateBaseUsesCueSchema(schema)
	if cue == "" {
		t.Fatal("expected non-empty CUE schema")
	}
	if !strings.Contains(cue, "databases?:") {
		t.Errorf("expected databases socket, got:\n%s", cue)
	}
	if !strings.Contains(cue, "target: string") {
		t.Errorf("expected target field, got:\n%s", cue)
	}
	if !strings.Contains(cue, "env?:") {
		t.Errorf("expected env mechanism, got:\n%s", cue)
	}
	if !strings.Contains(cue, "file?:") {
		t.Errorf("expected file mechanism, got:\n%s", cue)
	}
}

func TestGenerateBaseUsesCueSchema_NoInject(t *testing.T) {
	schema := types.OrderedMap[types.UsesSchemaEntry]{}
	schema.Set("apis", types.UsesSchemaEntry{Selector: `component.aggrega.ai/type in (server)`})

	cue := GenerateBaseUsesCueSchema(schema)
	if strings.Contains(cue, "inject?:") {
		t.Errorf("expected no inject block for Black-Box pattern, got:\n%s", cue)
	}
}

func TestGenerateBaseUsesCueSchema_Empty(t *testing.T) {
	cue := GenerateBaseUsesCueSchema(types.OrderedMap[types.UsesSchemaEntry]{})
	if cue != "" {
		t.Errorf("expected empty string for nil usesSchema, got: %s", cue)
	}
}

func TestGenerateBaseUsesCueSchema_Deterministic(t *testing.T) {
	schema := types.OrderedMap[types.UsesSchemaEntry]{}
	schema.Set("z_socket", types.UsesSchemaEntry{Selector: "s1"})
	schema.Set("a_socket", types.UsesSchemaEntry{Selector: "s2"})
	schema.Set("m_socket", types.UsesSchemaEntry{Selector: "s3"})

	cue1 := GenerateBaseUsesCueSchema(schema)
	cue2 := GenerateBaseUsesCueSchema(schema)
	if cue1 != cue2 {
		t.Error("expected deterministic output across calls")
	}
	// assertion refreshed for OrderedMap author-order iteration; was alphabetical under map[string]X
	// Verify author-insertion order: z, a, m (as declared by the test fixture)
	zIdx := strings.Index(cue1, "z_socket")
	aIdx := strings.Index(cue1, "a_socket")
	mIdx := strings.Index(cue1, "m_socket")
	if !(zIdx < aIdx && aIdx < mIdx) {
		t.Errorf("expected author order, got z=%d a=%d m=%d", zIdx, aIdx, mIdx)
	}
}

func TestUnifySchemas_Compatible(t *testing.T) {
	base := `{ uses?: { databases?: { [string]: { target: string } } } }`
	user := `{ values: { port: int }, uses?: { databases?: { [string]: { target: string } } } }`

	errs := UnifySchemas(base, user, "Component test:1.0.0")
	if len(errs) > 0 {
		t.Fatalf("expected no errors, got: %v", errs[0].Error())
	}
}

func TestUnifySchemas_InvalidUserCUE(t *testing.T) {
	errs := UnifySchemas("{}", "{ invalid CUE !!!", "Component test:1.0.0")
	if len(errs) == 0 {
		t.Fatal("expected error for invalid user CUE")
	}
	if !errors.Is(errs[0], ErrUsesSchemaUnification) {
		t.Errorf("expected ErrUsesSchemaUnification, got %v", errs[0].Err)
	}
}

func TestUnifySchemas_BothEmpty(t *testing.T) {
	errs := UnifySchemas("", "", "Component test:1.0.0")
	if len(errs) != 0 {
		t.Fatalf("expected no errors for both empty, got %d", len(errs))
	}
}
