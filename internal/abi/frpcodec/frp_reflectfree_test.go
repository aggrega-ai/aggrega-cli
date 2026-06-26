// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package frpcodec_test

import (
	"os"
	"strings"
	"testing"
)

// TestGeneratedCodec_ReflectFree asserts the generated codec source imports no
// reflect package and references no reflect symbol. The reflect-free property
// is the robustness claim of the generated codec — a guest decoding through it
// builds correctly without the GC sensitivity reflection drags in — so a
// regression that reintroduces reflect (via a future generator change) must
// fail loudly. The check is precise: it ignores the word "reflect" inside the
// "reflect-free" descriptor that appears in the generated godoc, matching only
// an actual import line or a reflect.Symbol reference.
func TestGeneratedCodec_ReflectFree(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("frp_gen.go")
	if err != nil {
		t.Fatalf("read generated codec: %v", err)
	}
	for i, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		// An import of the reflect package, in either single- or grouped-import
		// form.
		if trimmed == `import "reflect"` || trimmed == `"reflect"` {
			t.Errorf("frp_gen.go:%d imports reflect: %q", i+1, trimmed)
		}
		// A qualified reflect.* reference. Skip comment lines so the
		// "reflect-free" descriptor in the godoc does not trip the check.
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.Contains(line, "reflect.") {
			t.Errorf("frp_gen.go:%d references reflect.: %q", i+1, trimmed)
		}
	}
}
