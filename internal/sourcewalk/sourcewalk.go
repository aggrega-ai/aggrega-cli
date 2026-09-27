// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package sourcewalk decides which files under an entity's local source directory
// belong to the entity, and is the one walk every reading of such a directory
// goes through: the declared set a publication carries and the pin step pins, a
// build's comparison, and the tree a render hands a provider.
//
// It holds no opinion about any ecosystem. The rules it applies come from two
// places and are composed in one order — the patterns and ignore-file names the
// plugin serving the entity's capability declares, then the author's own
// .aggregaignore beside the entity — and once composed they are recorded in the
// entity's lock, so every later walk applies the recorded list rather than
// composing it again. What this package adds is the matching, which is gitignore
// syntax read through the same go-git package the language server's workspace
// walk reads .gitignore files with, and the refusal of a symbolic link.
package sourcewalk

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// AuthorIgnoreFile is the name of the author's own ignore file: one file, beside
// the entity document, in gitignore syntax. Its lines are the last rules the pin
// step composes, so the author has the final say on any path they name, and it
// travels in the lock that records them rather than as a file of its own.
//
// The name follows the convention of the ecosystems whose content an entity
// carries — a chart's .helmignore, a module's .terraformignore — so an author who
// knows one of them can predict it.
const AuthorIgnoreFile = ".aggregaignore"

// withinEntityDirectory is the IgnoreRule.Within spelling of the entity's own
// directory.
const withinEntityDirectory = "."

// ErrSymbolicLinkInEntityFiles is the loud halt raised where a symbolic link sits
// among the files an entity carries or a render reads: inside the local source
// directory an entity declares, at a path an entity names directly for content
// it carries, or beside the entity document a publication packs.
//
// A link is refused rather than followed, because following it would publish the
// bytes it points to under the link's own name — from wherever it points,
// including outside the entity's directory — into an artifact that is then
// digested and, where signing applies, signed. It is refused rather than skipped,
// because skipping it would publish an artifact whose content silently differs
// from the author's directory. Every shape of link is one condition: a link to a
// file, to a directory, one pointing nowhere, one pointing at itself and two
// pointing at each other are all refused the same way.
//
// Category: CATALOG_ERROR — the fault is in the entity's directory. Match with
// errors.Is(err, ErrSymbolicLinkInEntityFiles()).
func ErrSymbolicLinkInEntityFiles() error { return errSymbolicLinkInEntityFiles }

var errSymbolicLinkInEntityFiles = cerrors.Register("symbolic-link-in-entity-files", cerrors.Meta{
	ShortCode: "E0309",
	Message:   "a symbolic link sits among the files an entity carries",
	RulePlace: cerrors.RuleNoStageReaches("The rule is evaluated wherever the files of an entity's directory are " +
		"read: by the pin step, by a publication before it packs the entity, by a pull or a verifying pull holding " +
		"the lock beside an entity to its files, by a build comparing a local source with its pins, and by a render " +
		"handing a provider the tree it reads. Each of those reads the directory on disk rather than a document a " +
		"compilation holds, so no declared stage holds the entry the rule is judged against."),
})

// linkRemediation is what the refusal tells its reader. It names both ways out,
// because which applies depends on where the link sits: a link the author needs
// the content of is replaced by that content, and a link met while walking a
// directory can be left out instead.
const linkRemediation = "remediation: replace the link with a copy of what it points to, placed inside the " +
	"entity's own directory. A link met inside a source directory can instead be left out with a line in the " +
	AuthorIgnoreFile + " beside the entity, which takes effect when the entity is pinned again. A link is refused " +
	"rather than followed because the bytes it points to would travel under the link's name, from wherever it points"

// symbolicLink composes the refusal for the link at abs, naming it as shown.
func symbolicLink(abs, shown string) error {
	condition := ErrSymbolicLinkInEntityFiles()
	return cerrors.Raise(errSymbolicLinkInEntityFiles, cerrors.CatalogError,
		cerrors.SourceLocation{File: abs},
		cerrors.ComposeMessage(condition, []string{shown}, linkRemediation), nil)
}

