// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// promptVariable matches a variable written in the Windows command prompt's own
// notation, %NAME%, which that prompt and Explorer expand and PowerShell does not.
// Upper case only, as environment variables are written, so a pair of format
// verbs such as "%s%" in a text that quotes one is not taken for a variable.
var promptVariable = regexp.MustCompile(`%[A-Z_][A-Z0-9_]*%`)

// TestHelpWritesWindowsLocationsInWords walks the whole command tree and reads
// every text its help shows — each command's short and long description and its
// examples, and the usage of every flag it takes — for a variable written in the
// command prompt's notation. A location under such a variable is said in words
// instead, naming the variable the way the reader sets it, so the sentence reads
// the same at every prompt.
//
// The two Windows locations the configuration help states are asserted in their
// words as well. They are what makes the walk's zero mean something: a walk that
// reached no command, or read none of its text, would find no notation either,
// and fails here instead.
func TestHelpWritesWindowsLocationsInWords(t *testing.T) {
	t.Parallel()
	root := newRootCmd()

	type helpText struct{ where, text string }
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		texts := []helpText{{"short", cmd.Short}, {"long", cmd.Long}, {"example", cmd.Example}}
		for _, flags := range []*pflag.FlagSet{cmd.LocalFlags(), cmd.PersistentFlags()} {
			flags.VisitAll(func(f *pflag.Flag) { texts = append(texts, helpText{"--" + f.Name, f.Usage}) })
		}
		for _, h := range texts {
			if found := promptVariable.FindAllString(h.text, -1); len(found) > 0 {
				t.Errorf("%q help (%s) writes %v in the command prompt's notation; name the variable in words", cmd.CommandPath(), h.where, found)
			}
		}
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(root)

	for _, want := range []struct {
		path, words string
	}{
		{path: "aggrega config", words: `aggrega\config.yaml inside the directory the APPDATA environment variable names`},
		{path: "aggrega config init", words: `aggrega\cache inside the directory the LOCALAPPDATA environment variable names`},
	} {
		cmd := findCommandByPath(root, want.path)
		if cmd == nil {
			t.Errorf("the walk has no %q command to read", want.path)
			continue
		}
		// Compared with the line breaks taken out, since where the help wraps is
		// not what is being asserted.
		if long := strings.Join(strings.Fields(cmd.Long), " "); !strings.Contains(long, want.words) {
			t.Errorf("%q help does not state its Windows location in words (%q):\n%s", want.path, want.words, cmd.Long)
		}
	}
}
