// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// The digest `aggrega push --dry-run` prints is the digest a publication of the
// same content lands. A reader that holds an entity against the digest its
// version was published as reads that digest from the dry run, without reaching
// any registry, and is right only while the two agree — so each row reads the
// digest off the command's own dry-run line, then publishes the same content for
// real into an in-memory registry, and asserts the dry run's digest equals both
// the digest the publication line names and the digest the registry resolves the
// tag to.
//
// The dry run's digest is read by its POSITION — the field after the reference,
// in parentheses — rather than by finding a digest anywhere in the output. The
// position is what a reader of the line relies on, and a Plugin's account carries
// a second digest, its executable's content digest, which a looser match would
// accept in place of the manifest's.

// dryRunDigestComponent is a Component naming no file beside its document, so it
// publishes as that document alone.
const dryRunDigestComponent = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/api-server
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      type: object
`

// dryRunDigestSetup writes one row's entity and returns the publication inputs a
// dry run and a real publication of it share: the workspace root, the entity
// document, the executable (a Plugin's only) and the destination.
type dryRunDigestSetup func(t *testing.T) (wsRoot, entityPath, pluginPath, registry string)

// statusLineFields returns the whitespace-separated fields of the first line of
// out that starts with prefix, failing the test when there is none.
func statusLineFields(t *testing.T, out, prefix string) []string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.Fields(line)
		}
	}
	t.Fatalf("no line starting %q; the output read:\n%s", prefix, out)
	return nil
}

// parenthesised returns field with its surrounding parentheses removed, failing
// the test when it is not written as "(<value>)".
func parenthesised(t *testing.T, field string) string {
	t.Helper()
	if !strings.HasPrefix(field, "(") || !strings.HasSuffix(field, ")") || len(field) < 3 {
		t.Fatalf("field %q is not a parenthesised value", field)
	}
	return field[1 : len(field)-1]
}

// TestPushDryRunPrintsTheDigestThePublicationLands pins the dry-run line's digest
// to the digest a publication lands, for an entity published as its document and
// for a Plugin published with its executable inside one manifest.
//
// Two rows whose digests differ, asserted rather than assumed, so a line printing
// one constant digest fails the second row rather than passing on the first.
func TestPushDryRunPrintsTheDigestThePublicationLands(t *testing.T) {
	rows := []struct {
		name  string
		setup dryRunDigestSetup
	}{
		{
			name: "Component",
			setup: func(t *testing.T) (string, string, string, string) {
				root := t.TempDir()
				t.Chdir(root)
				entityPath := filepath.Join(root, "1.0.0", "entity.yaml")
				writeEntityFixture(t, entityPath, dryRunDigestComponent)
				return root, entityPath, "", "example.com"
			},
		},
		{
			name: "Plugin",
			setup: func(t *testing.T) (string, string, string, string) {
				wsRoot, entityPath, pluginPath, _ := writeAgreeingPlugin(t)
				return wsRoot, entityPath, pluginPath, "hub.aggrega.ai"
			},
		},
	}

	printed := map[string]string{}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			wsRoot, entityPath, pluginPath, registry := row.setup(t)

			// The dry run, through the command's own adapter. It is given no target
			// at all: a dry run constructs none, so the digest it prints is made from
			// the content alone.
			var dry bytes.Buffer
			if err := pushRun(t.Context(), pushConfig{
				Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot}),
				EntityPath: entityPath,
				PluginPath: pluginPath,
				Registry:   registry,
				DryRun:     true,
				Out:        &dry,
			}); err != nil {
				t.Fatalf("pushRun --dry-run: %v", err)
			}
			// aggrega push (dry-run): <reference>:<tag> (<digest>)
			dryFields := statusLineFields(t, dry.String(), "aggrega push (dry-run): ")
			if len(dryFields) != 5 {
				t.Fatalf("the dry-run line has %d fields, want 5 (the reference fourth and the digest fifth): %q",
					len(dryFields), dryFields)
			}
			dryRef := dryFields[3]
			dryDigest := parenthesised(t, dryFields[4])
			if !strings.HasPrefix(dryDigest, "sha256:") {
				t.Fatalf("the dry-run line's digest %q is not a sha256 digest", dryDigest)
			}

			// The real publication of the same content, into an in-memory registry.
			store := memory.New()
			var published bytes.Buffer
			if err := pushRun(t.Context(), pushConfig{
				Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot}),
				EntityPath: entityPath,
				PluginPath: pluginPath,
				Registry:   registry,
				PushTarget: store,
				Out:        &published,
			}); err != nil {
				t.Fatalf("pushRun: %v", err)
			}
			// Push complete: <reference>:<tag> (<digest>)
			pushedFields := statusLineFields(t, published.String(), "Push complete: ")
			if len(pushedFields) != 4 {
				t.Fatalf("the publication line has %d fields, want 4: %q", len(pushedFields), pushedFields)
			}
			pushedRef := pushedFields[2]
			pushedDigest := parenthesised(t, pushedFields[3])

			if dryRef != pushedRef {
				t.Errorf("the dry run names %q and the publication %q", dryRef, pushedRef)
			}
			if dryDigest != pushedDigest {
				t.Errorf("the dry run prints %s and the publication landed %s; the dry run must print the digest the publication lands",
					dryDigest, pushedDigest)
			}
			tag := pushedRef[strings.LastIndex(pushedRef, ":")+1:]
			resolved, exists, err := oci.Resolve(t.Context(), store, tag)
			if err != nil || !exists {
				t.Fatalf("the registry does not resolve %s after the publication: exists=%v err=%v", tag, exists, err)
			}
			if dryDigest != resolved {
				t.Errorf("the dry run prints %s and the registry resolves %s to %s", dryDigest, tag, resolved)
			}
			printed[row.name] = dryDigest
		})
	}

	if len(printed) == len(rows) && printed["Component"] == printed["Plugin"] {
		t.Errorf("table precondition: the two rows must print different digests; both printed %s", printed["Component"])
	}
}
