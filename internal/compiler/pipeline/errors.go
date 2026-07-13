// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import "errors"

// ErrPolicySettingsInvalid is the ABI-stable sentinel for a policy attachment
// whose operator-supplied with.settings does not satisfy the settings schema the
// attached plugin declares in its describe(). It is a build fault — a
// misconfigured attachment — surfaced before the plugin is dispatched, distinct
// from a policy verdict (a plugin that ran and rejected the build) and from a
// descriptor load fault (a plugin whose own self-report the host refuses). The
// wrapping error names the plugin, the offending settings key or path, and the
// schema's expectation, so the operator sees exactly which setting to fix. A
// build halts on it loudly rather than handing the plugin ill-shaped settings.
var ErrPolicySettingsInvalid = errors.New("policy attachment settings do not satisfy the plugin's declared settings schema")
