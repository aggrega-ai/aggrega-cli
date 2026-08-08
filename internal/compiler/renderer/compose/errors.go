// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compose

import cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"

// ErrEmptySource is emitted when a Recipe has `source: ""` (empty string).
// Nil/absent source = sourceless (synthesized) mode; non-empty string = sourced.
// Empty string is never valid. Mirrors helm/errors.go ErrEmptySource for
// consistency across providers.
const ErrEmptySource cerrors.Code = "compiler-renderer-compose/empty-source"

var _ = cerrors.Register(ErrEmptySource, cerrors.Meta{
	Message: "recipe source is empty string; use nil for synthesized mode or a non-empty path for sourced mode",
})

// ErrTranslationProbeNoPort is emitted when a probe http: shape resolves to a
// container that declares no ports — compose healthchecks need a port to
// build the curl URL.
const ErrTranslationProbeNoPort cerrors.Code = "compiler-renderer-compose/translation-probe-no-port"

var _ = cerrors.Register(ErrTranslationProbeNoPort, cerrors.Meta{
	Message: "TRANSLATION_ERROR: probe http: requires at least one container port; container declares none",
})

// ErrTranslationProbeShape is emitted when a ProbeSpec has no recognised shape
// (no http, no tcp, no exec). Should never occur after schema validation;
// defensive at synthesis time.
const ErrTranslationProbeShape cerrors.Code = "compiler-renderer-compose/translation-probe-shape"

var _ = cerrors.Register(ErrTranslationProbeShape, cerrors.Meta{
	Message: "TRANSLATION_ERROR: probe declares no shape (http/tcp/exec)",
})

// ErrTranslationConfigMapMissingArtifact is emitted when a configmap volume's
// service-level configs: reference resolves to no top-level configs: entry in
// the assembled boundary. A configmap volume is delivered through compose's own
// configs: primitive, referenced by the configmap's name; the entry carrying the
// content comes from a Recipe's artifacts: input, whose compose fragment
// declares it at the top level. An unresolved reference means that artifact is
// missing, and the mount would be dangling at run time.
//
// The check belongs to boundary assembly rather than to workload synthesis: the
// reference and the declaration reach the output through different input kinds
// and may come from different nodes of one dispatch group, so the assembled
// boundary is the first point where both are present.
const ErrTranslationConfigMapMissingArtifact cerrors.Code = "compiler-renderer-compose/translation-config-map-missing-artifact"

var _ = cerrors.Register(ErrTranslationConfigMapMissingArtifact, cerrors.Meta{
	Message: "TRANSLATION_ERROR: configmap volume requires a file emitted via the Recipe's artifacts: input; none found",
})

// ErrUnsupportedComposeForm is emitted when a Workload declares a controller
// form compose cannot express. compose renders stateless and stateful
// workloads as long-running services; daemon (one pod per node), scheduled
// (cron-driven), and task (run-to-completion) have no clean single-host
// compose mapping, so they halt loudly rather than degrade into a plain
// service that misrepresents the intent.
const ErrUnsupportedComposeForm cerrors.Code = "compiler-renderer-compose/unsupported-compose-form"

var _ = cerrors.Register(ErrUnsupportedComposeForm, cerrors.Meta{
	Message: "TRANSLATION_ERROR: compose cannot express this workload form",
})

// ErrUnsupportedComposeLifecycle is emitted when a container declares lifecycle
// hooks (postStart/preStop) on a compose target. compose has no lifecycle-hook
// primitive; a silently-unrun hook is a correctness surprise, so a declared
// lifecycle halts loudly rather than drop the hook.
const ErrUnsupportedComposeLifecycle cerrors.Code = "compiler-renderer-compose/unsupported-compose-lifecycle"

var _ = cerrors.Register(ErrUnsupportedComposeLifecycle, cerrors.Meta{
	Message: "TRANSLATION_ERROR: compose has no lifecycle-hook primitive; container declares lifecycle hooks",
})

// ErrUnsupportedComposeEnvFrom is emitted when a container declares envFrom
// (bulk env from a ConfigMap or Secret source) on a compose target. compose
// has no env-from-object primitive; a source reference is not a host file, so
// reinterpreting it as an env_file would silently change meaning. It halts
// loudly rather than degrade.
const ErrUnsupportedComposeEnvFrom cerrors.Code = "compiler-renderer-compose/unsupported-compose-env-from"

var _ = cerrors.Register(ErrUnsupportedComposeEnvFrom, cerrors.Meta{
	Message: "TRANSLATION_ERROR: compose has no env-from-object primitive; container declares envFrom",
})

// ErrUnconvertibleComposeQuantity is emitted when a container resources quantity
// (cpu or memory) cannot be converted to the compose deploy.resources form
// without loss — for example a fractional memory quantity or an unrecognized
// suffix. It halts loudly rather than round or emit a wrong value.
const ErrUnconvertibleComposeQuantity cerrors.Code = "compiler-renderer-compose/unconvertible-compose-quantity"

var _ = cerrors.Register(ErrUnconvertibleComposeQuantity, cerrors.Meta{
	Message: "TRANSLATION_ERROR: resources quantity cannot be converted to a compose deploy.resources value",
})
