// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package deprecation

import (
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ErrDeprecatedEntity halts a build that consumes a deprecated closure entity
// under a halting policy. It fires when a retroactive deprecation marker
// recorded in the lockfile carries a severity that meets or exceeds the
// configured halt threshold. Below the threshold, or with no threshold
// configured, the same consumption surfaces as an advisory rather than this
// error.
const ErrDeprecatedEntity cerrors.Code = "deprecation/deprecated-entity"

var _ = cerrors.Register(ErrDeprecatedEntity, cerrors.Meta{
	Message: "deprecation: build consumes a deprecated entity",
})
