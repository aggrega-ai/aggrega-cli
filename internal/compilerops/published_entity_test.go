// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compilerops

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/hometest"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestSplitRegistryReference pins the one reading of a registry reference every
// surface routes a published-entity read through: the repository the client is
// built for, and the tag or digest resolved within it.
//
// The host:port rows are the ones a naive split gets wrong — a colon in the host
// is not a version separator — and the refusals are every shape that names no
// single coordinate, so none of them is read at a version the reader chose.
func TestSplitRegistryReference(t *testing.T) {
	t.Parallel()
	accepted := []struct {
		reference, repository, target string
	}{
		{"oci://hub.aggrega.ai/component/core/compute/api-server:1.0.0", "hub.aggrega.ai/component/core/compute/api-server", "1.0.0"},
		{"oci://localhost:5000/recipe/helm/svc:2.1.0", "localhost:5000/recipe/helm/svc", "2.1.0"},
		{"oci://127.0.0.1:5000/plugin/parser/json:0.6.1-35-g33605f9f-dirty", "127.0.0.1:5000/plugin/parser/json", "0.6.1-35-g33605f9f-dirty"},
		{"oci://hub.aggrega.ai/component/core/x@sha256:abc", "hub.aggrega.ai/component/core/x", "sha256:abc"},
		{"oci://localhost:5000/component/core/x@sha256:abc", "localhost:5000/component/core/x", "sha256:abc"},
	}
	for _, row := range accepted {
		repository, target, err := splitRegistryReference(row.reference)
		if err != nil {
			t.Errorf("%s: refused: %v", row.reference, err)
			continue
		}
		if repository != row.repository || target != row.target {
			t.Errorf("%s: split into (%q, %q); want (%q, %q)", row.reference, repository, target, row.repository, row.target)
		}
	}
	for _, reference := range []string{
		"oci://hub.aggrega.ai",
		"oci://localhost:5000",
		"oci://hub.aggrega.ai/component/core/x",
		"oci://hub.aggrega.ai/component/core/x:",
		"oci://hub.aggrega.ai/component/core/x@",
		"oci://hub.aggrega.ai/:1.0.0",
		"oci:///component/core/x:1.0.0",
	} {
		if _, _, err := splitRegistryReference(reference); err == nil || !strings.Contains(err.Error(), "is not a registry reference") {
			t.Errorf("%s: err = %v; want the refusal naming the form to write", reference, err)
		}
	}
}

// TestPublishedHeaderRefusesADocumentThatIsNotAnEntity pins the refusal the
// reading makes after unwrapping: bytes that are a document but not an entity of
// a published kind are named as such rather than rendered. The Component row is
// the positive control, and the Release row is the case a kind check keyed on
// "declares any kind" would admit.
func TestPublishedHeaderRefusesADocumentThatIsNotAnEntity(t *testing.T) {
	t.Parallel()
	header, err := publishedHeader([]byte("apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  name: core/test/svc\n  version: \"1.0.0\"\n  description: A service.\n"))
	if err != nil {
		t.Fatalf("control: a Component document must be read: %v", err)
	}
	if header.Kind != "Component" || header.Metadata.Name != "core/test/svc" || header.Metadata.Version != "1.0.0" || header.Metadata.Description != "A service." {
		t.Errorf("header = %+v; want the identity the document declares", header)
	}
	for name, document := range map[string]string{
		"prose":        "just some notes, not a document an entity is written in\n",
		"no kind":      "metadata:\n  name: x\n",
		"a Release":    "apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: rel\n",
		"another kind": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: gw\n",
	} {
		if _, err := publishedHeader([]byte(document)); !errors.Is(err, oci.ErrPublishedArtifactNotAnEntity()) {
			t.Errorf("%s: err = %v; want ErrPublishedArtifactNotAnEntity", name, err)
		}
	}
}

