// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// reachSecondRegistry is the second destination the reach rows publish to. Like
// publishTestRegistry it is not a loopback address: every row injects its own
// target, so nothing here can reach it.
const reachSecondRegistry = "second.example.test"

// refusesDates is an in-memory store that refuses every upload of a publication
// date, the way a registry failing that write past its retries leaves a first
// publication: the content and its tag land, and the date beside them does not.
type refusesDates struct {
	*memory.Store
}

func (r *refusesDates) Push(ctx context.Context, desc ocispec.Descriptor, rd io.Reader) error {
	if desc.MediaType == oci.MediaTypePublishedAt {
		return errors.New("the test registry refuses this upload")
	}
	return r.Store.Push(ctx, desc, rd)
}

// reachWorkspace is a workspace whose two destinations each pin the public half
// of one held key, so a signature that key made verifies under either registry's
// trust policy — which is what lets a signing batch find a reference already
// signed — and whose signing block names that key, so every reference of a batch
// is signed with one credential. Nothing requires a signature, so a publication
// lands unsigned and the signing tool is what attaches one.
func reachWorkspace(t *testing.T) string {
	t.Helper()
	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "signing-key.pem")
	if err := os.WriteFile(keyPath, privPEM, 0o600); err != nil {
		t.Fatalf("write the signing key: %v", err)
	}
	var pin strings.Builder
	for _, line := range strings.Split(strings.TrimRight(string(pubPEM), "\n"), "\n") {
		pin.WriteString("      " + line + "\n")
	}
	return defaultRegistryWorkspace + "registries:\n" +
		"  - host: " + publishTestRegistry + "\n    public_key: |\n" + pin.String() +
		"  - host: " + reachSecondRegistry + "\n    public_key: |\n" + pin.String() +
		"signing:\n  key_path: " + keyPath + "\n"
}

// reachRow is one thing a publication call can do, and the reach its answer must
// state for it. Each row carries one difference from the row that precedes it.
type reachRow struct {
	name string
	// defect is what a wrong answer on this row would mean.
	defect string
	// run drives the call this row is about, setting up whatever it needs first,
	// and returns the reach the answer stated.
	run            func(t *testing.T, s *server, root string, store *memory.Store) publicationReach
	wantRegistries []string
	wantBeyond     bool
}

