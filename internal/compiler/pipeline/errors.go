// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ErrPolicySettingsInvalid is the ABI-stable sentinel for a policy attachment
// whose operator-supplied with.settings does not satisfy the settings schema the
// attached plugin declares in its describe(). It is a build fault — a
// misconfigured attachment — surfaced before the plugin is dispatched, distinct
// from a policy verdict (a plugin that ran and rejected the build) and from a
// descriptor load fault (a plugin whose own self-report the host refuses). The
// wrapping error names the plugin, the offending settings key or path, and the
// schema's expectation, so the operator sees exactly which setting to fix. A
// build halts on it loudly rather than handing the plugin ill-shaped settings.
const ErrPolicySettingsInvalid cerrors.Code = "compiler-pipeline/policy-settings-invalid"

var _ = cerrors.Register(ErrPolicySettingsInvalid, cerrors.Meta{
	Message: "policy attachment settings do not satisfy the plugin's declared settings schema",
})

// ErrAnnotationInjectionRejected is the ABI-stable sentinel for an
// annotation-transform plugin whose output the host refuses to apply: an
// injection naming a component coordinate the host did not supply as a target.
// The annotation-transform role is host-confined to adding string-valued
// annotations onto existing component coordinates; the wire shape admits nothing
// else, and an injection that reaches outside the supplied target set halts the
// build loudly rather than being silently absorbed. It is the annotation-role
// analogue of the label role's patch-confinement halt — the structural facet
// guarantee for the annotation-transform role.
const ErrAnnotationInjectionRejected cerrors.Code = "compiler-pipeline/annotation-injection-rejected"

var _ = cerrors.Register(ErrAnnotationInjectionRejected, cerrors.Meta{
	Message: "annotation-transform plugin output was rejected: an injection targets a component coordinate the host did not supply",
})
