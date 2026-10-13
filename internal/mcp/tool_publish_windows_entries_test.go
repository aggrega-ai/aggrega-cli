// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// windowsNamesEntity is a Component whose two schema pillars are imported from
// `aux/x.yaml`, under a directory named after a device, and `report.`, ending in a
// full stop — two entries Windows cannot hold as written.
const windowsNamesEntity = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/windows-names
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      $import: ./aux/x.yaml
    infra:
      $import: ./report.
`

// TestThePublishToolAnswersWithTheEntriesWindowsCannotUse drives the publish tool
// through the real tools/call dispatch, as a dry run, over an entity carrying two
// entries Windows cannot hold and over the fixture's entity, which carries nothing.
//
// The first answer must carry `windows_unusable_entries` naming both, in the order
// the archive's names are composed, and its text must name them too; the second
// must carry no such key at all — absent, not an empty list — which is read off the
// raw structured payload, because a decoded struct cannot tell the two apart.
func TestThePublishToolAnswersWithTheEntriesWindowsCannotUse(t *testing.T) {
	s, root := publishFixture(t, defaultRegistryWorkspace)
	dir := filepath.Join(root, "windows-names", "1.0.0")
	for rel, body := range map[string]string{
		"component.yaml": windowsNamesEntity,
		"aux/x.yaml":     "greeting:\n  type: string\n  default: hello\n",
		"report.":        "level:\n  type: string\n  default: info\n",
	} {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	t.Run("an entity carrying entries Windows cannot hold", func(t *testing.T) {
		res := callTool(t, s, publishEntityToolName, publishEntityArgs{
			Entity: "windows-names/1.0.0/component.yaml", DryRun: true, WorkspaceRoot: root,
		})
		var got publishEntityPayload
		decodeStructured(t, res, &got)
		want := []string{"aux/x.yaml", "report."}
		if !reflect.DeepEqual(got.WindowsUnusableEntries, want) {
			t.Errorf("windows_unusable_entries = %q, want %q", got.WindowsUnusableEntries, want)
		}
		text := res.Content[0].Text
		for _, name := range want {
			if !strings.Contains(text, `"`+name+`"`) {
				t.Errorf("the answer's text does not name %q:\n%s", name, text)
			}
		}
	})

	t.Run("an entity carrying nothing", func(t *testing.T) {
		res := callTool(t, s, publishEntityToolName, publishEntityArgs{
			Entity: "1.0.0/entity.yaml", DryRun: true, WorkspaceRoot: root,
		})
		var keys map[string]json.RawMessage
		decodeStructured(t, res, &keys)
		if len(keys) == 0 {
			t.Fatal("the structured answer carries no key at all, so an absent one proves nothing")
		}
		if raw, present := keys["windows_unusable_entries"]; present {
			t.Errorf("the answer carries windows_unusable_entries for an entity carrying nothing: %s", raw)
		}
	})
}
