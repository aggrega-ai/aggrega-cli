// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package egress

import (
	"fmt"
	"net/http"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// ErrOfflineEgress is returned by every egress constructor when the resolved
// workspace configuration is offline. It is refused at construction, before any
// network dial, so an offline operation fails early with a clear message rather
// than deep inside a transport with an opaque network error. The message carries
// its own remediation so callers that surface it need add none.
//
// It belongs to the fetch-error family: the source-resolution error classifier
// routes it into the same category as a remote-acquisition fault, since an
// offline refusal is, from the user's perspective, an inability to fetch.
func ErrOfflineEgress() error { return errOfflineEgress }

// The configuration key is spelled here as a literal rather than through the
// constant that names it, and that is required rather than preferred: the
// generator which publishes this sentence reads it from the declaration's own
// source text and deliberately refuses a value naming a constant, because
// evaluating the package is the thing that scan exists to avoid — and a
// declaration whose text is not in the declaration is one a reader of the
// source cannot see either. So the duplication is the mechanism, and a package
// guard compares the two rather than leaving them to agree by eye. Do not
// "single-source" it back: the generator refuses the build.
//
// The remediation is in the sentence rather than supplied per raise site
// because every path that reaches this condition offers the same three ways
// out, and the one clause that is not universally applicable says so with its
// own condition attached.
var errOfflineEgress = cerrors.Register("offline-egress", cerrors.Meta{
	ShortCode: "E0125",
	Message: "egress: network access refused because the workspace is offline; remediation: pass " +
		"`--offline=false` to allow the network for this one run, or clear the top-level `offline` key in " +
		"the configuration file (or AGGREGA_OFFLINE) to allow it from now on; where the work is a build " +
		"that needs content the machine does not have, run `aggrega pull --release <release>` from a " +
		"connected environment first to populate the cache",
	RulePlace: cerrors.NotARule("Network access was refused because the resolved workspace is offline, at the point an egress " +
		"is constructed and before any dial. It reports an operation this workspace declines to " +
		"perform rather than a rule refusing anything an author wrote."),
})

// RefuseIfOffline returns the offline refusal for an operation whose own work is
// a network round trip, or nil when the workspace is not offline.
//
// It is the pre-flight form of the decision RoundTripper makes, for an operation
// that would otherwise do real work — bundle an entity, load a catalog, walk a
// registry — before reaching a transport and being refused there. Both read the
// offline field off the resolved configuration and neither takes a boolean from
// a caller, so the two can never disagree about whether a workspace is offline;
// what taking it early adds is the two facts a refusal at the transport cannot
// carry: what was refused, and which layer set the posture.
//
// It is deliberately NOT keyed on which command is running. An operation that
// has an offline-safe form — a dry run that prints what it would publish and
// constructs no target, a dependency fill that honours the pins it already has —
// simply does not call this, so the one form of a network command that is
// legitimately offline keeps working. Keying on the command name instead would
// refuse that form too, and would refuse it for a reason that is not true of it.
//
// act names the refused work in the words a reader would use for it, as a clause
// that reads on from the command's own name. It never names a Go symbol: whoever
// reads this typed a command, not a call.
func RefuseIfOffline(rw *workspaceconfig.ResolvedWorkspace, act string) error {
	if rw == nil || !rw.Config().Offline {
		return nil
	}
	return fmt.Errorf("%s, and %s: %w", act, offlinePostureSource(rw), ErrOfflineEgress())
}

// offlinePostureSource names the layer that set the offline posture, in that
// layer's own vocabulary: the flag as a reader types it, the environment
// variable, or the configuration key and the file carrying it.
//
// The attribution is the resolved workspace's own per-value provenance rather
// than a second reading of the cascade, so this refusal and the configuration
// report can never name different layers for one setting. The reason a reader
// needs it is narrow and real: under this ruling a posture committed to a shared
// project file refuses a publication for everyone on that project, and somebody
// who typed no flag and exported no variable has otherwise nothing to look at.
//
// Where the provenance cannot be produced — a contributing file that has become
// unreadable since resolution — the refusal still states the posture and says
// only that the configuration carries it. Failing to attribute a setting must
// not turn a refusal into a different failure, which would replace an
// actionable message with one about the machinery behind it.
func offlinePostureSource(rw *workspaceconfig.ResolvedWorkspace) string {
	const unattributed = "the resolved configuration sets the offline posture"
	origins, err := rw.ValueOrigins()
	if err != nil {
		return unattributed
	}
	for _, o := range origins {
		if o.Field != workspaceconfig.OfflineSettingKey {
			continue
		}
		switch kind, name := o.Layer(); kind {
		case workspaceconfig.OriginCommandLineFlag:
			return "the offline posture is set by " + name + " on this invocation"
		case workspaceconfig.OriginEnvironment:
			return "the offline posture is set by " + workspaceconfig.EnvironmentVariableWords(name)
		case workspaceconfig.OriginConfigFile:
			return "the offline posture is set by `" + workspaceconfig.OfflineSettingKey + ": true` in " + name
		}
	}
	return unattributed
}

// RoundTripper returns the HTTP transport for network egress, derived from the
// resolved workspace configuration. When the configuration is offline it returns
// (nil, ErrOfflineEgress) before any dial; otherwise it returns the shared
// default transport, with every request held to the wait on its server's answer
// (AnswerWait), so no request through it waits on a server without end. It is the
// transport source the OCI package threads into its repository and registry
// construction.
//
// It is also how a caller consults the offline decision without building a
// client it does not yet need — a pre-flight that has to know whether a later
// step could reach the network at all asks here and matches the returned error,
// which keeps that pre-flight and the fetch it guards reading one decision from
// one place instead of two readings that can drift apart.
func RoundTripper(cfg workspaceconfig.Config) (http.RoundTripper, error) {
	if cfg.Offline {
		return nil, ErrOfflineEgress()
	}
	return answerWaitTransport{next: http.DefaultTransport}, nil
}

// HTTPClient returns an HTTP client for network egress, derived from the resolved
// workspace configuration. When the configuration is offline it returns
// (nil, ErrOfflineEgress) before any dial. Its transport holds every request to
// the wait on its server's answer (AnswerWait), and the client itself carries no
// overall timeout, since a large transfer that keeps arriving is not waiting; a
// caller with a deadline of its own for the whole fetch sets Timeout on the
// returned client.
func HTTPClient(cfg workspaceconfig.Config) (*http.Client, error) {
	rt, err := RoundTripper(cfg)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: rt}, nil
}
