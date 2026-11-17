// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package oci

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"oras.land/oras-go/v2/registry"
)

const ociScheme = "oci://"

var sha256Digest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// RepositoryOnly checks that url names one repository in a registry and nothing
// more: `oci://<registry>/<repository>`, the registry optionally carrying a
// port. A tag, a digest, a query, a fragment or a `//` after the scheme is
// refused, since the version of a module source is written apart from its url.
// The error names what the url carries.
func RepositoryOnly(url string) error {
	if url == "" {
		return errors.New("is empty")
	}
	rest, ok := strings.CutPrefix(url, ociScheme)
	if !ok {
		return errors.New("is not an oci:// reference")
	}
	if strings.Contains(rest, "?") {
		return errors.New("carries a query")
	}
	if strings.Contains(rest, "#") {
		return errors.New("carries a fragment")
	}
	if strings.Contains(rest, "//") {
		return errors.New("carries an empty path segment")
	}
	registryHost, repository, found := strings.Cut(rest, "/")
	if !found || registryHost == "" || repository == "" {
		return errors.New("names no repository")
	}
	ref, err := registry.ParseReference(rest)
	if err != nil {
		return fmt.Errorf("names an invalid registry or repository: %w", err)
	}
	if strings.Contains(rest, "@") {
		return fmt.Errorf("carries the digest %q", ref.Reference)
	}
	if ref.Reference != "" {
		return fmt.Errorf("carries the tag %q", ref.Reference)
	}
	return nil
}

// TagOrDigest checks that version is a tag in the OCI distribution grammar or a
// `sha256:` digest of 64 lowercase hex digits. The error names what was
// written.
func TagOrDigest(version string) error {
	if version == "" {
		return errors.New("is empty")
	}
	if strings.HasPrefix(version, "sha256:") {
		if !sha256Digest.MatchString(version) {
			return fmt.Errorf("is the malformed digest %q", version)
		}
		return nil
	}
	ref := registry.Reference{Registry: "r", Repository: "m", Reference: version}
	if err := ref.ValidateReferenceAsTag(); err != nil {
		return fmt.Errorf("is neither a tag nor a sha256 digest: %q", version)
	}
	return nil
}
