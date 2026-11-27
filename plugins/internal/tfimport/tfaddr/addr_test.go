// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package tfaddr

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		source string
		want   Address
		key    string // Registry only: the Key the address reads as
	}{
		{source: "./modules/db", want: Address{Kind: Local, Raw: "./modules/db"}},
		{source: "../../modules/db", want: Address{Kind: Local, Raw: "../../modules/db"}},
		{
			source: "terraform-aws-modules/rds/aws",
			want: Address{Kind: Registry, Raw: "terraform-aws-modules/rds/aws",
				Namespace: "terraform-aws-modules", Name: "rds", Provider: "aws"},
			key: "terraform-aws-modules/rds/aws",
		},
		{
			source: "registry.terraform.io/terraform-aws-modules/rds/aws",
			want: Address{Kind: Registry, Raw: "registry.terraform.io/terraform-aws-modules/rds/aws",
				Host: "registry.terraform.io", Namespace: "terraform-aws-modules", Name: "rds", Provider: "aws"},
			key: "terraform-aws-modules/rds/aws",
		},
		{
			source: "registry.opentofu.org/terraform-aws-modules/rds/aws",
			want: Address{Kind: Registry, Raw: "registry.opentofu.org/terraform-aws-modules/rds/aws",
				Host: "registry.opentofu.org", Namespace: "terraform-aws-modules", Name: "rds", Provider: "aws"},
			key: "terraform-aws-modules/rds/aws",
		},
		{
			source: "terraform-google-modules/sql-db/google//modules/postgresql",
			want: Address{Kind: Registry, Raw: "terraform-google-modules/sql-db/google//modules/postgresql",
				Namespace: "terraform-google-modules", Name: "sql-db", Provider: "google", Subdir: "modules/postgresql"},
			key: "terraform-google-modules/sql-db/google//modules/postgresql",
		},
		{
			source: "GoogleCloudPlatform/sql-db/google",
			want: Address{Kind: Registry, Raw: "GoogleCloudPlatform/sql-db/google",
				Namespace: "terraform-google-modules", Name: "sql-db", Provider: "google"},
			key: "terraform-google-modules/sql-db/google",
		},
		{
			source: "app.terraform.io/acme/vpc/aws",
			want: Address{Kind: Private, Raw: "app.terraform.io/acme/vpc/aws",
				Host: "app.terraform.io", Namespace: "acme", Name: "vpc", Provider: "aws"},
		},
		{
			source: "git::https://example.com/x.git//sub?ref=v1.2.0",
			want: Address{Kind: Git, Raw: "git::https://example.com/x.git//sub?ref=v1.2.0",
				URL: "https://example.com/x.git", Subdir: "sub", Ref: "v1.2.0"},
		},
		{
			source: "git::file:///tmp/x.git?ref=v1",
			want: Address{Kind: Git, Raw: "git::file:///tmp/x.git?ref=v1",
				URL: "file:///tmp/x.git", Ref: "v1"},
		},
		{
			source: "github.com/acme/terraform-aws-x",
			want: Address{Kind: Git, Raw: "github.com/acme/terraform-aws-x",
				URL: "https://github.com/acme/terraform-aws-x.git"},
		},
		{
			source: "oci://registry.example.com/modules/net?tag=1.2.0",
			want: Address{Kind: OCI, Raw: "oci://registry.example.com/modules/net?tag=1.2.0",
				URL: "oci://registry.example.com/modules/net", Ref: "1.2.0"},
		},
		{
			source: "oci://registry.example.com/modules/net?digest=sha256:abc",
			want: Address{Kind: OCI, Raw: "oci://registry.example.com/modules/net?digest=sha256:abc",
				URL: "oci://registry.example.com/modules/net", Ref: "sha256:abc"},
		},
		{source: "s3::https://s3.amazonaws.com/bucket/x.zip", want: Address{Kind: Other, Raw: "s3::https://s3.amazonaws.com/bucket/x.zip"}},
		{source: "gcs::https://www.googleapis.com/storage/v1/bucket/x.zip", want: Address{Kind: Other, Raw: "gcs::https://www.googleapis.com/storage/v1/bucket/x.zip"}},
		{source: "hg::https://example.com/x", want: Address{Kind: Other, Raw: "hg::https://example.com/x"}},
		{source: "https://example.com/x.zip", want: Address{Kind: Other, Raw: "https://example.com/x.zip"}},
	}
	for _, tc := range cases {
		got := Parse(tc.source)
		if got != tc.want {
			t.Errorf("Parse(%q) = %+v, want %+v", tc.source, got, tc.want)
		}
		if tc.key != "" && got.Key() != tc.key {
			t.Errorf("Parse(%q).Key() = %q, want %q", tc.source, got.Key(), tc.key)
		}
	}
}

