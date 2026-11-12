// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/clisurface"
	"github.com/aggrega-ai/aggrega-cli/internal/opprogress"
)

// reporterHomes are the only functions of this package that may make a progress
// reporter: the one constructor for a command drawing its own progress, and the
// shared surface every other waiting command is given.
var reporterHomes = []string{"newOwnProgress", "showsOperationProgress"}

// TestEveryCommandThatDrawsItsOwnProgressDrawsItsWaits holds the commands whose
// operation's row says they draw their own progress (clisurface.DrawsOwnProgress)
// to the one constructor that draws their operation's waits (newOwnProgress). It
// reads this package's non-test source and requires:
//
//   - the operations named in calls of newOwnProgress to be exactly the operations
//     whose row says their command draws its own progress, naming any missing or
//     extra by set difference rather than comparing counts;
//   - each such call to name its operation as a literal, so the set can be read;
//   - no other function of the package to make a reporter (newProgressReporter),
//     so a command cannot draw its own progress around the constructor.
//
// Planting either bypass fails it, naming what was planted: `build` making its
// reporter with newProgressReporter rather than newOwnProgress leaves "build"
// missing and names the stray call; a new row declared DrawsOwnProgress whose
// command makes no such call is named missing.
func TestEveryCommandThatDrawsItsOwnProgressDrawsItsWaits(t *testing.T) {
	t.Parallel()
	var want []string
	for id := range clisurface.OperationAvailability() {
		if display, _ := clisurface.OperationWaitDisplay(id); display == clisurface.DrawsOwnProgress {
			want = append(want, id)
		}
	}
	slices.Sort(want)
	if len(want) == 0 {
		t.Fatal("no operation's row says its command draws its own progress, so this check holds nothing")
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var got []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, perr := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Fatalf("parse %s: %v", name, perr)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				callee, ok := call.Fun.(*ast.Ident)
				if !ok {
					return true
				}
				switch callee.Name {
				case "newProgressReporter":
					if !slices.Contains(reporterHomes, fn.Name.Name) {
						t.Errorf("%s: %s makes a progress reporter of its own; a command drawing its own progress makes it with newOwnProgress",
							fset.Position(call.Pos()), fn.Name.Name)
					}
				case "newOwnProgress":
					if len(call.Args) < 2 {
						return true
					}
					lit, ok := call.Args[1].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						t.Errorf("%s: newOwnProgress is called with an operation that is not a literal, so the set of commands drawing their waits cannot be read",
							fset.Position(call.Pos()))
						return true
					}
					id, uerr := strconv.Unquote(lit.Value)
					if uerr != nil {
						t.Fatal(uerr)
					}
					got = append(got, id)
				}
				return true
			})
		}
	}
	slices.Sort(got)
	got = slices.Compact(got)
	for _, id := range want {
		if !slices.Contains(got, id) {
			t.Errorf("operation %q draws its own progress, and its command makes no reporter with newOwnProgress, so its waits are drawn nowhere", id)
		}
	}
	for _, id := range got {
		if !slices.Contains(want, id) {
			t.Errorf("newOwnProgress is called for %q, whose row does not say its command draws its own progress", id)
		}
	}
}

// TestACommandDrawingItsOwnProgressShowsWaitsAndNoSecondStep drives the
// observer a command drawing its own progress gives its operation, over a plain
// reporter writing to a buffer, and requires a wait the operation reports
// (opprogress.Wait) to be written, and a step it reports (opprogress.Begin) — the
// plugin loader's compile, say, which the command's own spine already draws — to
// be written nowhere. A reporter that drew the step as well would write the
// command's compile row twice; one that drew neither would leave a wait on a busy
// registry silent.
//
// It also holds a silent reporter to leaving the caller's context as it was, so a
// daemon request or an in-process assistant tool keeps its own observer.
func TestACommandDrawingItsOwnProgressShowsWaitsAndNoSecondStep(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	_, ctx := newOwnProgress(context.Background(), "pull", &buf, buildDiagOptions{progress: true}, pullProgressSpine())
	opprogress.Begin(ctx, "compiling plugin example")(nil)
	opprogress.Wait(ctx, "registry.example asked to slow down; waiting 10 s")(nil)
	out := buf.String()
	if strings.Contains(out, "compiling plugin example") {
		t.Errorf("a step the operation reports was drawn beside the command's own steps:\n%s", out)
	}
	if !strings.Contains(out, "registry.example asked to slow down; waiting 10 s") {
		t.Errorf("a wait the operation reports was not drawn:\n%s", out)
	}

	type marker struct{}
	caller := context.WithValue(context.Background(), marker{}, "the caller's own")
	_, kept := newOwnProgress(caller, "pull", &bytes.Buffer{}, buildDiagOptions{}, pullProgressSpine())
	if kept != caller {
		t.Error("a reporter that draws nothing replaced the caller's context, and with it any observer the caller installed")
	}
}
