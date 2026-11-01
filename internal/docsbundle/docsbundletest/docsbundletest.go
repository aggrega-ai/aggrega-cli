// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package docsbundletest builds documentation bundles and serves them from an
// in-process registry, for tests that drive a documentation surface against
// content they control.
//
// The registry is an in-memory OCI store that also lists its tags, so a reader
// that chooses among the published versions is exercised as well as one that
// reads an exact version. A bundle is published unsigned, or signed with a held
// key whose public half a test pins in the workspace's trust policy — the same
// signature a publication attaches, verified through the same pull-time check.
package docsbundletest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// Member is one entry of a bundle's archive.
type Member struct {
	// Name is the entry's name as the archive carries it.
	Name string
	// Body is a regular file's content.
	Body string
	// Typeflag is the entry's type; zero is a regular file.
	Typeflag byte
	// Linkname is where a link entry points.
	Linkname string
	// Mode is the entry's permission bits; zero is 0644 for a file and 0755 for
	// a directory.
	Mode int64
}

// Bundle packs members, in the order given, into the gzipped tar a documentation
// bundle is published as.
func Bundle(t testing.TB, members []Member) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, m := range members {
		hdr := &tar.Header{Name: m.Name, Typeflag: m.Typeflag, Linkname: m.Linkname, Mode: m.Mode}
		if hdr.Typeflag == 0 {
			hdr.Typeflag = tar.TypeReg
		}
		if hdr.Typeflag == tar.TypeReg {
			hdr.Size = int64(len(m.Body))
		}
		if hdr.Mode == 0 {
			hdr.Mode = 0o644
			if hdr.Typeflag == tar.TypeDir {
				hdr.Mode = 0o755
			}
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header %q: %v", m.Name, err)
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(m.Body)); err != nil {
				t.Fatalf("tar write %q: %v", m.Name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// Registry is an in-memory registry that lists the tags published to it.
type Registry struct {
	*memory.Store
	tags []string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{Store: memory.New()}
}

// Tags lists every tag published, in publication order.
func (r *Registry) Tags(_ context.Context, _ string, fn func(tags []string) error) error {
	return fn(slices.Clone(r.tags))
}

// Signer signs what it publishes with a held key.
type Signer struct {
	signer *signing.KeyedSigner
}

// NewSigner returns a signer holding a fresh key.
func NewSigner(t testing.TB) *Signer {
	t.Helper()
	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("generate a key: %v", err)
	}
	s, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("load the key: %v", err)
	}
	return &Signer{signer: s}
}

// PublicKeyPEM returns the public half of the signer's key, for a trust policy to
// pin.
func (s *Signer) PublicKeyPEM() []byte {
	return s.signer.PublicKeyPEM()
}

// ExampleReadme is the page of the example ExampleMembers carries as demo: a
// header, a shell block before the Build section that is not a next command, and
// the Build section's own block.
const ExampleReadme = "---\nexample:\n  level: start\n---\n\n# Demo\n\nA demo example.\n\n" +
	"```sh\nnot a next command\n```\n\n## Build\n\n```sh\n# Compose\naggrega build --release release-compose.yaml --auto-pull\n```\n\n" +
	"## Notes\n"

// ExampleNext is the Build section's block of ExampleReadme, line by line: the
// next commands an example written from it reports.
var ExampleNext = []string{"# Compose", "aggrega build --release release-compose.yaml --auto-pull"}

// ExampleFiles are the files of the demo example ExampleMembers carries, by name
// relative to the example's directory, each with its content.
var ExampleFiles = map[string]string{
	"README.md":            ExampleReadme,
	"aggregate.yaml":       "kind: Aggregate\n",
	"release-compose.yaml": "kind: Release\n",
}

// ExampleMembers is a documentation bundle carrying the list of examples — which
// names demo twice, under a product and under a capability, as the published list
// does — and two examples, demo and other, with extra appended after them.
func ExampleMembers(extra ...Member) []Member {
	return append([]Member{
		{Name: "language-reference/examples/by-product-and-capability.md", Body: "# Examples by product and capability\n\n" +
			"## By product\n\n### `core/x`\n\n" +
			"- [demo](demo/README.md) (`start`) — A demo example.\n" +
			"- [other](other/README.md) (`intermediate`) — Another example.\n\n" +
			"## By capability\n\n### `credentials`\n\n" +
			"- [demo](demo/README.md) (`start`) — A demo example.\n"},
		{Name: "language-reference/examples/demo/README.md", Body: ExampleReadme},
		{Name: "language-reference/examples/demo/aggregate.yaml", Body: ExampleFiles["aggregate.yaml"]},
		{Name: "language-reference/examples/demo/release-compose.yaml", Body: ExampleFiles["release-compose.yaml"]},
		{Name: "language-reference/examples/other/README.md", Body: "# Other\n\nAnother example.\n"},
	}, extra...)
}

// WriteTrustingWorkspace writes into dir the configuration of a workspace whose
// default registry is host and whose policy for it requires a signature verifying
// under trusted's key.
func WriteTrustingWorkspace(t testing.TB, dir, host string, trusted *Signer) {
	t.Helper()
	var key strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(string(trusted.PublicKeyPEM())), "\n") {
		key.WriteString("      " + line + "\n")
	}
	config := "default_registry: " + host + "\nworkspace: {}\nregistries:\n" +
		"  - host: " + host + "\n" +
		"    signing:\n      required: true\n" +
		"    public_key: |\n" + key.String()
	if err := os.WriteFile(filepath.Join(dir, workspaceconfig.ConfigFilename), []byte(config), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
}

// Publish publishes bundle as the documentation bundle at version and, where by
// is not nil, attaches its signature over the published manifest. Bundles
// published at two versions must differ in content.
func (r *Registry) Publish(t testing.TB, version string, bundle []byte, by *Signer) {
	t.Helper()
	ctx := context.Background()
	dg, err := oci.Push(ctx, r.Store, version, oci.MediaTypeDocs, bundle, nil)
	if err != nil {
		t.Fatalf("publish %s: %v", version, err)
	}
	r.tags = append(r.tags, version)
	if by == nil {
		return
	}
	sig, artifactType, err := by.signer.Sign(ctx, dg)
	if err != nil {
		t.Fatalf("sign %s: %v", version, err)
	}
	if _, err := oci.AttachReferrer(ctx, r.Store, version, artifactType, sig); err != nil {
		t.Fatalf("attach the signature to %s: %v", version, err)
	}
}
