// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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

// TestPublishEntityOnTheDaemonAnswersWithTheEntriesWindowsCannotUse drives
// aggrega/publishEntity over the wire, as a dry run, over an entity carrying two
// entries Windows cannot hold and over the fixture's entity, which carries nothing.
//
// The first answer must carry `windowsUnusableEntries` naming both, in the order the
// archive's names are composed; the second must carry no such key at all — absent,
// not an empty list — which is read off the raw result, because a decoded struct
// cannot tell the two apart.
func TestPublishEntityOnTheDaemonAnswersWithTheEntriesWindowsCannotUse(t *testing.T) {
	t.Parallel()
	srv, root := publishFixture(t, defaultRegistryWorkspace)
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

	carrying := callDaemonMethod(t, srv, "aggrega/publishEntity", map[string]any{
		"entity": "windows-names/1.0.0/component.yaml",
		"dryRun": true,
	})
	var res publishEntityResult
	decodeDaemonResult(t, carrying, &res)
	if want := []string{"aux/x.yaml", "report."}; !reflect.DeepEqual(res.WindowsUnusableEntries, want) {
		t.Errorf("windowsUnusableEntries = %q, want %q", res.WindowsUnusableEntries, want)
	}

	clean := callDaemonMethod(t, srv, "aggrega/publishEntity", map[string]any{
		"entity": "1.0.0/entity.yaml",
		"dryRun": true,
	})
	var keys map[string]json.RawMessage
	decodeDaemonResult(t, clean, &keys)
	if len(keys) == 0 {
		t.Fatal("the answer carries no key at all, so an absent one proves nothing")
	}
	if raw, present := keys["windowsUnusableEntries"]; present {
		t.Errorf("the answer carries windowsUnusableEntries for an entity carrying nothing: %s", raw)
	}
}
