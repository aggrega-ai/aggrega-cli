// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package closure

import (
	"errors"
	"testing"
)

// TestLibraryFloorSelectsHighestAdmitted holds that the request a library's
// consumers settle on is the highest floor within one major, which the lookup
// reads as `>=floor` and answers with the highest held version it admits, and
// that a major disagreement is refused.
func TestLibraryFloorSelectsHighestAdmitted(t *testing.T) {
	const uri = "BaseComponents"
	consumers := func(versions ...string) *pendingLibrary {
		lib := &pendingLibrary{versionByConsumer: map[consumerIdentity]string{}}
		for i, v := range versions {
			lib.versionByConsumer[consumerIdentity{Kind: KindComponent, URI: string(rune('a' + i))}] = v
		}
		return lib
	}

	t.Run("a floor and a bare consumer request the floor", func(t *testing.T) {
		selected, resolveURI, ask, err := libraryRequest(uri, consumers("1.1.0", ""))
		if err != nil || !ask || selected != "1.1.0" || resolveURI != uri+":1.1.0" {
			t.Fatalf("got (%q, %q, %v, %v)", selected, resolveURI, ask, err)
		}
	})

	t.Run("two floors request the higher", func(t *testing.T) {
		selected, _, _, err := libraryRequest(uri, consumers("1.1.0", "1.0.0"))
		if err != nil || selected != "1.1.0" {
			t.Fatalf("got (%q, %v), want 1.1.0", selected, err)
		}
	})

	t.Run("a major disagreement is refused", func(t *testing.T) {
		_, _, _, err := libraryRequest(uri, consumers("1.1.0", "2.0.0"))
		if !errors.Is(err, ErrMVSConflict()) {
			t.Fatalf("err = %v, want ErrMVSConflict", err)
		}
	})

	t.Run("bare consumers request no version", func(t *testing.T) {
		selected, resolveURI, ask, err := libraryRequest(uri, consumers("", ""))
		if err != nil || !ask || selected != "" || resolveURI != uri {
			t.Fatalf("got (%q, %q, %v, %v)", selected, resolveURI, ask, err)
		}
	})
}
