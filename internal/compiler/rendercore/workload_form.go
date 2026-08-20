// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Workload controller-shape form values. The form vocabulary is abstract
// controller-shape intent; each provider engine maps a form to its own
// native shape. Empty is treated as stateless.
const (
	WorkloadFormStateless = "stateless"
	WorkloadFormStateful  = "stateful"
	WorkloadFormDaemon    = "daemon"
	WorkloadFormScheduled = "scheduled"
	WorkloadFormTask      = "task"
	WorkloadFormReference = "reference"
)

// ErrPersistentTemplateRequiresStatefulForm is the cross-provider companion
// constraint: a per-replica claim template (persistentTemplate volume) is only
// coherent on a stateful workload, because only the stateful shape gives each
// replica the stable identity a per-replica claim needs. Declaring one on any
// other form halts loudly rather than silently dropping the per-replica
// semantics. Surfaced wrapped in a CompilerError via cerrors so errors.Is
// matches.
func ErrPersistentTemplateRequiresStatefulForm() error {
	return errPersistentTemplateRequiresStatefulForm
}

var errPersistentTemplateRequiresStatefulForm = cerrors.Register("compiler-rendercore/persistent-template-requires-stateful-form", cerrors.Meta{
	ShortCode: "E0021",
	Message:   "persistentTemplate volume requires form: stateful",
})

// ErrScheduledFormRequiresSchedule is the companion constraint that a
// form: scheduled workload must carry a schedule: a scheduled workload maps to
// a recurring, time-triggered resource, and without a schedule there is no
// coherent recurrence to emit. Declaring form: scheduled with no schedule halts
// loudly rather than invent one. Surfaced wrapped in a CompilerError via cerrors
// so errors.Is matches.
func ErrScheduledFormRequiresSchedule() error { return errScheduledFormRequiresSchedule }

var errScheduledFormRequiresSchedule = cerrors.Register("compiler-rendercore/scheduled-form-requires-schedule", cerrors.Meta{
	ShortCode: "E0025",
	Message:   "form: scheduled requires a schedule",
})

// ErrScheduleRequiresScheduledForm is the reverse companion constraint: a
// schedule is only meaningful on a form: scheduled workload. A schedule set on
// any other form has no recurring resource to attach to, so it halts loudly
// rather than silently ignore the schedule. Surfaced wrapped in a CompilerError
// via cerrors so errors.Is matches.
func ErrScheduleRequiresScheduledForm() error { return errScheduleRequiresScheduledForm }

var errScheduleRequiresScheduledForm = cerrors.Register("compiler-rendercore/schedule-requires-scheduled-form", cerrors.Meta{
	ShortCode: "E0024",
	Message:   "schedule requires form: scheduled",
})

// ErrReferenceFormRequiresEndpoint is the companion constraint that a
// form: reference workload must declare an endpoint: a reference is an external
// endpoint pointer with no compute, and without an endpoint there is nothing to
// point at. Surfaced wrapped in a CompilerError via cerrors so errors.Is
// matches.
func ErrReferenceFormRequiresEndpoint() error { return errReferenceFormRequiresEndpoint }

var errReferenceFormRequiresEndpoint = cerrors.Register("compiler-rendercore/reference-form-requires-endpoint", cerrors.Meta{
	ShortCode: "E0023",
	Message:   "form: reference requires an endpoint",
})

// ErrEndpointRequiresReferenceForm is the reverse companion constraint: an
// endpoint (and its companion port/credential) is only meaningful on a
// form: reference workload. Declaring it on any other form halts loudly rather
// than silently ignore it. Surfaced wrapped in a CompilerError via cerrors so
// errors.Is matches.
func ErrEndpointRequiresReferenceForm() error { return errEndpointRequiresReferenceForm }

var errEndpointRequiresReferenceForm = cerrors.Register("compiler-rendercore/endpoint-requires-reference-form", cerrors.Meta{
	ShortCode: "E0019",
	Message:   "endpoint requires form: reference",
})

