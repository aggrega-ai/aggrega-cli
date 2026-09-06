// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginfetchverify

import (
	"net/http"
	"strings"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
)

// RemoteTargetFor resolves a real remote OCI repository for an artifact's
// registry, returning the manifest digest as the subject reference to discover
// signature referrers against — the manifest the referrer is attached to, which
// the signature also commits to. It serves both axes: a catalog-entity URI
// (host/path, no scheme) and a content-pinned .wasm artifact URI
// (oci://host/path@sha256:...). Authentication delegates to the shared OCI
// credential mechanisms, which resolve an identity from a supplied or ambient
// source and never ask a person for one.
//
// It lives beside the verification home rather than in either caller, because
// every caller that verifies also has to say where referrers are discovered, and
// a second construction of that target would be free to address a different
// location than the one the artifact was fetched from.
//
// httpClient binds the network-egress gate to the resolution: the client is nil
// for an offline workspace, in which case a referrer fetch is refused loudly with
// ErrOfflineEgress. An explicitly requested verify never silently skips the
// network step when offline — it refuses, so a pinned-trust-root offline verify
// (which never reaches this fetch) stays the only offline-capable verify path.
func RemoteTargetFor(httpClient *http.Client) pullverify.TargetFor {
	return func(e pullverify.Entity) (oras.ReadOnlyTarget, string, error) {
		if httpClient == nil {
			return nil, "", egress.ErrOfflineEgress()
		}
		// A catalog entity's URI is a host-less bare identity, so its repository
		// reference is the location bound at resolution time (host + path), carried
		// on the entity. A host-carrying artifact (a content-pinned .wasm plugin)
		// leaves Location empty and supplies its reference through the URI.
		repoRef := e.Location
		if repoRef == "" {
			// A content-pinned .wasm artifact URI carries the oci:// scheme; the
			// remote-repository parser wants a bare host/path reference, so strip it.
			repoRef = strings.TrimPrefix(e.URI, "oci://")
			// Strip any version or content-digest suffix from the URI to form the
			// repository reference; the subject ref is the manifest digest the
			// signature commits to.
			if at := strings.LastIndex(repoRef, "@"); at >= 0 {
				repoRef = repoRef[:at]
			}
		}
		repo, err := oci.NewRemoteRepository(repoRef, httpClient)
		if err != nil {
			return nil, "", err
		}
		return repo, e.ManifestDigest, nil
	}
}