// TestThePublicationReachOnTheToolProtocolIsWhatTheCallAttached drives every
// publication tool through this surface's own argument projection into the
// operation its handler runs, against a registry this test holds, and holds each
// answer's reach to what the call actually attached: true exactly when it
// uploaded content or attached a signature, a publication date or a deprecation
// marker, and naming every registry the call addressed, each once, in the order
// it first addressed them.
//
// The dry run is driven through the tools/call dispatch itself, which is the
// whole of what a call can reach without a destination.
func TestThePublicationReachOnTheToolProtocolIsWhatTheCallAttached(t *testing.T) {
	entityRef := publishTestRegistry + "/component/core/api-server:1.0.0"

	rows := []reachRow{
		{
			name:           "a first publication",
			defect:         "a publication that uploaded its content does not say it reached beyond this machine",
			wantRegistries: []string{publishTestRegistry},
			wantBeyond:     true,
			run: func(t *testing.T, s *server, root string, store *memory.Store) publicationReach {
				return publishThroughTool(t, s, root, store).Reach
			},
		},
		{
			name:           "the same publication run again over a complete version",
			defect:         "a re-run that attached nothing claims a landing that did not happen",
			wantRegistries: []string{publishTestRegistry},
			run: func(t *testing.T, s *server, root string, store *memory.Store) publicationReach {
				publishThroughTool(t, s, root, store)
				again := publishThroughTool(t, s, root, store)
				if !again.AlreadyPublished {
					t.Fatalf("the second publication is not reported as already published: %+v", again)
				}
				return again.Reach
			},
		},
		{
			name:           "the same publication run again over a version missing its date",
			defect:         "a re-run that attached the date the version lacked does not say it reached beyond this machine",
			wantRegistries: []string{publishTestRegistry},
			wantBeyond:     true,
			run: func(t *testing.T, s *server, root string, store *memory.Store) publicationReach {
				cfg, refusal := s.publishEntityConfig(publishEntityArgs{Entity: "1.0.0/entity.yaml", WorkspaceRoot: root})
				if refusal != "" {
					t.Fatalf("publishEntityConfig: %s", refusal)
				}
				cfg.PushTarget = &refusesDates{Store: store}
				if _, err := runPublishEntity(t.Context(), s.servedWorkspace(), cfg); err == nil {
					t.Fatal("the first publication succeeded; this row needs its date to have been refused")
				}
				again := publishThroughTool(t, s, root, store)
				if !again.AlreadyPublished {
					t.Fatalf("the completing publication is not reported as already published: %+v", again)
				}
				return again.Reach
			},
		},
		{
			name:           "a dry run",
			defect:         "a dry run claims to have reached beyond this machine",
			wantRegistries: []string{publishTestRegistry},
			run: func(t *testing.T, s *server, root string, _ *memory.Store) publicationReach {
				res := callTool(t, s, publishEntityToolName, publishEntityArgs{
					Entity: "1.0.0/entity.yaml", DryRun: true, WorkspaceRoot: root,
				})
				var got publishEntityPayload
				decodeStructured(t, res, &got)
				return got.Reach
			},
		},
		{
			name:           "a signing batch of references already signed",
			defect:         "a batch that attached nothing claims a landing that did not happen",
			wantRegistries: []string{publishTestRegistry},
			run: func(t *testing.T, s *server, root string, store *memory.Store) publicationReach {
				publishThroughTool(t, s, root, store)
				signThroughTool(t, s, root, store, entityRef)
				again := signThroughTool(t, s, root, store, entityRef)
				if len(again.Signed) != 1 || again.Signed[0].Reference != entityRef {
					t.Fatalf("a reference already signed is still reported signed; got %+v", again.Signed)
				}
				// The text a caller is shown says what the reach says.
				if text := renderSignArtifactsText(again); !strings.Contains(text, "nothing was attached") {
					t.Errorf("the text of a batch that attached nothing does not say so:\n%s", text)
				}
				return again.Reach
			},
		},
		{
			name:           "a signing batch mixing a reference already signed and one unsigned",
			defect:         "a batch that attached one signature does not say it reached beyond this machine",
			wantRegistries: []string{publishTestRegistry},
			wantBeyond:     true,
			run: func(t *testing.T, s *server, root string, store *memory.Store) publicationReach {
				publishThroughTool(t, s, root, store)
				signThroughTool(t, s, root, store, entityRef)
				unsigned := publishArtifactThroughTool(t, s, root, store, "handbook", "2.0.0", publishTestRegistry)
				return signThroughTool(t, s, root, store, entityRef, unsigned).Reach
			},
		},
		{
			name: "a signing batch spanning two registries under one credential",
			defect: "a batch spanning registries names only one of them, or names them in an order other than " +
				"the one it addressed them in, or names one twice",
			wantRegistries: []string{reachSecondRegistry, publishTestRegistry},
			wantBeyond:     true,
			run: func(t *testing.T, s *server, root string, store *memory.Store) publicationReach {
				publishThroughTool(t, s, root, store)
				second := publishArtifactThroughTool(t, s, root, store, "handbook", "2.0.0", reachSecondRegistry)
				first := publishArtifactThroughTool(t, s, root, store, "guide", "3.0.0", publishTestRegistry)
				return signThroughTool(t, s, root, store, second, entityRef, first).Reach
			},
		},
		{
			name:           "a deprecation",
			defect:         "an attached marker does not say it reached beyond this machine",
			wantRegistries: []string{publishTestRegistry},
			wantBeyond:     true,
			run: func(t *testing.T, s *server, root string, store *memory.Store) publicationReach {
				publishThroughTool(t, s, root, store)
				cfg, refusal := s.deprecateArtifactConfig(deprecateArtifactArgs{
					Reference: entityRef, Severity: "warning", Reason: "superseded by 2.0.0", WorkspaceRoot: root,
				})
				if refusal != "" {
					t.Fatalf("deprecateArtifactConfig: %s", refusal)
				}
				cfg.Target = store
				got, err := runDeprecateArtifact(t.Context(), cfg)
				if err != nil {
					t.Fatalf("deprecate: %v", err)
				}
				return got.Reach
			},
		},
	}

	// Not parallel: the fixture sets the environment override for the global
	// configuration layer.
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			s, root := publishFixture(t, reachWorkspace(t))
			got := row.run(t, s, root, memory.New())
			if got.BeyondThisMachine != row.wantBeyond {
				t.Errorf("beyond_this_machine = %v; want %v (defect: %s)", got.BeyondThisMachine, row.wantBeyond, row.defect)
			}
			if !slices.Equal(got.Registries, row.wantRegistries) {
				t.Errorf("registries = %q; want %q (defect: %s)", got.Registries, row.wantRegistries, row.defect)
			}
		})
	}
}

// signThroughTool signs references held in store through this surface's own
// argument projection.
func signThroughTool(t *testing.T, s *server, root string, store *memory.Store, references ...string) signArtifactsPayload {
	t.Helper()
	cfg, refusal := s.signArtifactsConfig(signArtifactsArgs{References: references, WorkspaceRoot: root})
	if refusal != "" {
		t.Fatalf("signArtifactsConfig: %s", refusal)
	}
	cfg.Target = store
	got, err := runSignArtifacts(t.Context(), cfg)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return got
}

// publishArtifactThroughTool publishes the fixture's artifact file into store
// under name and tag at registry, through this surface's own argument projection,
// and returns the reference a signing batch addresses it by.
func publishArtifactThroughTool(t *testing.T, s *server, root string, store *memory.Store, name, tag, registry string) string {
	t.Helper()
	cfg, refusal := s.publishArtifactConfig(publishArtifactArgs{
		File: "bundle.txt", Name: name, Tag: tag, Registry: registry, WorkspaceRoot: root,
	})
	if refusal != "" {
		t.Fatalf("publishArtifactConfig: %s", refusal)
	}
	cfg.PushTarget = store
	got, err := runPublishArtifact(t.Context(), cfg)
	if err != nil {
		t.Fatalf("publish the artifact: %v", err)
	}
	if !got.Reach.BeyondThisMachine {
		t.Fatalf("an artifact that landed does not say it reached beyond this machine: %+v", got.Reach)
	}
	return got.Reference + ":" + tag
}
