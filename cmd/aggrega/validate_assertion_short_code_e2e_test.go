// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestValidateCmd_AFailedAssertionWarningCarriesTheConditionsShortCode drives
// `aggrega validate <recipe>` through the real command tree over a Recipe whose
// assertion fails in a build of one, and reads the warning line a user reads: it
// opens its head with the assertion condition's short code — the token a build of
// the same Recipe halts with — so the condition can be looked up from the check as
// from a build. The build of a Release leaving the value unset is the control that
// the code is the build's own.
//
// The code is read off the condition's accessor and the build's halt, never
// written out, so a renumbered condition cannot leave this asserting a stale code.
//
// Not parallel: the warning is read off the process's standard error, and the test
// moves the working directory. The workspace is offline, so nothing reaches a
// registry.
func TestValidateCmd_AFailedAssertionWarningCarriesTheConditionsShortCode(t *testing.T) {
	code, ok := cerrors.ShortCodeOf(renderer.ErrAssertionFailed())
	if !ok || code == "" {
		t.Fatal("the assertion condition reports no short code")
	}

	root, catalogRoot, releasePath, recipePath := assertionWorkspace(t, "")
	writeWorkspaceConfig(t, root, "workspace: {}\noffline: true")
	t.Chdir(root)

	var out string
	var err error
	stderr := captureStderr(t, func() {
		out, err = runAggrega(t, "validate", recipePath, "--catalog", catalogRoot)
	})
	if err != nil {
		t.Fatalf("validate failed the Recipe rather than warning about its assertion: %v\noutput:\n%s\nstderr:\n%s", err, out, stderr)
	}
	var warning string
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "  warning: ") && strings.Contains(line, imageChosenFailure) {
			warning = line
		}
	}
	if warning == "" {
		t.Fatalf("stderr carries no warning line naming the assertion:\n%s", stderr)
	}
	head := regexp.MustCompile(`\[([A-Z_]+) ` + regexp.QuoteMeta(code) + `\] `)
	m := head.FindStringSubmatch(warning)
	if m == nil {
		t.Fatalf("the warning carries no head naming the assertion condition %s:\n%s", code, warning)
	}

	// The control: a build of a Release leaving the value unset halts on the same
	// assertion, and its head carries the same category and code.
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	if err := pullRun(context.Background(), pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogRoot}, ws: ws}); err != nil {
		t.Fatalf("prime lockfile via pull: %v", err)
	}
	var diag, buildOut strings.Builder
	berr := dispatchBuild(context.Background(), ws, releasePath, []string{catalogRoot}, false, &diag, &buildOut)
	if berr == nil {
		t.Fatal("the build returned nil; want it to halt on the assertion")
	}
	if printed := formatCompilerError(berr); !strings.Contains(printed, "["+m[1]+" "+code+"]") {
		t.Errorf("the build's head is not the warning's [%s %s]:\n%s", m[1], code, printed)
	}
}
