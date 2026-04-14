// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import "errors"

var (
	ErrSelectorViolation = errors.New("selector violation")
	ErrCatalogLookup     = errors.New("catalog lookup failed")
)
