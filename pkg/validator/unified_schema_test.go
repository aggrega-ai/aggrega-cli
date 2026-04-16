// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestBuildUnifiedSchema_BothParts(t *testing.T) {
	t.Parallel()
	user := `values: { port: int }`
	usesSchema := map[string]types.UsesSchemaEntry{
		"databases": {Selector: `component.aggrega.ai/type=stateful-server`, Inject: []string{"env"}},
	}
	unified := BuildUnifiedSchema(user, usesSchema)
	if !strings.Contains(unified, "values: { port: int }") {
		t.Errorf("user schema missing: %s", unified)
	}
	if !strings.Contains(unified, "uses: close({") {
		t.Errorf("generated base missing: %s", unified)
	}
	if !strings.Contains(unified, `@selector("component.aggrega.ai/type=stateful-server")`) {
		t.Errorf("selector attribute missing: %s", unified)
	}
	if !strings.Contains(unified, `@inject("env")`) {
		t.Errorf("inject attribute missing: %s", unified)
	}
}

func TestBuildUnifiedSchema_EmptyUses(t *testing.T) {
	t.Parallel()
	user := `values: { port: int }`
	unified := BuildUnifiedSchema(user, nil)
	if unified != user {
		t.Errorf("expected user schema returned as-is, got: %s", unified)
	}
}

func TestBuildUnifiedSchema_EmptyUser(t *testing.T) {
	t.Parallel()
	usesSchema := map[string]types.UsesSchemaEntry{
		"databases": {Selector: "x"},
	}
	unified := BuildUnifiedSchema("", usesSchema)
	if !strings.Contains(unified, "uses:") {
		t.Errorf("expected base only, got: %s", unified)
	}
}

func TestBuildUnifiedSchema_BothEmpty(t *testing.T) {
	t.Parallel()
	if unified := BuildUnifiedSchema("", nil); unified != "" {
		t.Errorf("expected empty string, got: %q", unified)
	}
}
