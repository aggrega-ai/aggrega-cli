// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
)

// entries returns n entries named p0, p1, ….
func entries(prefix string, n int) map[string]any {
	m := map[string]any{}
	for i := 0; i < n; i++ {
		m[fmt.Sprintf("%s%d", prefix, i)] = map[string]any{"type": "string"}
	}
	return m
}

func TestCheckLimits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		level   map[string]any
		encoded []byte
		measure string
		limit   string
	}{
		{"template size", map[string]any{}, make([]byte, 4*1024*1024+1), "bytes", "4194304 bytes"},
		{"resource size", map[string]any{"resources": map[string]any{"Big": map[string]any{"p": strings.Repeat("x", 1024*1024)}}}, nil, "/resources/Big", "1048576 bytes"},
		{"parameters", map[string]any{"parameters": entries("p", 257)}, nil, "257 parameters", "256 parameters"},
		{"variables", map[string]any{"variables": entries("v", 513)}, nil, "513 variables", "512 variables"},
		{"resources", map[string]any{"resources": entries("r", 801)}, nil, "801 resources", "800 resources"},
		{"outputs", map[string]any{"outputs": entries("o", 65)}, nil, "65 outputs", "64 outputs"},
		{"expression", map[string]any{"variables": map[string]any{"long": "[" + strings.Repeat("a", 24575) + "]"}}, nil, "24577 characters at JSON pointer /variables/long", "24576 characters"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := CheckLimits(tt.level, tt.encoded)
			if !errors.Is(err, plugincondition.ErrPluginCannotExpressInput()) {
				t.Fatalf("err = %v, want cannot express input", err)
			}
			for _, want := range []string{"arm-provider:", tt.measure, tt.limit, "split the boundary"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message %q does not contain %q", err, want)
				}
			}
		})
	}
}

func TestCheckLimitsAdmitsTheLimits(t *testing.T) {
	t.Parallel()
	level := map[string]any{
		"parameters": entries("p", 256), "variables": entries("v", 512), "resources": entries("r", 800), "outputs": entries("o", 64),
		"metadata": map[string]any{"edge": "[" + strings.Repeat("a", 24574) + "]", "text": strings.Repeat("a", 30000)},
	}
	if err := CheckLimits(level, make([]byte, 4*1024*1024)); err != nil {
		t.Fatal(err)
	}
}
