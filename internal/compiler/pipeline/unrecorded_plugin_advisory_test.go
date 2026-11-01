// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestAPluginResolvedWithNoRecordedDigestJoinsTheRunsChannelOnce holds the
// advisory a run's plugin resolution raises — a plugin read with no recorded
// digest to check it against, under a construction that declares recorded
// digests govern — to the run's one advisory channel, so a surface reading no
// terminal receives it with the run's other advisories, and to the run's
// display, once per reference however often the run resolves it, and to no
// process stream.
//
// The resolutions go through the run's own resolver construction, the one every
// plugin the run loads is resolved through.
//
// 🔴 PLANTED DEFECT: hand the resolver no receiver (buildPluginResolver), or drop
// firstMention from reportUnrecordedPlugin, or write the advisory to the process
// stream in pluginpath — and this fails.
func TestAPluginResolvedWithNoRecordedDigestJoinsTheRunsChannelOnce(t *testing.T) {
	guest := filepath.Join(t.TempDir(), "guest.wasm")
	if err := os.WriteFile(guest, []byte("guest-bytes"), 0o644); err != nil {
		t.Fatalf("write guest: %v", err)
	}
	cfg := workspaceconfig.Default()
	var display bytes.Buffer
	log := &AdvisoryLog{}
	inputs := Inputs{
		WorkspaceConfig: &cfg,
		WorkspaceRoot:   t.TempDir(),
		PluginPins:      pluginpath.RecordedPins(pluginpath.PinMaps{}),
		AdvisoryLog:     log,
		Display:         &display,
	}

	stray := captureProcessStderr(t, func() {
		r := buildPluginResolver(inputs, nil)
		for i := 0; i < 3; i++ {
			if _, err := r.Resolve(guest); err != nil {
				t.Fatalf("resolve %d: %v", i, err)
			}
		}
	})

	entries := log.Entries()
	if len(entries) != 1 {
		t.Fatalf("three resolutions of one unrecorded plugin left %d entries on the run's channel, want 1: %+v",
			len(entries), entries)
	}
	got := entries[0]
	if !strings.Contains(got.Message, fmt.Sprintf("plugin %q resolved from", guest)) ||
		!strings.Contains(got.Message, "no recorded digest") {
		t.Errorf("the entry does not say which plugin ran unverified: %q", got.Message)
	}
	if got.Severity != abi.SeverityWarn {
		t.Errorf("entry severity = %q, want %q", got.Severity, abi.SeverityWarn)
	}
	if got.SourceRef != guest {
		t.Errorf("entry names %q as its source, want the plugin reference %q", got.SourceRef, guest)
	}
	if want := WarningLine(got) + "\n"; display.String() != want {
		t.Errorf("the run's display = %q\nwant the one warning line %q", display.String(), want)
	}
	if stray != "" {
		t.Errorf("the advisory was written to the process's standard error:\n%s", stray)
	}
}

// TestAPinFreeRunRaisesNoUnrecordedPluginAdvisory is the control: a run whose
// pin source declares that no recorded digest governs what it resolves raises
// nothing for the same plugin, so the entry above is the rule firing rather than
// a line every resolution writes.
func TestAPinFreeRunRaisesNoUnrecordedPluginAdvisory(t *testing.T) {
	guest := filepath.Join(t.TempDir(), "guest.wasm")
	if err := os.WriteFile(guest, []byte("guest-bytes"), 0o644); err != nil {
		t.Fatalf("write guest: %v", err)
	}
	cfg := workspaceconfig.Default()
	var display bytes.Buffer
	log := &AdvisoryLog{}
	inputs := Inputs{
		WorkspaceConfig: &cfg,
		WorkspaceRoot:   t.TempDir(),
		PluginPins:      pluginpath.NoPins(),
		AdvisoryLog:     log,
		Display:         &display,
	}
	if _, err := buildPluginResolver(inputs, nil).Resolve(guest); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if entries := log.Entries(); len(entries) != 0 || display.Len() != 0 {
		t.Errorf("a pin-free run advised: entries %+v, display %q", entries, display.String())
	}
}

// captureProcessStderr runs fn with the process's standard error redirected and
// returns what was written to it.
func captureProcessStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	saved := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		_, _ = b.ReadFrom(r)
		done <- b.String()
	}()
	func() {
		defer func() { os.Stderr = saved }()
		fn()
	}()
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}
