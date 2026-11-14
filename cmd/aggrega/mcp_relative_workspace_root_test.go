// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// A published prompt cannot know where its reader's workspace folder sits on the
// machine, so it names the folder of an example's step relative to it. This drives
// that prompt's two calls through `aggrega mcp` launched over a workspace root, the
// way an assistant's client launches it: the scaffold tool writes an example into
// the folder named after it, and the build tool, named that example's step folder
// RELATIVE to the launch root, builds the step's Release there and writes its
// artifacts beside it — and nowhere else.
//
// The documentation bundle is served from an in-process registry at the narrowest
// seam, the target the run states, so the scaffold reads it through the production
// acquisition and signature check without reaching a network. The example's step
// carries its own catalog and an offline configuration, so the build's dependency
// fill resolves from that catalog alone and reaches no registry either.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle/docsbundletest"
)

// relativeRootExample is the example the scaffold call writes, under the name a
// published prompt gives it.
const relativeRootExample = "quickstart-ai"

// relativeRootStep is the example's step folder, relative to the folder the
// example is written into.
const relativeRootStep = "1"

// relativeRootMembers is the example the bundle carries: a page, and one step
// whose folder is a workspace of its own — its configuration offline, so a
// dependency fill reads only the step's own catalog — holding a Release of one
// component realized by a synthesized Docker Compose recipe.
func relativeRootMembers() []docsbundletest.Member {
	base := "language-reference/examples/" + relativeRootExample + "/"
	step := base + relativeRootStep + "/"
	return []docsbundletest.Member{
		{Name: base + "README.md", Body: "---\nexample:\n  level: start\n---\n\n# Quickstart\n\nA quickstart.\n"},
		{Name: step + ".aggrega-config.yaml", Body: "workspace: {}\noffline: true\n"},
		{Name: step + "catalog/component/example/compute/web/1.0.0/component.yaml", Body: `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: example/compute/web
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
    component.aggrega.ai/category: compute
spec:
  schema:
    infra:
      image:
        type: string
        default: "nginx:1.27"
`},
		{Name: step + "catalog/recipe/compose/web/1.0.0/recipe.yaml", Body: `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: web
  version: 1.0.0
spec:
  capability: compose
  implements: example/compute/web:1.0.0
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      main:
        image: "{{ optional .infra ` + "`image` `nginx:1.27`" + ` }}"
        ports:
          http: 8080
`},
		{Name: step + "aggregate.yaml", Body: `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: site
  version: "1.0.0"
spec:
  components:
    web:
      type: example/compute/web
`},
		{Name: step + "manifest.yaml", Body: `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: site-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        example/compute/web:1.0.0: compose/web:1.0.0
`},
		{Name: step + "release.yaml", Body: `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: site-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: site
  manifest: site-manifest
  components:
    web: {}
`},
	}
}

// toolAnswer is one tools/call answer as a client reads it.
type toolAnswer struct {
	text       string
	structured map[string]json.RawMessage
	isError    bool
}

