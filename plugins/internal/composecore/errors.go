// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"

// ErrTranslationProbeNoPort is emitted when a probe http: shape resolves to a
// container that declares no ports — compose healthchecks need a port to
// build the curl URL.
func ErrTranslationProbeNoPort() error { return errTranslationProbeNoPort }

var errTranslationProbeNoPort = cerrors.Register("compiler-renderer-compose/translation-probe-no-port", cerrors.Meta{
	ShortCode: "E0236",
	Message:   "TRANSLATION_ERROR: probe http: requires at least one container port; container declares none",
})

// ErrTranslationProbeShape is emitted when a ProbeSpec has no recognised shape
// (no http, no tcp, no exec). Should never occur after schema validation;
// defensive at synthesis time.
func ErrTranslationProbeShape() error { return errTranslationProbeShape }

var errTranslationProbeShape = cerrors.Register("compiler-renderer-compose/translation-probe-shape", cerrors.Meta{
	ShortCode: "E0237",
	Message:   "TRANSLATION_ERROR: probe declares no shape (http/tcp/exec)",
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
func ErrTranslationConfigMapMissingArtifact() error { return errTranslationConfigMapMissingArtifact }

var errTranslationConfigMapMissingArtifact = cerrors.Register("compiler-renderer-compose/translation-config-map-missing-artifact", cerrors.Meta{
	ShortCode: "E0235",
	Message:   "TRANSLATION_ERROR: configmap volume requires a file emitted via the Recipe's artifacts: input; none found",
})

// ErrUnsupportedComposeForm is emitted when a Workload declares a controller
// form compose cannot express. compose renders stateless and stateful
// workloads as long-running services; daemon (one pod per node), scheduled
// (cron-driven), and task (run-to-completion) have no clean single-host
// compose mapping, so they halt loudly rather than degrade into a plain
// service that misrepresents the intent.
func ErrUnsupportedComposeForm() error { return errUnsupportedComposeForm }

var errUnsupportedComposeForm = cerrors.Register("compiler-renderer-compose/unsupported-compose-form", cerrors.Meta{
	ShortCode: "E0240",
	Message:   "TRANSLATION_ERROR: compose cannot express this workload form",
})

// ErrUnsupportedComposeLifecycle is emitted when a container declares lifecycle
// hooks (postStart/preStop) on a compose target. compose has no lifecycle-hook
// primitive; a silently-unrun hook is a correctness surprise, so a declared
// lifecycle halts loudly rather than drop the hook.
func ErrUnsupportedComposeLifecycle() error { return errUnsupportedComposeLifecycle }

var errUnsupportedComposeLifecycle = cerrors.Register("compiler-renderer-compose/unsupported-compose-lifecycle", cerrors.Meta{
	ShortCode: "E0241",
	Message:   "TRANSLATION_ERROR: compose has no lifecycle-hook primitive; container declares lifecycle hooks",
})

// ErrUnsupportedComposeEnvFrom is emitted when a container declares envFrom
// (bulk env from a ConfigMap or Secret source) on a compose target. compose
// has no env-from-object primitive; a source reference is not a host file, so
// reinterpreting it as an env_file would silently change meaning. It halts
// loudly rather than degrade.
func ErrUnsupportedComposeEnvFrom() error { return errUnsupportedComposeEnvFrom }

var errUnsupportedComposeEnvFrom = cerrors.Register("compiler-renderer-compose/unsupported-compose-env-from", cerrors.Meta{
	ShortCode: "E0239",
	Message:   "TRANSLATION_ERROR: compose has no env-from-object primitive; container declares envFrom",
})

// ErrUnsupportedComposePortProtocol is emitted when a published port declares a
// transport protocol compose's port syntax cannot express. A compose published
// port carries its transport as a `/protocol` suffix whose vocabulary is tcp and
// udp; a workload port declaring anything else has no compose spelling.
//
// It halts rather than dropping the suffix, because a published port that
// silently falls back to the default transport comes up carrying traffic its
// clients cannot reach — the failure this whole surface exists to make
// declarable, reintroduced at the point of translation.
func ErrUnsupportedComposePortProtocol() error { return errUnsupportedComposePortProtocol }

var errUnsupportedComposePortProtocol = cerrors.Register("compiler-renderer-compose/unsupported-compose-port-protocol", cerrors.Meta{
	ShortCode: "E0258",
	Message:   "TRANSLATION_ERROR: compose publishes a port's transport as a tcp or udp suffix; this port declares a transport compose cannot express",
})

// ErrUnconvertibleComposeQuantity is emitted when a container resources quantity
// (cpu or memory) cannot be converted to the compose deploy.resources form
// without loss — for example a fractional memory quantity or an unrecognized
// suffix. It halts loudly rather than round or emit a wrong value.
func ErrUnconvertibleComposeQuantity() error { return errUnconvertibleComposeQuantity }

var errUnconvertibleComposeQuantity = cerrors.Register("compiler-renderer-compose/unconvertible-compose-quantity", cerrors.Meta{
	ShortCode: "E0238",
	Message:   "TRANSLATION_ERROR: resources quantity cannot be converted to a compose deploy.resources value",
})