func TestFromGitURL(t *testing.T) {
	positive := []struct {
		url, subdir, key string
	}{
		{"https://github.com/terraform-aws-modules/terraform-aws-rds", "", "terraform-aws-modules/rds/aws"},
		{"https://github.com/terraform-aws-modules/terraform-aws-rds.git", "", "terraform-aws-modules/rds/aws"},
		{"https://github.com/terraform-google-modules/terraform-google-sql-db", "modules/mysql", "terraform-google-modules/sql-db/google//modules/mysql"},
		{"https://github.com/Azure/terraform-azurerm-avm-res-dbforpostgresql-flexibleserver", "", "Azure/avm-res-dbforpostgresql-flexibleserver/azurerm"},
		{"https://github.com/GoogleCloudPlatform/terraform-google-alloy-db", "", "GoogleCloudPlatform/alloy-db/google"},
	}
	for _, tc := range positive {
		got, ok := FromGitURL(tc.url, tc.subdir)
		if !ok || got != tc.key {
			t.Errorf("FromGitURL(%q, %q) = %q, %v; want %q, true", tc.url, tc.subdir, got, ok, tc.key)
		}
	}

	negative := []struct {
		name, url, subdir string
	}{
		{"another host", "https://gitlab.com/terraform-aws-modules/terraform-aws-rds", ""},
		{"repository not named for a provider and name", "https://github.com/acme/rds-module", ""},
		{"repository with no module name", "https://github.com/acme/terraform-aws", ""},
	}
	for _, tc := range negative {
		if key, ok := FromGitURL(tc.url, tc.subdir); ok {
			t.Errorf("%s: FromGitURL(%q, %q) = %q, true; want false", tc.name, tc.url, tc.subdir, key)
		}
	}

	// The key keeps the repository's own namespace: no alias names this module.
	addr := Parse("GoogleCloudPlatform/alloy-db/google")
	if addr.Namespace != "GoogleCloudPlatform" || addr.Name != "alloy-db" {
		t.Errorf("Parse(alloy-db key) namespace and name = %q, %q; want GoogleCloudPlatform, alloy-db", addr.Namespace, addr.Name)
	}
}

func TestGitURLRoundTrips(t *testing.T) {
	cases := []struct {
		url, subdir, want string
	}{
		{"https://github.com/terraform-aws-modules/terraform-aws-rds", "", "https://github.com/terraform-aws-modules/terraform-aws-rds.git"},
		{"https://github.com/terraform-google-modules/terraform-google-sql-db", "modules/mysql", "https://github.com/terraform-google-modules/terraform-google-sql-db.git"},
		{"https://github.com/Azure/terraform-azurerm-avm-res-dbforpostgresql-flexibleserver", "", "https://github.com/Azure/terraform-azurerm-avm-res-dbforpostgresql-flexibleserver.git"},
		{"https://github.com/GoogleCloudPlatform/terraform-google-alloy-db", "", "https://github.com/GoogleCloudPlatform/terraform-google-alloy-db.git"},
	}
	for _, tc := range cases {
		key, ok := FromGitURL(tc.url, tc.subdir)
		if !ok {
			t.Fatalf("FromGitURL(%q, %q) = false; want a key", tc.url, tc.subdir)
		}
		got, ok := GitURL(Parse(key))
		if !ok || got != tc.want {
			t.Errorf("GitURL(Parse(%q)) = %q, %v; want %q, true", key, got, ok, tc.want)
		}
	}

	aliased, ok := GitURL(Parse("GoogleCloudPlatform/sql-db/google"))
	if want := "https://github.com/terraform-google-modules/terraform-google-sql-db.git"; !ok || aliased != want {
		t.Errorf("GitURL(alias address) = %q, %v; want %q, true", aliased, ok, want)
	}

	for _, source := range []string{"./modules/db", "app.terraform.io/acme/vpc/aws", "git::https://example.com/x.git"} {
		if got, ok := GitURL(Parse(source)); ok {
			t.Errorf("GitURL(Parse(%q)) = %q, true; want false: only public registry addresses have a repository", source, got)
		}
	}
}
