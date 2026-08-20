// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package set_the_field obtains an identity legitimately and then attempts to
// overwrite the code it carries. It must not compile.
package set_the_field

import cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"

var own = cerrors.Register("errors-sealing-probe/set-the-field", cerrors.Meta{
	ShortCode: "E0000",
	Message:   "an identity this package legitimately declares",
})

// Repointing an identity this package does own at a code another package
// declares is the route that stays open when the type itself cannot be named.
func Repoint() {
	own.code = "errors-sealing-probe/legitimate"
}
