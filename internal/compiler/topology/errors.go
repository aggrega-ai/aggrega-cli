// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import "errors"

var (
	ErrSubAggregateLoad = errors.New("sub-aggregate load failed")
	ErrInvalidUsesFormat = errors.New("invalid uses format")
	ErrMaxDepth          = errors.New("max sub-aggregate nesting depth exceeded")
)
