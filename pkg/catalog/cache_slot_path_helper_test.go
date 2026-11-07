// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// kindOfMediaType returns the published kind the media type mt names — the kind a
// fixture caching bytes it packed under mt keeps them under, as a registry fetch
// of those bytes would.
func kindOfMediaType(t *testing.T, mt string) string {
	t.Helper()
	kind, _, ok := oci.KindOfMediaType(mt)
	if !ok {
		t.Fatalf("media type %q names no published kind", mt)
	}
	return kind
}