// callOverLaunch runs `aggrega mcp --workspace-root root` under ctx with one
// tools/call, and returns its answer. Each call is a session of its own, because
// a session answers its requests concurrently and the build must not start
// before the scaffold has finished.
func callOverLaunch(t *testing.T, ctx context.Context, root, tool string, args map[string]any) toolAnswer {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal the arguments: %v", err)
	}
	var stdin, stdout, stderr bytes.Buffer
	stdin.WriteString(rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`))
	stdin.WriteString(rpcLine(2, "tools/call", fmt.Sprintf(`{"name":%q,"arguments":%s}`, tool, raw)))
	cmd := newRootCmd()
	cmd.SetIn(&stdin)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"mcp", "--workspace-root", root})
	if code := executeRoot(ctx, cmd); code != 0 {
		t.Fatalf("aggrega mcp exited %d\nstderr:\n%s", code, stderr.String())
	}
	answers := answersByID(t, stdout.String(), 1, 2)
	if answers[2].Error != nil {
		t.Fatalf("%s was answered with a protocol error: %s", tool, answers[2].Error.Message)
	}
	var call struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent map[string]json.RawMessage `json:"structuredContent"`
		IsError           bool                       `json:"isError"`
	}
	if err := json.Unmarshal(answers[2].Result, &call); err != nil || len(call.Content) == 0 {
		t.Fatalf("decode the %s answer: %v\n%s", tool, err, answers[2].Result)
	}
	return toolAnswer{text: call.Content[0].Text, structured: call.StructuredContent, isError: call.IsError}
}

// stringField decodes one string field of an answer's structured payload.
func stringField(t *testing.T, answer toolAnswer, field string) string {
	t.Helper()
	var out string
	if err := json.Unmarshal(answer.structured[field], &out); err != nil {
		t.Fatalf("decode %s: %v (%s)", field, err, answer.structured[field])
	}
	return out
}

// TestAStepFolderNamedRelativeToTheLaunchRootIsBuiltInPlace is the proof by
// invocation: a server launched over a root R scaffolds the example with no
// directory named, and reports the folder it wrote as R's quickstart-ai folder, in
// full; a build named the step folder relative to R writes the step's Docker
// Compose file into that step's own dist folder, and nothing under R's dist folder
// or the folder the process runs in; and the same build named the step folder
// absolutely writes the same bytes to the same place.
//
// 🔴 PLANTED DEFECTS: have callRoot return a relative root as sent, and the
// relative build is answered in the folder the process runs in, which holds no
// such step, so the build is refused; drop the location from the scaffold's
// answer and the output_dir assertion fails; have the absolute root read from the
// launch root as well, and the absolute build is refused.
//
// Not parallel: it moves the process working directory and the home it reads.
func TestAStepFolderNamedRelativeToTheLaunchRootIsBuiltInPlace(t *testing.T) {
	isolateCLIEnvironment(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("read the working folder: %v", err)
	}
	signer := docsbundletest.NewSigner(t)
	f := newExampleFixture(t, exampleMembers(relativeRootMembers()...), signer, signer)
	ctx := f.online()
	root := f.ws

	written := callOverLaunch(t, ctx, root, scaffoldToolName(t), map[string]any{"example": relativeRootExample})
	if written.isError {
		t.Fatalf("the scaffold tool was refused:\n%s", written.text)
	}
	exampleDir := filepath.Join(root, relativeRootExample)
	if dir := stringField(t, written, "dir"); dir != relativeRootExample {
		t.Errorf("the scaffold answer's dir is %q, want the folder as named, %q", dir, relativeRootExample)
	}
	outputDir := stringField(t, written, "output_dir")
	if !filepath.IsAbs(outputDir) || !sameDirectory(t, outputDir, exampleDir) {
		t.Errorf("the scaffold answer's output_dir is %q, want %s in full", outputDir, exampleDir)
	}
	if want := "The folder written is " + outputDir + "."; !strings.Contains(written.text, want) {
		t.Errorf("the scaffold account does not name the folder it wrote (%q):\n%s", want, written.text)
	}

	stepRel := relativeRootExample + "/" + relativeRootStep
	stepDir := filepath.Join(exampleDir, relativeRootStep)
	composeFile := filepath.Join(stepDir, "dist", "compose", "docker-compose.yaml")
	build := map[string]any{"release": "release.yaml", "auto_pull": true, "write": true}

	build["workspace_root"] = stepRel
	relative := callOverLaunch(t, ctx, root, "build", build)
	if relative.isError {
		t.Fatalf("the build naming %q relative to the launch root was refused:\n%s", stepRel, relative.text)
	}
	if got := stringField(t, relative, "output_dir"); !sameDirectory(t, got, filepath.Join(stepDir, "dist")) {
		t.Errorf("the relative build reports writing to %q, want the step's own dist folder %s", got,
			filepath.Join(stepDir, "dist"))
	}
	fromRelative, err := os.ReadFile(composeFile)
	if err != nil {
		t.Fatalf("the relative build wrote no %s: %v", composeFile, err)
	}
	if _, err := os.Stat(filepath.Join(root, "dist")); !os.IsNotExist(err) {
		t.Errorf("the relative build wrote under the launch root's own dist folder (stat: %v)", err)
	}
	if entries, err := os.ReadDir(cwd); err != nil || len(entries) != 0 {
		t.Errorf("the folder the process runs in holds %d entries after the build (%v), want none", len(entries), err)
	}

	if err := os.RemoveAll(filepath.Join(stepDir, "dist")); err != nil {
		t.Fatalf("clear the step's dist folder: %v", err)
	}
	build["workspace_root"] = stepDir
	absolute := callOverLaunch(t, ctx, root, "build", build)
	if absolute.isError {
		t.Fatalf("the build naming the step folder absolutely was refused:\n%s", absolute.text)
	}
	fromAbsolute, err := os.ReadFile(composeFile)
	if err != nil {
		t.Fatalf("the absolute build wrote no %s: %v", composeFile, err)
	}
	if !bytes.Equal(fromAbsolute, fromRelative) {
		t.Errorf("the absolute build wrote different bytes than the relative one.\nrelative:\n%s\nabsolute:\n%s",
			fromRelative, fromAbsolute)
	}
	if _, err := os.Stat(filepath.Join(root, "dist")); !os.IsNotExist(err) {
		t.Errorf("the absolute build wrote under the launch root's own dist folder (stat: %v)", err)
	}
}
