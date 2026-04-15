// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import "errors"

// ErrEmptySource is emitted when a Recipe has `source: ""` (empty string).
// Nil/absent source = sourceless (generative) mode; non-empty string = sourced.
// Empty string is never valid.
var ErrEmptySource = errors.New("recipe source is empty string; use nil for sourceless mode or a non-empty path for sourced mode")