// TestReadPublishedEntityRefusesBeforeDialling pins the two refusals the reading
// makes ahead of the network, and their order. A malformed reference is refused
// for its shape even in an offline workspace, so a caller is told the form to
// write rather than that the network is off; a well-formed one in an offline
// workspace is refused at the egress gate. Both rows run offline, so neither can
// reach a registry whatever the reading does.
func TestReadPublishedEntityRefusesBeforeDialling(t *testing.T) {
	t.Parallel()
	offline := workspaceconfig.Config{Offline: true}

	_, err := ReadPublishedEntity(context.Background(), offline, "oci://hub.aggrega.ai/component/core/x")
	if err == nil || errors.Is(err, egress.ErrOfflineEgress()) || !strings.Contains(err.Error(), "is not a registry reference") {
		t.Errorf("malformed reference: err = %v; want the shape refusal, ahead of the egress gate", err)
	}

	_, err = ReadPublishedEntity(context.Background(), offline, "oci://hub.aggrega.ai/component/core/x:1.0.0")
	if !errors.Is(err, egress.ErrOfflineEgress()) {
		t.Errorf("offline workspace: err = %v; want ErrOfflineEgress", err)
	}
}

// TestRegistryReferenceHostIsTheHostTheReadContacts holds the host a surface
// admits a read by to the host the read then contacts. RegistryReferenceHost
// derives the host from the split reference; the registry client the reading
// builds parses the same repository for itself. A surface deciding on one and a
// client contacting the other is a surface that admits one registry and reaches
// another, so each row here is either refused by the client before anything is
// contacted, or addresses exactly the host RegistryReferenceHost reports.
//
// The adversarial rows are the shapes on which the two parsers could part:
// userinfo in front of a configured host, a second scheme, a scheme-like
// separator inside the path, a bracketed address, and a mixed-case host. The
// ordinary rows are the positive control — without them a client refusing
// everything would satisfy the property.
//
// Not parallel: it points the credential home at a scratch directory for its
// duration, because constructing a registry client reads that file.
func TestRegistryReferenceHostIsTheHostTheReadContacts(t *testing.T) {
	hometest.Isolate(t)
	constructed := 0
	for _, reference := range []string{
		"oci://hub.aggrega.ai/component/core/compute/api-server:1.0.0",
		"oci://127.0.0.1:5000/recipe/helm/svc:2.1.0",
		"oci://localhost:5000/component/core/x@sha256:" + strings.Repeat("a", 64),
		"oci://[::1]:5000/component/core/x:1.0.0",
		"oci://Registry.Example.com/component/core/x:1.0.0",
		"oci://evil.example.com@hub.aggrega.ai/component/core/x:1.0.0",
		"oci://hub.aggrega.ai@evil.example.com/component/core/x:1.0.0",
		"oci://evil.example.com://hub.aggrega.ai/component/core/x:1.0.0",
		"oci://https://hub.aggrega.ai/component/core/x:1.0.0",
		"oci://evil.example.com/a://hub.aggrega.ai/component/core/x:1.0.0",
		"oci://hub.aggrega.ai/component/core/x:tag@sha256:" + strings.Repeat("b", 64),
	} {
		host, err := RegistryReferenceHost(reference)
		if err != nil {
			// Refused as a reference, so neither parser is ever reached; nothing to hold.
			continue
		}
		repository, _, err := splitRegistryReference(reference)
		if err != nil {
			t.Fatalf("%s: RegistryReferenceHost accepted a reference the reading's split refuses: %v", reference, err)
		}
		repo, err := oci.NewRemoteRepository(repository, &http.Client{})
		if err != nil {
			// The client refuses the repository before anything is contacted, so no
			// host is reached whatever RegistryReferenceHost reported.
			continue
		}
		constructed++
		if repo.Reference.Registry != host {
			t.Errorf("%s: a surface would admit %q, and the read would contact %q", reference, host, repo.Reference.Registry)
		}
	}
	if constructed < 5 {
		t.Fatalf("only %d reference(s) reached a constructed client; the ordinary rows are the control, and without "+
			"them this holds nothing", constructed)
	}
}

// TestIsRegistryReference pins the shape test every surface routes by. An entity
// identity and a document path are never registry references, which is what lets
// one argument carry all three.
func TestIsRegistryReference(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]bool{
		"oci://hub.aggrega.ai/component/core/x:1.0.0": true,
		"core/compute/api-server":                     false,
		"core/compute/api-server:1.0.0":               false,
		"./catalog/component.yaml":                    false,
		"hub.aggrega.ai/component/core/x:1.0.0":       false,
	} {
		if got := IsRegistryReference(input); got != want {
			t.Errorf("IsRegistryReference(%q) = %v; want %v", input, got, want)
		}
	}
}
