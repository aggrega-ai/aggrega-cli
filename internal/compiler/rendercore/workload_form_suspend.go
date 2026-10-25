// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ErrSuspendRequiresScheduledForm is the companion constraint that suspend is
// only meaningful on a form: scheduled workload: it holds a recurring schedule,
// and no other form carries one. Declaring it on any other form halts loudly
// rather than silently ignore it. Surfaced wrapped in a CompilerError via
// cerrors so errors.Is matches.
func ErrSuspendRequiresScheduledForm() error { return errSuspendRequiresScheduledForm }

var errSuspendRequiresScheduledForm = cerrors.Register("suspend-requires-scheduled-form", cerrors.Meta{
	ShortCode: "E0356",
	Message:   "suspend requires form: scheduled",
	RulePlace: cerrors.RuleEvaluatedElsewhere("The provider guest rendering the node evaluates this, inside its own module and over the " +
		"workload the host handed it: suspend holds a recurring schedule, and only the scheduled shape " +
		"carries one. Which workload shapes a target accepts is the provider's to know and differs " +
		"between them, so no single rule in the dispatch could be right for every one, and no view of a " +
		"compilation reaches the guest that decides it."),
})
