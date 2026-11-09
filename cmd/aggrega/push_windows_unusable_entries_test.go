// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/publishops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestPushDryRunWarnsAboutEachEntryWindowsCannotUseAndExitsZero drives `aggrega push
// <entity> --dry-run` through the real command tree, on whatever platform runs it,
// over a Component carrying two files whose names Windows cannot hold as written:
// `aux/x.yaml`, under a directory named after a device, and `report.`, ending in a
// full stop. Its two schema pillars import them, which is the smallest declaration
// that makes an entity carry files.
//
// The command must exit cleanly, print the dry run's account on standard output
// with no warning in it, and print on standard error one warning per entry, in the
// order the archive's names are composed, each opening with the condition's own
// sentence — read off its registration rather than spelled here — and naming the
// entry and the segment that decides it. The entity's own file name,
// `component.yaml`, is judged with them and draws nothing.
func TestPushDryRunWarnsAboutEachEntryWindowsCannotUseAndExitsZero(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	dir := filepath.Join(root, "component", "core", "windows-names", "1.0.0")
	entityPath := filepath.Join(dir, "component.yaml")
	writeFixture(t, entityPath, `apiVersion: aggrega.ai/v1beta1
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
`)
	writeFixture(t, filepath.Join(dir, "aux", "x.yaml"), "greeting:\n  type: string\n  default: hello\n")
	writeFixture(t, filepath.Join(dir, "report."), "level:\n  type: string\n  default: info\n")
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\ndefault_registry: example.com\n")
	empty := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", empty)
	t.Setenv("HOME", empty)
	t.Setenv("AGGREGA_OFFLINE", "")

	var stdout, stderr bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"push", "--workspace-root", root, entityPath, "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("aggrega push --dry-run refused; the warning refuses nothing: %v\nstderr:\n%s", err, stderr.String())
	}

	sentence, ok := cerrors.MessageOf(publishops.ErrWindowsUnusableEntry())
	if !ok || sentence == "" {
		t.Fatal("the advisory registers no sentence, so no line can be read for it")
	}
	if !strings.Contains(stdout.String(), "aggrega push (dry-run): example.com/component/core/windows-names:1.0.0 (") {
		t.Errorf("standard output does not carry the dry run's account:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), sentence) {
		t.Errorf("the warning reached standard output, the stream a script reads:\n%s", stdout.String())
	}

	var warnings []string
	for _, line := range strings.Split(stderr.String(), "\n") {
		if strings.HasPrefix(line, "warning: "+sentence+" ") {
			warnings = append(warnings, line)
		}
	}
	want := []struct{ entry, segment string }{{"aux/x.yaml", "aux"}, {"report.", "report."}}
	if len(warnings) != len(want) {
		t.Fatalf("standard error carries %d warning(s) about entries Windows cannot use, want %d:\n%s",
			len(warnings), len(want), stderr.String())
	}
	for i, w := range want {
		for _, part := range []string{fmt.Sprintf("the entry %q", w.entry), fmt.Sprintf("its segment %q", w.segment)} {
			if !strings.Contains(warnings[i], part) {
				t.Errorf("warning %d does not name %s: %q", i, part, warnings[i])
			}
		}
	}
	if strings.Contains(stderr.String(), `the entry "component.yaml"`) {
		t.Errorf("the entity's own file name, which Windows holds, drew a warning:\n%s", stderr.String())
	}
}
