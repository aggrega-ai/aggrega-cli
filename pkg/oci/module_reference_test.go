// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package oci

import (
	"strings"
	"testing"
)

func TestRepositoryOnly(t *testing.T) {
	t.Parallel()
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, url := range []string{
		"oci://mcr.microsoft.com/bicep/avm/res/app/container-app",
		"oci://localhost:5000/modules/net",
		"oci://127.0.0.1:5055/m",
		"oci://myacr.azurecr.io:443/bicep/x",
	} {
		if err := RepositoryOnly(url); err != nil {
			t.Errorf("RepositoryOnly(%q) = %v; want nil", url, err)
		}
	}
	for url, fault := range map[string]string{
		"oci://r/m:1.0.0":         `carries the tag "1.0.0"`,
		"oci://r/m@" + digest:     "carries the digest",
		"oci://r/m:":              "carries an empty tag",
		"oci://r/m@":              "carries an empty digest",
		"oci://r/m:tag@" + digest: `carries the digest "` + digest + `"`,
		"oci://r/m:1.0.0/x":       `carries the tag "1.0.0/x"`,
		"oci://r/m?tag=v1":        "carries a query",
		"oci://r/m#frag":          "carries a fragment",
		"oci://r/m//sub":          "carries an empty path segment",
		"oci://r":                 "names no repository",
		"oci://r/":                "names no repository",
		"oci://r/Modules/x":       `names an invalid repository "Modules/x"`,
		"oci://user:s3cr3tpw@myacr.azurecr.io/bicep/x": "names an invalid registry",
		"https://r/m": "is not an oci:// reference",
		"":            "is empty",
	} {
		err := RepositoryOnly(url)
		if err == nil || !strings.Contains(err.Error(), fault) {
			t.Errorf("RepositoryOnly(%q) = %v; want an error containing %q", url, err, fault)
		}
		if err != nil && strings.Contains(err.Error(), "s3cr3tpw") {
			t.Errorf("RepositoryOnly(%q) repeats the credential: %v", url, err)
		}
	}
}

func TestTagOrDigest(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"0.23.0", "v1.2.0", "latest", "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		if err := TagOrDigest(v); err != nil {
			t.Errorf("TagOrDigest(%q) = %v; want nil", v, err)
		}
	}
	for v, fault := range map[string]string{
		"":    "is empty",
		"a/b": "neither a tag nor a sha256 digest",
		"-x":  "neither a tag nor a sha256 digest",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": "neither a tag nor a sha256 digest",
		"sha256:abc": "malformed digest",
		"sha256:" + strings.ToUpper("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"):                                           "malformed digest",
		"sha512:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": "neither a tag nor a sha256 digest",
	} {
		err := TagOrDigest(v)
		if err == nil || !strings.Contains(err.Error(), fault) {
			t.Errorf("TagOrDigest(%q) = %v; want an error containing %q", v, err, fault)
		}
	}
}
