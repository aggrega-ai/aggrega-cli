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
	schema := map[string]types.UsesSchemaEntry{
		"databases": {
			Selector: `component.aggrega.ai/type in (resource, stateful-server)`,
			Inject:   []string{"env", "file"},
		},
	}

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
	schema := map[string]types.UsesSchemaEntry{
		"apis": {
			Selector: `component.aggrega.ai/type in (server)`,
		},
	}

	cue := GenerateBaseUsesCueSchema(schema)
	if strings.Contains(cue, "inject?:") {
		t.Errorf("expected no inject block for Black-Box pattern, got:\n%s", cue)
	}
}

func TestGenerateBaseUsesCueSchema_Empty(t *testing.T) {
	cue := GenerateBaseUsesCueSchema(nil)
	if cue != "" {
		t.Errorf("expected empty string for nil usesSchema, got: %s", cue)
	}
}

func TestGenerateBaseUsesCueSchema_Deterministic(t *testing.T) {
	schema := map[string]types.UsesSchemaEntry{
		"z_socket": {Selector: "s1"},
		"a_socket": {Selector: "s2"},
		"m_socket": {Selector: "s3"},
	}

	cue1 := GenerateBaseUsesCueSchema(schema)
	cue2 := GenerateBaseUsesCueSchema(schema)
	if cue1 != cue2 {
		t.Error("expected deterministic output across calls")
	}
	// Verify sorted order
	aIdx := strings.Index(cue1, "a_socket")
	mIdx := strings.Index(cue1, "m_socket")
	zIdx := strings.Index(cue1, "z_socket")
	if !(aIdx < mIdx && mIdx < zIdx) {
		t.Errorf("expected alphabetical order, got a=%d m=%d z=%d", aIdx, mIdx, zIdx)
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