// RefuseLink returns the refusal for a symbolic link at abs, shown to the reader
// as shown, and nil for anything else — including a path that does not exist,
// whose absence is judged by whoever reads it. It is the one test a caller naming
// a single path directly asks, so a file an entity names and a file a walk finds
// are refused by one rule in one sentence.
func RefuseLink(abs, shown string) error {
	info, err := os.Lstat(abs)
	if err != nil {
		return nil
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return symbolicLink(abs, shown)
	}
	return nil
}

// Scope is a composed rule list read against the directory it is relative to:
// the entity's own directory, which every IgnoreRule.Within is spelled against.
// The zero Scope keeps everything.
type Scope struct {
	base    string
	matcher gitignore.Matcher
}

// NewScope compiles rules, relative to the directory base, into a Scope. The
// rules apply in the order given and a later match overrides an earlier one.
func NewScope(base string, rules []types.IgnoreRule) Scope {
	s := Scope{base: base}
	if len(rules) == 0 {
		return s
	}
	patterns := make([]gitignore.Pattern, 0, len(rules))
	for _, r := range rules {
		patterns = append(patterns, gitignore.ParsePattern(r.Pattern, withinComponents(r.Within)))
	}
	s.matcher = gitignore.NewMatcher(patterns)
	return s
}

// withinComponents splits an IgnoreRule.Within into the path components a
// pattern's domain is expressed in; the entity's own directory is no component.
func withinComponents(within string) []string {
	clean := path.Clean(filepath.ToSlash(within))
	if clean == withinEntityDirectory || clean == "" {
		return nil
	}
	return strings.Split(clean, "/")
}

// Excluded reports whether the entry at abs is left out by the scope's rules.
// An entry outside the scope's directory is judged by no rule and is kept.
func (s Scope) Excluded(abs string, isDir bool) bool {
	if s.matcher == nil {
		return false
	}
	parts, ok := s.components(abs)
	if !ok {
		return false
	}
	return s.matcher.Match(parts, isDir)
}

// components spells abs as the path components relative to the scope's
// directory, and reports false for the directory itself or a path outside it.
func (s Scope) components(abs string) ([]string, bool) {
	rel, err := filepath.Rel(s.base, abs)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return nil, false
	}
	return strings.Split(filepath.ToSlash(rel), "/"), true
}

// Entry is one entry a walk keeps: its slash-separated path relative to the
// directory walked, and whether it is a directory.
type Entry struct {
	Path  string
	IsDir bool
}

