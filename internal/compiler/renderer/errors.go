// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import "errors"

var (
	ErrProvidesSelfContainment = errors.New("provides block references cross-node data")
	ErrProvidesEvaluation      = errors.New("provides template evaluation failed")
	ErrInjectResolution        = errors.New("inject key resolution failed")
	ErrJsonnetNotSupported     = errors.New("valuesMappingJsonnet is not supported in ; use valuesMapping or valuesMappingFile")
)
