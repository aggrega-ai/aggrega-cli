// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import "errors"

var (
	ErrProvidesSelfContainment = errors.New("provides block references cross-node data")
	ErrProvidesEvaluation      = errors.New("provides template evaluation failed")
	ErrInjectResolution        = errors.New("inject key resolution failed")
	ErrJsonnetNotSupported          = errors.New("valuesMappingJsonnet is not supported in ; use valuesMapping or valuesMappingFile")
	ErrSelfInjectForbiddenReference = errors.New("self-inject value: template references cross-node data")
	ErrSelfInjectKeyCollision       = errors.New("duplicate delivery key across self-inject and uses-inject")

	// ErrUnknownProvider is the class-A fatal halt produced by the build-time
	// engineFactory when a Recipe declares a provider name that the CLI does
	// not register. The CLI's engineFactory closure wraps this sentinel with
	// the offending provider name so callers can match via
	// errors.Is(err, ErrUnknownProvider) regardless of which provider
	// triggered the halt.
	//
	// Covered by . Replaces the earlier ErrUnsupportedProvider
	// from the deleted internal/compiler/provider/ tree, which was dead code
	// after the v2 rewrite.
	ErrUnknownProvider = errors.New("unknown provider")
)
