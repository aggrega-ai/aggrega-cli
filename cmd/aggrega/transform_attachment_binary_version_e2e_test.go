// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// binaryVersionProofValue is the label value the attached policy decides, which the
// fixture's workload echoes into the rendered output.
const binaryVersionProofValue = "decided-by-the-binary-version"

// embeddedRegoVersion returns the version of the Rego governance entity this binary
// carries, read out of the binary itself, because it moves with every build of the
// guests and a literal would be stale on the next one.
func embeddedRegoVersion(t *testing.T) string {
	t.Helper()
	var p types.Plugin
	if err := yaml.Unmarshal(embeddedPluginEntityYAML(t, regoArtifact), &p); err != nil {
		t.Fatalf("parse the embedded %s entity: %v", regoArtifact, err)
	}
	if p.Metadata.Version == "" {
		t.Fatalf("the embedded %s entity carries no version", regoArtifact)
	}
	return p.Metadata.Version
}

// regoArtifact is the identity coordinate of the Rego governance plugin the binary
// carries.
const regoArtifact = "governance/rego"

// TestPullResolvesATransformAttachmentAtTheBinarysOwnVersion drives the real pull and
// build, offline, over a governance attachment at the label-transform role whose
// version the workspace's catalog does not carry: the catalog holds the Rego plugin's
// entity at another version only, with no module beside it.
//
// Naming the version the binary's own entity carries, the pull resolves the
// attachment from the binary — as the same reference attached at a verdict role is
// loaded — and pins it as every output-shaping attachment is pinned: the entity at
// that version, and the artifact at the content digest of the binary's own copy with
// no manifest digest. The build over that lockfile passes its drift check and runs
// the attached policy. Naming a version neither the catalog nor the binary carries,
// the pull refuses with the version-select condition, naming the reference.
func TestPullResolvesATransformAttachmentAtTheBinarysOwnVersion(t *testing.T) {
	own := embeddedRegoVersion(t)
	wasm, ok := embedded.Lookup(regoArtifact)
	if !ok {
		t.Fatalf("the binary carries no %s guest", regoArtifact)
	}

	cases := []struct {
		name    string
		version string
		wantErr error
	}{
		{name: "the version the binary carries", version: own},
		{name: "a version neither the catalog nor the binary carries", version: "9.9.9", wantErr: catalog.ErrUnsatisfiableEntityVersion()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, releasePath, _ := catalogGovernanceAttachmentWorkspace(t, catalogGovernanceAttachment{
				artifact: regoArtifact, export: "mutate", role: "label-transform",
			}, nil, "offline: true\n")
			entity := regoArtifact + ":" + tc.version
			writeWorkspaceConfig(t, root, "workspace:\n  catalog_dirs:\n    - ./catalog\noffline: true\nauto_pull: false\n"+
				"plugins:\n  governance:\n    - uri: "+entity+"\n      role: label-transform\n"+
				"      with:\n        sources:\n          - policy.rego\n")
			writeFixture(t, filepath.Join(root, "policy.rego"), `package proof

import rego.v1

labels contains {"node": id, "key": "proof.example/version", "value": "`+binaryVersionProofValue+`"} if {
	some id, node in input.graph.nodes
	node.labels["component.aggrega.ai/type"] == "server"
}
`)
			t.Chdir(root)
			ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})

			err := pullRun(context.Background(), pullConfig{ws: ws, ReleasePath: releasePath})
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("the pull must refuse %q with the version-select condition; got %v", entity, err)
				}
				if !strings.Contains(err.Error(), entity) {
					t.Errorf("the refusal does not name %q: %v", entity, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("pull: %v — a transform attachment at the binary's own version resolves from the binary", err)
			}

			lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
			if err != nil {
				t.Fatalf("load the written lockfile: %v", err)
			}
			i := slices.IndexFunc(lf.Entities, func(e lockfile.Entity) bool { return e.URI == entity })
			if i < 0 || lf.Entities[i].ResolvedVersion != own {
				t.Errorf("the lockfile pins no entity row %q at %q; entities=%+v", entity, own, lf.Entities)
			}
			j := slices.IndexFunc(lf.Plugins, func(p lockfile.Plugin) bool { return p.URI == regoArtifact })
			if j < 0 {
				t.Fatalf("the lockfile pins no artifact for %q; plugins=%+v", regoArtifact, lf.Plugins)
			}
			want := digest.ContentDigest(wasm)
			if p := lf.Plugins[j]; p.Digest != want || p.ResolvedVersion != want || p.ManifestDigest != "" ||
				!slices.Contains(p.ReferencedFrom, entity) {
				t.Errorf("the artifact pin is %+v; want the content digest of the binary's own copy, %s, as both "+
					"version and digest, no manifest digest, referenced from %q", p, want, entity)
			}
			if !slices.ContainsFunc(lf.Mutators, func(m lockfile.MutatorAttachment) bool {
				return m.Plugin == regoArtifact && m.Role == "label-transform"
			}) {
				t.Errorf("the lockfile records no label-transform attachment of %q; mutators=%+v", regoArtifact, lf.Mutators)
			}

			if err := dispatchBuild(context.Background(), ws, releasePath, nil, false, io.Discard, io.Discard); err != nil {
				t.Fatalf("the build over the lockfile the pull wrote: %v", err)
			}
			if found, tree := renderedTreeHolds(t, filepath.Join(root, "dist"), binaryVersionProofValue); !found {
				t.Errorf("the rendered output does not carry the label the attached policy decides:\n%s", tree)
			}
		})
	}
}
