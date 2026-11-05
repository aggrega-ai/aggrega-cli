// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestPrintedHeadNamesTheInstanceApartFromTheDocument asserts the head a reader
// meets for a failure whose position carries an instance: after the document
// position, in parentheses, where the reporting site holds a document; on its
// own, introduced by its own word, where it holds none. The instance never
// stands where a document belongs, so the source slot of the head always names
// something a reader can open or is plainly marked as not a document.
//
// The chain is the shape a provider guest's failure reaches the terminal in: the
// guest reports no condition of its own, so the head carries the host's
// guest-failure code, and the host frames the failure at the instance it was
// rendering.
func TestPrintedHeadNamesTheInstanceApartFromTheDocument(t *testing.T) {
	t.Parallel()

	short, ok := cerrors.ShortCodeOf(wasm.ErrWasmGuestError())
	if !ok {
		t.Fatal("the host's guest-failure condition reports no short code, so the heads below cannot be built")
	}
	bracket := "[" + string(cerrors.TranslationError) + " " + short + "]"

	for _, c := range []struct {
		name     string
		at       cerrors.SourceLocation
		wantHead string
	}{
		{
			"beside the document the site holds",
			cerrors.SourceLocation{File: "catalog-compose/recipe/adminer-fragment.yaml", Line: 11, Instance: "adminer"},
			bracket + " catalog-compose/recipe/adminer-fragment.yaml:11 (instance adminer)",
		},
		{
			"alone where the site holds no document",
			cerrors.SourceLocation{Instance: "adminer"},
			bracket + " instance adminer",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			guestErr := wasm.GuestReturnedError("provider/compose-provider", "render_node", wasm.GuestFailure{
				Message: "compose: a failure naming no registered condition",
			})
			framed := cerrors.Unidentified(cerrors.TranslationError, c.at, "rendering node \"adminer\"", nil, guestErr)

			got := formatCompilerError(framed)
			head, _, _ := strings.Cut(got, "\n")
			if head != c.wantHead {
				t.Errorf("the printed head is not the instance's ruled shape.\n  want: %s\n  got:  %s\n  whole diagnostic:\n%s", c.wantHead, head, got)
			}
			// The message is the occurrence's own sentence, unchanged by where the
			// position names the instance.
			if !strings.Contains(got, "\n  rendering node \"adminer\"") {
				t.Errorf("the message line moved:\n%s", got)
			}
		})
	}
}