// Entries walks the directory root and returns every entry beneath it the
// scope's rules keep, in lexical path order, each relative to root.
//
// A directory the rules exclude is not entered, so nothing beneath it is
// returned, whatever a later rule says about a file inside it — the directory has
// to be brought back first, as in git. A symbolic link is refused with
// [ErrSymbolicLinkInEntityFiles] unless the rules leave it out, in which case it
// is passed over like any other excluded entry; it is never followed. root itself
// is not an entry. show spells an entry's path for a refusal, so each caller can
// name a link the way its reader knows the directory.
func (s Scope) Entries(root string, show func(rel string) string) ([]Entry, error) {
	var out []Entry
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.Type()&fs.ModeSymlink != 0 {
			if s.Excluded(p, false) || s.Excluded(p, true) {
				return nil
			}
			shown := rel
			if show != nil {
				shown = show(rel)
			}
			return symbolicLink(p, shown)
		}
		if s.Excluded(p, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		out = append(out, Entry{Path: rel, IsDir: d.IsDir()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Files is [Scope.Entries] narrowed to the files it keeps, each spelled
// relative to the scope's own directory rather than to root — the path a
// publication carries it under.
func (s Scope) Files(root string, show func(rel string) string) ([]string, error) {
	entries, err := s.Entries(root, show)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		rel, err := filepath.Rel(s.base, filepath.Join(root, filepath.FromSlash(e.Path)))
		if err != nil {
			return nil, err
		}
		files = append(files, filepath.ToSlash(rel))
	}
	sort.Strings(files)
	return files, nil
}

// Compose builds the rule list the pin step records for the local source
// directory dir of the entity whose document sits in base: the patterns the
// serving plugin declares, scoped to dir; then the lines of every ignore file
// the plugin names that the walk of dir finds, each scoped to the directory
// holding it, a directory's own file before those beneath it; then the lines of
// the author's .aggregaignore in base, scoped to base. A later rule overrides an
// earlier one, so the author's file has the last word on any path it names, and
// a chart's own ignore file refines the plugin's defaults for that chart.
//
// declared is the serving plugin's declaration, or nil where no plugin served the
// capability. A directory the rules composed so far exclude is not entered in
// search of ignore files, and the author's lines count towards that from the
// start. A symbolic link found on the way is refused as [Scope.Entries] refuses
// it.
func Compose(base, dir string, declared *types.PluginSourceIgnore) ([]types.IgnoreRule, error) {
	within := withinEntityDirectory
	if rel, err := filepath.Rel(base, dir); err == nil && rel != "." && filepath.IsLocal(rel) {
		within = filepath.ToSlash(rel)
	}
	var static []types.IgnoreRule
	var fileNames []string
	if declared != nil {
		for _, p := range declared.Patterns {
			static = append(static, types.IgnoreRule{Pattern: p, Within: within, From: types.IgnoreRuleFromPlugin})
		}
		fileNames = declared.Files
	}
	author, err := readRules(base, filepath.Join(base, AuthorIgnoreFile))
	if err != nil {
		return nil, err
	}

	var found []types.IgnoreRule
	scopeNow := func() Scope {
		all := make([]types.IgnoreRule, 0, len(static)+len(found)+len(author))
		all = append(all, static...)
		all = append(all, found...)
		all = append(all, author...)
		return NewScope(base, all)
	}
	var descend func(d string) error
	descend = func(d string) error {
		for _, name := range fileNames {
			rules, err := readRules(base, filepath.Join(d, name))
			if err != nil {
				return err
			}
			found = append(found, rules...)
		}
		entries, err := os.ReadDir(d)
		if err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, e := range entries {
			if !e.IsDir() || e.Type()&fs.ModeSymlink != 0 {
				continue
			}
			child := filepath.Join(d, e.Name())
			if scopeNow().Excluded(child, true) {
				continue
			}
			if err := descend(child); err != nil {
				return err
			}
		}
		return nil
	}
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		if err := descend(dir); err != nil {
			return nil, err
		}
	}
	out := make([]types.IgnoreRule, 0, len(static)+len(found)+len(author))
	out = append(out, static...)
	out = append(out, found...)
	out = append(out, author...)
	return out, nil
}

// readRules reads the ignore file at file, returning one rule per pattern line
// scoped to the directory holding it and naming the file it came from, both
// relative to base. A file that is not there contributes nothing. A link at the
// file's own path is refused, because what it points to would decide which
// files travel.
func readRules(base, file string) ([]types.IgnoreRule, error) {
	info, err := os.Lstat(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	fromRel, relErr := filepath.Rel(base, file)
	if relErr != nil {
		return nil, relErr
	}
	from := filepath.ToSlash(fromRel)
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, symbolicLink(file, from)
	}
	if info.IsDir() {
		return nil, nil
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	lines, err := PatternLines(f)
	if err != nil {
		return nil, fmt.Errorf("read the ignore file %s: %w", from, err)
	}
	within := path.Dir(from)
	rules := make([]types.IgnoreRule, 0, len(lines))
	for _, l := range lines {
		rules = append(rules, types.IgnoreRule{Pattern: l, Within: within, From: from})
	}
	return rules, nil
}

// PatternLines reads the pattern lines of an ignore file in gitignore syntax, in
// order: every line but a blank one and one whose first non-blank character opens
// a comment with #. A line is returned as written, so the pattern parser decides
// what its surrounding spaces mean. It is the one reading of an ignore file's
// lines, shared by the entity walk and the language server's workspace walk, so
// the two cannot come to disagree about which lines of one file are patterns.
func PatternLines(r io.Reader) ([]string, error) {
	var lines []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}
