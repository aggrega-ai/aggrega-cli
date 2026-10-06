// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestAConfiguredReleaseThroughALinkResolvesTheNamesBesideIt drives aggrega/irGraph
// through the production dispatch for a Release the workspace configuration names,
// which refers to its Aggregate and its Manifest by name, so the compile looks for
// them among the documents beside it.
//
// The Release is, row by row, a plain file and a file link leading out of the
// workspace. The configuration admits the link where it names it, so the compile
// does not judge that one document again when it meets it beside the Aggregate and
// the Manifest: the linked row answers exactly as the plain one does, with no
// refusal naming the Release. A neighbouring Release that is a link leading out is
// workspace content the compile meets on its own account, and is refused naming
// it, with the code a compile's refusal answers.
func TestAConfiguredReleaseThroughALinkResolvesTheNamesBesideIt(t *testing.T) {
	t.Parallel()
	const release = "release-linked.yaml"
	answer := func(t *testing.T, arrange func(t *testing.T, ws, outside string)) (jsonRPCResponse, string) {
		t.Helper()
		ws, outside := t.TempDir(), t.TempDir()
		writeFixtureFile(t, filepath.Join(ws, workspaceconfig.ConfigFilename),
			"workspace:\n  releases:\n    "+release+": {}\n")
		writeFixtureFile(t, filepath.Join(ws, "aggregate.yaml"), linkedReleaseAggregate)
		writeFixtureFile(t, filepath.Join(ws, "manifest.yaml"), linkedReleaseManifest)
		writeFixtureFile(t, filepath.Join(ws, "release-linked.lock"), "version: 1\n")
		arrange(t, ws, outside)
		var out bytes.Buffer
		srv := catalogDirsServer(t, ws, &out)
		return irGraphAnswer(t, srv, &out, release), ws
	}
	link := func(t *testing.T, target, at string) {
		t.Helper()
		if err := os.Symlink(target, at); err != nil {
			t.Skipf("symbolic links are not available here: %v", err)
		}
	}
	codeOf := func(resp jsonRPCResponse) int {
		if resp.Error == nil {
			return 0
		}
		return resp.Error.Code
	}

	control, _ := answer(t, func(t *testing.T, ws, _ string) {
		writeFixtureFile(t, filepath.Join(ws, release), linkedReleaseDocument)
	})
	if control.Error != nil && strings.Contains(control.Error.Message, "leads through a link") {
		t.Fatalf("the control refused a link it does not hold: %s", control.Error.Message)
	}

	t.Run("the configured Release a file link leading out answers as the plain one does", func(t *testing.T) {
		resp, ws := answer(t, func(t *testing.T, ws, outside string) {
			writeFixtureFile(t, filepath.Join(outside, "release-copy.yaml"), linkedReleaseDocument)
			link(t, filepath.Join(outside, "release-copy.yaml"), filepath.Join(ws, release))
		})
		if resp.Error != nil && strings.Contains(resp.Error.Message, "the link "+filepath.Join(ws, release)) {
			t.Fatalf("the compile refused the Release the configuration names, as a document beside itself: %s",
				resp.Error.Message)
		}
		if codeOf(resp) != codeOf(control) {
			t.Fatalf("the linked Release answered code %d where the plain one answered %d: %+v",
				codeOf(resp), codeOf(control), resp.Error)
		}
	})

	t.Run("a neighbouring Release a link leading out is refused naming it", func(t *testing.T) {
		resp, ws := answer(t, func(t *testing.T, ws, outside string) {
			writeFixtureFile(t, filepath.Join(ws, release), linkedReleaseDocument)
			writeFixtureFile(t, filepath.Join(outside, "sibling-copy.yaml"), linkedReleaseDocument)
			link(t, filepath.Join(outside, "sibling-copy.yaml"), filepath.Join(ws, "sibling.yaml"))
		})
		if resp.Error == nil {
			t.Fatalf("answered over a neighbouring Release leading out of the workspace: %s", resp.Result)
		}
		if resp.Error.Code != errEntityNotResolvable {
			t.Errorf("code %d; want %d", resp.Error.Code, errEntityNotResolvable)
		}
		if !strings.Contains(resp.Error.Message, "the link "+filepath.Join(ws, "sibling.yaml")+" leads to ") {
			t.Errorf("the refusal does not name the neighbouring link: %s", resp.Error.Message)
		}
	})
}

// linkedReleaseDocument is a Release naming its Aggregate and its Manifest by name.
const linkedReleaseDocument = "apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: linked-release\n" +
	"  version: \"1.0.0\"\n  environment: production\nspec:\n  aggregate: linked-app\n  manifest: linked-manifest\n"

// linkedReleaseAggregate is the Aggregate the Release names, kept beside it.
const linkedReleaseAggregate = "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: linked-app\n" +
	"  version: \"1.0.0\"\nspec:\n  components: {}\n"

// linkedReleaseManifest is the Manifest the Release names, kept beside it.
const linkedReleaseManifest = "apiVersion: aggrega.ai/v1alpha1\nkind: Manifest\nmetadata:\n  name: linked-manifest\n" +
	"  version: \"1.0.0\"\nspec:\n  mappings:\n    components:\n      type: {}\n"