// ErrInvalidCronSchedule is emitted when a schedule fails the structural cron
// check: it is neither one of the accepted macros (@hourly, @daily, @weekly,
// @monthly, @yearly) nor a standard 5-field crontab expression using only the
// legal characters. Surfaced wrapped in a CompilerError via cerrors so
// errors.Is matches.
func ErrInvalidCronSchedule() error { return errInvalidCronSchedule }

var errInvalidCronSchedule = cerrors.Register("compiler-rendercore/invalid-cron-schedule", cerrors.Meta{
	ShortCode: "E0020",
	Message:   "schedule is not a valid cron expression",
})

// NormalizeWorkloadForm maps an empty form to the stateless default and
// returns every other value unchanged. It is the single source of the
// empty-to-stateless rule shared by every provider synth core.
func NormalizeWorkloadForm(form string) string {
	if form == "" {
		return WorkloadFormStateless
	}
	return form
}

// ValidateWorkloadForm enforces the provider-agnostic companion constraint that
// a persistentTemplate volume requires form: stateful. Every provider synth
// core calls it at entry so the rule is single-sourced. nodeID attributes the
// error to the offending node. A persistentTemplate volume on any non-stateful
// form halts with ErrPersistentTemplateRequiresStatefulForm; the reverse
// (form: stateful with no persistentTemplate) is allowed.
func ValidateWorkloadForm(w *types.Workload, nodeID string) error {
	form := NormalizeWorkloadForm(w.Form)

	if form != WorkloadFormStateful {
		for _, v := range w.Volumes.All() {
			if v.PersistentTemplate != nil {
				return cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
					"workload declares a persistentTemplate volume but is not form: stateful; declare form: stateful",
					nil, ErrPersistentTemplateRequiresStatefulForm())
			}
		}
	}

	switch {
	case form == WorkloadFormReference && w.Endpoint == "":
		return cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
			"workload is form: reference but declares no endpoint; set endpoint to the external host or URL the reference points at",
			nil, ErrReferenceFormRequiresEndpoint())
	case form != WorkloadFormReference && w.Endpoint != "":
		return cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
			"workload declares an endpoint but is not form: reference; declare form: reference or remove the endpoint",
			nil, ErrEndpointRequiresReferenceForm())
	case form == WorkloadFormScheduled && w.Schedule == "":
		return cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
			"workload is form: scheduled but declares no schedule; set schedule to a 5-field cron expression or an @-macro (e.g. @daily)",
			nil, ErrScheduledFormRequiresSchedule())
	case form != WorkloadFormScheduled && w.Schedule != "":
		return cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
			"workload declares a schedule but is not form: scheduled; declare form: scheduled or remove the schedule",
			nil, ErrScheduleRequiresScheduledForm())
	case form == WorkloadFormScheduled && !isValidCronSchedule(w.Schedule):
		return cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
			"workload schedule is not a valid cron expression; use a 5-field crontab expression (e.g. \"0 0 * * *\") or one of @hourly, @daily, @weekly, @monthly, @yearly",
			nil, ErrInvalidCronSchedule())
	}

	return nil
}

// isValidCronSchedule performs a light structural check on a cron schedule
// string. It accepts exactly the five macros @hourly, @daily, @weekly,
// @monthly, and @yearly, or a standard 5-field crontab expression whose fields
// use only the legal characters (digits, '*', ',', '-', '/'). It is not a full
// cron parser: it rejects 6-field (seconds-prefixed) expressions and any field
// carrying an illegal character, but does not validate numeric ranges or step
// arithmetic. The check is deterministic and allocation-light.
func isValidCronSchedule(s string) bool {
	switch s {
	case "@hourly", "@daily", "@weekly", "@monthly", "@yearly":
		return true
	}
	fields := strings.Fields(s)
	if len(fields) != 5 {
		return false
	}
	for _, f := range fields {
		for _, r := range f {
			switch {
			case r >= '0' && r <= '9':
			case r == '*' || r == ',' || r == '-' || r == '/':
			default:
				return false
			}
		}
	}
	return true
}
