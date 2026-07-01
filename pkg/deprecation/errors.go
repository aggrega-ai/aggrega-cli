// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package deprecation

import "errors"

// ErrDeprecatedEntity halts a build that consumes a deprecated closure entity
// under a halting policy. It fires from two independent sources under one
// build-time gate: a retroactive deprecation marker recorded in the lockfile
// whose severity meets or exceeds the configured halt threshold, and an
// author-time metadata.deprecated entity when the build is configured to halt on
// deprecated entities. Below the threshold (or with halting off) the same
// consumption surfaces as an advisory rather than this error.
var ErrDeprecatedEntity = errors.New("deprecation: build consumes a deprecated entity")
