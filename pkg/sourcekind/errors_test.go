// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package sourcekind

import (
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestErrOCIModuleSourceMalformedCarriesItsRegisteredIdentity pins the condition
// the document rule over an OCI module source raises.
func TestErrOCIModuleSourceMalformedCarriesItsRegisteredIdentity(t *testing.T) {
	t.Parallel()
	if code, ok := cerrors.ShortCodeOf(ErrOCIModuleSourceMalformed()); !ok || code != "E0379" {
		t.Errorf("ShortCodeOf = %q, %v; want E0379", code, ok)
	}
	if code, ok := cerrors.CodeOf(ErrOCIModuleSourceMalformed()); !ok || code != "oci-module-source-malformed" {
		t.Errorf("CodeOf = %q, %v; want oci-module-source-malformed", code, ok)
	}
}
