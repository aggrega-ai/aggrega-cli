// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compose

import "errors"

// ErrEmptySource is emitted when a Recipe has `source: ""` (empty string).
// Nil/absent source = sourceless (synthesized) mode; non-empty string = sourced.
// Empty string is never valid. Mirrors helm/errors.go ErrEmptySource for
// consistency across providers.
var ErrEmptySource = errors.New("recipe source is empty string; use nil for synthesized mode or a non-empty path for sourced mode")

// ErrTranslationProbeNoPort is emitted when a probe http: shape resolves to a
// container that declares no ports — compose healthchecks need a port to
// build the curl URL.
var ErrTranslationProbeNoPort = errors.New("TRANSLATION_ERROR: probe http: requires at least one container port; container declares none")

// ErrTranslationProbeShape is emitted when a ProbeSpec has no recognised shape
// (no http, no tcp, no exec). Should never occur after schema validation;
// defensive at synthesis time.
var ErrTranslationProbeShape = errors.New("TRANSLATION_ERROR: probe declares no shape (http/tcp/exec)")

// ErrTranslationConfigMapMissingArtifact is emitted when a configmap volume
// is declared but the Recipe's artifacts input emits no underlying file.
// Compose has no ConfigMap primitive — a configmap volume becomes a
// bind-mount from a generated file path; the file MUST exist.
//
// Track 1 cannot detect this (the artifacts input kind's emission runs in a
// separate sub-pass); the sentinel is declared here so the synthesizer can
// wire it once artifacts-input awareness lands. See the configmap-volume
// comment in workload_synth.go.
var ErrTranslationConfigMapMissingArtifact = errors.New("TRANSLATION_ERROR: configmap volume requires a file emitted via the Recipe's artifacts: input; none found")

// ErrUnsupportedComposeForm is emitted when a Workload declares a controller
// form compose cannot express. compose renders stateless and stateful
// workloads as long-running services; daemon (one pod per node), scheduled
// (cron-driven), and task (run-to-completion) have no clean single-host
// compose mapping, so they halt loudly rather than degrade into a plain
// service that misrepresents the intent.
var ErrUnsupportedComposeForm = errors.New("TRANSLATION_ERROR: compose cannot express this workload form")

// ErrUnsupportedComposeLifecycle is emitted when a container declares lifecycle
// hooks (postStart/preStop) on a compose target. compose has no lifecycle-hook
// primitive; a silently-unrun hook is a correctness surprise, so a declared
// lifecycle halts loudly rather than drop the hook.
var ErrUnsupportedComposeLifecycle = errors.New("TRANSLATION_ERROR: compose has no lifecycle-hook primitive; container declares lifecycle hooks")

// ErrUnsupportedComposeEnvFrom is emitted when a container declares envFrom
// (bulk env from a ConfigMap or Secret source) on a compose target. compose
// has no env-from-object primitive; a source reference is not a host file, so
// reinterpreting it as an env_file would silently change meaning. It halts
// loudly rather than degrade.
var ErrUnsupportedComposeEnvFrom = errors.New("TRANSLATION_ERROR: compose has no env-from-object primitive; container declares envFrom")

// ErrUnconvertibleComposeQuantity is emitted when a container resources quantity
// (cpu or memory) cannot be converted to the compose deploy.resources form
// without loss — for example a fractional memory quantity or an unrecognized
// suffix. It halts loudly rather than round or emit a wrong value.
var ErrUnconvertibleComposeQuantity = errors.New("TRANSLATION_ERROR: resources quantity cannot be converted to a compose deploy.resources value")
