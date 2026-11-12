// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"encoding/json"
	"testing"
)

// TestErrorDataFullyPopulatedRoundTrip pins the wire shape for a
// fully populated payload. Every documented field is present in
// the emitted JSON.
func TestErrorDataFullyPopulatedRoundTrip(t *testing.T) {
	t.Parallel()
	d := newErrorData("/workspace/release.yaml", 12, 7)
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"file":"/workspace/release.yaml","line":12,"column":7}`
	if string(out) != want {
		t.Errorf("marshal:\n  got:  %s\n  want: %s", out, want)
	}
	var back errorData
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back != *d {
		t.Errorf("round-trip: got %+v, want %+v", back, *d)
	}
}

// TestErrorDataFileOnlyOmitsZeroLineColumn pins the partial-data
// case: a payload carrying only File omits Line and Column from
// the wire entirely (via omitempty on zero-valued ints).
func TestErrorDataFileOnlyOmitsZeroLineColumn(t *testing.T) {
	t.Parallel()
	d := newErrorData("/workspace/release.yaml", 0, 0)
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"file":"/workspace/release.yaml"}`
	if string(out) != want {
		t.Errorf("marshal:\n  got:  %s\n  want: %s", out, want)
	}
}

// TestErrorDataAllZeroOmitsEverything pins the no-data case: an
// errorData with every field at its zero value emits an empty
// object. (Callers in production should not construct this shape —
// they should pass nil for Data instead — but the marshal behavior
// is documented.)
func TestErrorDataAllZeroOmitsEverything(t *testing.T) {
	t.Parallel()
	d := errorData{}
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{}`
	if string(out) != want {
		t.Errorf("marshal:\n  got:  %s\n  want: %s", out, want)
	}
}

// TestErrorDataLineOnly pins the line-without-column case (rare
// but possible — e.g., yaml.v3 parse errors carry a line number
// without a column).
func TestErrorDataLineOnly(t *testing.T) {
	t.Parallel()
	d := newErrorData("/file.yaml", 5, 0)
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"file":"/file.yaml","line":5}`
	if string(out) != want {
		t.Errorf("marshal:\n  got:  %s\n  want: %s", out, want)
	}
}
