// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile_test

import "github.com/aggrega-ai/aggrega-cli/pkg/digest"

// fixtureDigest returns a content digest in the one spelling the lockfile's
// digest fields admit — `sha256:` followed by 64 lowercase hex characters —
// derived from a readable label. The loader refuses any other spelling, so a
// fixture that is to load writes its digests through this; distinct labels give
// distinct digests, and the same label gives the same digest wherever a test
// compares one value against another.
func fixtureDigest(label string) string {
	return digest.ContentDigest([]byte(label))
}
