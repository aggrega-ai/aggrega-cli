// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package skills

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestInstallAsksTheGuardAboutEveryPathBeforeWriting drives the guard both ways.
//
// A guard refusing the second assistant's file must leave the first assistant's
// folder unwritten too, because every target is checked before anything is
// written; a guard consulted per folder as it was written would pass the
// refusal's message and leave half an installation behind. The accepting run is
// the positive control, and it also records which paths were asked about, so a
// guard that was never called cannot pass as one that said yes.
func TestInstallAsksTheGuardAboutEveryPathBeforeWriting(t *testing.T) {
	t.Parallel()

	t.Run("a refusal for one target writes no target", func(t *testing.T) {
		t.Parallel()
		base := t.TempDir()
		refused := filepath.Join(base, ".claude", "skills", InstalledSkill, "SKILL.md")
		errRefused := errors.New("held outside")

		_, err := Install(InstallRequest{
			Skill:  InstalledSkill,
			Agents: []string{"codex", "claude-code"},
			Base:   base,
			Guard: func(p string) error {
				if p == refused {
					return errRefused
				}
				return nil
			},
		})
		if !errors.Is(err, errRefused) {
			t.Fatalf("err = %v; want the guard's refusal", err)
		}
		entries, readErr := os.ReadDir(base)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if len(entries) != 0 {
			t.Errorf("a refused installation wrote %d entr(ies) under the base", len(entries))
		}
	})

	t.Run("an accepting guard is asked about each folder and file", func(t *testing.T) {
		t.Parallel()
		base := t.TempDir()
		var asked []string

		if _, err := Install(InstallRequest{
			Skill:  InstalledSkill,
			Agents: []string{"claude-code", "codex"},
			Base:   base,
			Guard: func(p string) error {
				rel, err := filepath.Rel(base, p)
				if err != nil {
					return err
				}
				asked = append(asked, filepath.ToSlash(rel))
				return nil
			},
		}); err != nil {
			t.Fatalf("Install: %v", err)
		}
		sort.Strings(asked)
		want := []string{
			".agents/skills/aggrega",
			".agents/skills/aggrega/SKILL.md",
			".claude/skills/aggrega",
			".claude/skills/aggrega/SKILL.md",
		}
		if strings.Join(asked, "|") != strings.Join(want, "|") {
			t.Errorf("the guard was asked about %v; want %v", asked, want)
		}
	})
}
