// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// bundleVersionProbe is the version the launches below hand the server as the
// desktop bundle's where the two are to differ. It is shaped like no release, so
// no binary under test reports it as its own and no surface can carry it by
// coincidence.
const bundleVersionProbe = "0.0.0-bundle-version-probe"

// bundleLaunchAnswer is what one launch of `aggrega mcp` answered: the
// handshake's serverInfo block and guidance, and the argumentless capability
// answer's text and structured payload.
type bundleLaunchAnswer struct {
	serverInfo   map[string]json.RawMessage
	instructions string
	text         string
	payload      map[string]json.RawMessage
}

// TestTheServerReportsItsVersionBesideTheBundleThatLaunchedIt runs the built
// binary as the desktop bundle launches it — `aggrega mcp` over standard input
// and output, started in a folder of the client's own — three times: with the
// bundle's version set to a version other than the binary's, set to the
// binary's own, and absent. The binary's own version is read from that binary,
// through `aggrega version -o json`, never written here.
//
// Each launch answers initialize and the argumentless describe-capabilities.
// serverInfo carries exactly its name and its version every time. The
// capability answer carries server_version every time and bundle_version only
// where the variable was set, and its text opens with one line naming them:
// where they differ, the sentence saying the executable and the bundle come from
// different releases, which the guidance then opens with too; where they are
// equal, the line naming both and nothing more, and guidance identical to the
// launch that carried no bundle; where absent, the server's version alone.
//
// 🔴 PLANTED DEFECT: in internal/mcp/version_report.go, replace the comparison
// `bundleVersion == serverVersion` in releaseDifference with `true`. The
// differing launch then fails: its capability answer opens with no line naming
// the two versions, and its guidance carries no sentence saying they differ.
func TestTheServerReportsItsVersionBesideTheBundleThatLaunchedIt(t *testing.T) {
	t.Parallel()
	bin := buildServeBinary(t)
	own := builtBinaryVersion(t, bin)
	if own == "" || own == bundleVersionProbe {
		t.Fatalf("the binary under test reports version %q, which the differing launch cannot be told apart from",
			own)
	}

	differing := launchForBundleVersion(t, bin, bundleVersionProbe)
	equal := launchForBundleVersion(t, bin, own)
	absent := launchForBundleVersion(t, bin, "")

	difference := fmt.Sprintf("This server runs aggrega %s, launched by the desktop bundle version %s: the aggrega "+
		"executable and the bundle come from different releases.", own, bundleVersionProbe)

	for _, row := range []struct {
		name   string
		answer bundleLaunchAnswer
		// bundle is the bundle_version the payload must carry; empty wants no key.
		bundle   string
		wantLine string
	}{
		{name: "a bundle from another release", answer: differing, bundle: bundleVersionProbe, wantLine: difference},
		{name: "a bundle from the binary's own release", answer: equal, bundle: own,
			wantLine: fmt.Sprintf("This server runs aggrega %s, launched by the desktop bundle of the same version, %s.",
				own, own)},
		{name: "no bundle", answer: absent,
			wantLine: fmt.Sprintf("This server runs aggrega %s.", own)},
	} {
		t.Run(row.name, func(t *testing.T) {
			a := row.answer
			keys := make([]string, 0, len(a.serverInfo))
			for key := range a.serverInfo {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			if !slices.Equal(keys, []string{"name", "version"}) {
				t.Errorf("serverInfo carries %q; want exactly name and version", keys)
			}
			if got := jsonString(t, a.serverInfo["version"]); got != own {
				t.Errorf("serverInfo.version is %q; want the binary's own %q", got, own)
			}

			if got := jsonString(t, a.payload["server_version"]); got != own {
				t.Errorf("describe-capabilities carries server_version %q; want the binary's own %q", got, own)
			}
			raw, carried := a.payload["bundle_version"]
			switch {
			case row.bundle == "" && carried:
				t.Errorf("describe-capabilities carries bundle_version %s for a launch no bundle made", raw)
			case row.bundle != "" && jsonString(t, raw) != row.bundle:
				t.Errorf("describe-capabilities carries bundle_version %s; want %q", raw, row.bundle)
			}
			if first, _, _ := strings.Cut(a.text, "\n"); first != row.wantLine {
				t.Errorf("describe-capabilities' text opens with %q; want %q", first, row.wantLine)
			}
			if _, index, _ := strings.Cut(a.text, "\n"); !strings.HasPrefix(index, "Every tool this server offers you") {
				t.Errorf("describe-capabilities' text carries more than the one line before its index:\n%s", a.text)
			}
		})
	}

	if want := difference + "\n\n" + absent.instructions; differing.instructions != want {
		t.Errorf("the guidance of a launch whose bundle comes from another release is\n%s\nwant the sentence naming "+
			"both, then the guidance a launch with no bundle reads:\n%s", differing.instructions, want)
	}
	if equal.instructions != absent.instructions {
		t.Errorf("the guidance of a launch whose bundle comes from the binary's own release differs from a launch "+
			"with no bundle; nothing more is said where the two agree:\n%s", equal.instructions)
	}
	if strings.Contains(absent.instructions, "different releases") {
		t.Errorf("the guidance of a launch with no bundle speaks of different releases:\n%s", absent.instructions)
	}
}

// builtBinaryVersion is the version the built binary reports for itself, read
// through its own version command.
func builtBinaryVersion(t *testing.T, bin string) string {
	t.Helper()
	cmd := exec.Command(bin, "version", "-o", "json")
	cmd.Dir = t.TempDir()
	cmd.Env = builtBinaryEnv(t, "AGGREGA_OFFLINE=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("aggrega version -o json: %v", err)
	}
	var reported struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(out, &reported); err != nil {
		t.Fatalf("decode the version command's output: %v\n%s", err, out)
	}
	return reported.Version
}

// launchForBundleVersion starts `aggrega mcp` over standard input and output
// with the bundle's version set to bundle — set empty for none, which the server
// reads as unset, so a value in this process's own environment cannot leak in —
// sends initialize and the argumentless describe-capabilities, closes the input,
// and returns what the two answered.
func launchForBundleVersion(t *testing.T, bin, bundle string) bundleLaunchAnswer {
	t.Helper()
	// A liveness ceiling, not a budget: it separates a server that answers and
	// exits from one that never will.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "mcp")
	cmd.Dir = t.TempDir() // the client's own folder, which no workspace resolves from
	cmd.Env = builtBinaryEnv(t, "AGGREGA_OFFLINE=1", workspaceconfig.EnvWorkspaceRoot+"=",
		workspaceconfig.EnvBundleVersion+"="+bundle)
	cmd.Stdin = strings.NewReader(rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`) +
		rpcLine(2, "tools/call", `{"name":"describe-capabilities","arguments":{}}`))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("aggrega mcp with %s=%q: %v\nstderr:\n%s", workspaceconfig.EnvBundleVersion, bundle, err,
			stderr.String())
	}
	answers := answersByID(t, stdout.String(), 1, 2)

	var initialized struct {
		ServerInfo   map[string]json.RawMessage `json:"serverInfo"`
		Instructions string                     `json:"instructions"`
	}
	if a := answers[1]; a.Error != nil || json.Unmarshal(a.Result, &initialized) != nil {
		t.Fatalf("initialize was answered %s %+v; want the handshake's result", a.Result, a.Error)
	}
	var called struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent map[string]json.RawMessage `json:"structuredContent"`
	}
	if a := answers[2]; a.Error != nil || json.Unmarshal(a.Result, &called) != nil || called.IsError ||
		len(called.Content) == 0 {
		t.Fatalf("describe-capabilities was answered %s %+v; want the index", a.Result, a.Error)
	}
	return bundleLaunchAnswer{
		serverInfo:   initialized.ServerInfo,
		instructions: initialized.Instructions,
		text:         called.Content[0].Text,
		payload:      called.StructuredContent,
	}
}

// jsonString decodes one JSON string value, failing where raw holds anything
// else, and reads an absent value as the empty string.
func jsonString(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	if raw == nil {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("%s is not a JSON string: %v", raw, err)
	}
	return s
}
