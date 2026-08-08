// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package egress

import (
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

var errOfflineEgress = cerrors.Register("egress/offline-egress", cerrors.Meta{
	Message: "egress: network access refused because the workspace is offline; remediation: unset workspace.offline (or AGGREGA_OFFLINE) or run `aggrega pull --release <release>` from a connected environment to populate the cache",
})

// RoundTripper returns the HTTP transport for network egress, derived from the
// resolved workspace configuration. When the configuration is offline it returns
// (nil, ErrOfflineEgress) before any dial; otherwise it returns the shared
// default transport. It is the transport source the OCI package threads into its
// repository and registry construction.
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
	return http.DefaultTransport, nil
}

// HTTPClient returns an HTTP client for network egress, derived from the resolved
// workspace configuration. When the configuration is offline it returns
// (nil, ErrOfflineEgress) before any dial. The returned client carries no
// timeout; a caller with a fetch deadline sets Timeout on the returned client.
func HTTPClient(cfg workspaceconfig.Config) (*http.Client, error) {
	rt, err := RoundTripper(cfg)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: rt}, nil
}
