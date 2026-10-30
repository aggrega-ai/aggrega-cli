// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package credentialfailure

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestAnUnreadableStoreKeepsItsIdentityButNotItsText pins the two halves of what
// the sentence keeps from the store's own failure: every condition a caller could
// match before still matches, and none of the store's text can be reached through
// the chain.
//
// The causes are built by hand because the conditions in question — a context
// already done, a credential helper that is not installed, a file the process
// may not open — each need a machine arranged a particular way to arise for real,
// and what is under test is how the sentence carries them, not how the store
// raises them. Each cause wraps its condition beneath text standing in for the
// store's, so a sentence that kept that text on its chain is caught.
func TestAnUnreadableStoreKeepsItsIdentityButNotItsText(t *testing.T) {
	const storeText = "probe-placeholder-token-no-colon"
	cases := []struct {
		name      string
		condition error
	}{
		{"a cancelled context", context.Canceled},
		{"an expired deadline", context.DeadlineExceeded},
		{"a credential helper that is not installed", exec.ErrNotFound},
		{"a file the process may not open", fs.ErrPermission},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cause := fmt.Errorf("auth %q does not conform: %w", storeText, tc.condition)
			err := Unreadable("registry.example", "/home/u/.docker/config.json", cause)
			wrapped := fmt.Errorf("resolve: %w", err)

			if !errors.Is(wrapped, tc.condition) {
				t.Errorf("errors.Is(%v) = false through the sentence; a caller matching it no longer can", tc.condition)
			}
			if errors.Is(wrapped, errors.New(tc.condition.Error())) {
				t.Error("errors.Is matched an unrelated error with the same text; it must match by identity only")
			}
			if next := errors.Unwrap(err); next != nil {
				t.Errorf("the sentence unwraps to %q; the store's text is reachable beneath it", next)
			}
			for _, rendered := range []struct{ how, text string }{
				{"the error's text", wrapped.Error()},
				{"the one-line cause chain", cerrors.CauseChain(wrapped)},
			} {
				if strings.Contains(rendered.text, storeText) {
					t.Errorf("%s carries the store's text:\n%s", rendered.how, rendered.text)
				}
			}
			var target *unreadable
			if !errors.As(wrapped, &target) || target.cause != cause {
				t.Error("errors.As does not find the sentence itself on the chain")
			}
		})
	}
}

// TestTheSentenceNamesTheRegistryAndTheFile pins what the sentence says in each
// of the two situations it is composed for: the store opened and named its file,
// or opening the store is what failed and no file can be named from it.
func TestTheSentenceNamesTheRegistryAndTheFile(t *testing.T) {
	cases := []struct {
		name string
		file string
		// names is what the sentence must carry for the file.
		names string
	}{
		{"the store named its file", "/home/u/.docker/config.json", "/home/u/.docker/config.json"},
		{"the store could not be opened", "", "config.json in the directory DOCKER_CONFIG names"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := Unreadable("registry.example:5000", tc.file, errors.New("store text")).Error()
			for _, want := range []string{`"registry.example:5000"`, tc.names, "is not shown", "`aggrega login registry.example:5000`"} {
				if !strings.Contains(msg, want) {
					t.Errorf("the sentence does not carry %q:\n%s", want, msg)
				}
			}
			if strings.Contains(msg, "store text") {
				t.Errorf("the sentence carries the store's text:\n%s", msg)
			}
		})
	}
}
