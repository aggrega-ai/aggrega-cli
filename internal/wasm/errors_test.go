// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"errors"
	"strings"
	"testing"
)

// TestGuestReturnedError proves the guest-returned-error taxonomy: an empty
// message is the success case (nil), a non-empty message classifies as
// ErrWasmGuestError and names the export and the reason, and the guest
// structured-error sentinel is distinct from the crash-path sentinels so the
// three guest-failure transports converge on one classifiable taxonomy without
// collapsing into each other.
func TestGuestReturnedError(t *testing.T) {
	if err := GuestReturnedError("render_node", ""); err != nil {
		t.Fatalf("an empty message is the success case and must yield nil, got %v", err)
	}

	err := GuestReturnedError("render_node", "template evaluation failed")
	if err == nil {
		t.Fatal("a non-empty message must yield an error")
	}
	if !errors.Is(err, ErrWasmGuestError()) {
		t.Errorf("a guest-returned error must classify as ErrWasmGuestError, got %v", err)
	}
	if !strings.Contains(err.Error(), "render_node") || !strings.Contains(err.Error(), "template evaluation failed") {
		t.Errorf("the error must name the export and the reason, got %q", err.Error())
	}
	if errors.Is(err, ErrWasmGuestPanic()) || errors.Is(err, ErrWasmPluginExited()) {
		t.Error("a guest structured error must not classify as a crash or a proc_exit")
	}
}
