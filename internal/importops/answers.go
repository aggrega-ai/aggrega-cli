// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
	// The host uses the importer kit for one thing only: to write an answer into a
	// drafted document exactly as an importer's own writer would — its one
	// double-quoted scalar writer, and for an Aggregate its one template escape —
	// so an answered draft reads as one the importer wrote with that value.
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"

	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// AnswersFileName is the answers file an import reads and writes where it is
// named no other: beside a source file, or inside a source directory.
//
// It holds the values the drafts leave empty that no source can hold — an
// application's image, an owner's email, the name of a Secret — keyed by the
// drafted file and the key path inside it, so a later import of the same source,
// and a sync, read them from here and ask nothing. It never holds a secret: the
// key of a secret asks where the secret comes from, and only a name is accepted
// there ([AnswerIsAName]).
const AnswersFileName = "import-answers.yaml"

// DefaultAnswersPath is where an import reads and writes its answers file when it
// is named none and no record beside the drafts names one: inside the source,
// where the source is a directory, and beside it otherwise.
func DefaultAnswersPath(sourcePath string) string {
	if info, err := os.Stat(sourcePath); err == nil && info.IsDir() {
		return filepath.Join(sourcePath, AnswersFileName)
	}
	return filepath.Join(filepath.Dir(sourcePath), AnswersFileName)
}

// answers is what an answers file holds: for each drafted file, each key path
// answered, as the file writes it ([RenderKey]), and the answer, which may be
// empty.
type answers map[string]map[string]string

// get is the answer for the owed value o, and whether the file answers it with
// anything but an empty value.
func (a answers) get(o Owed) (string, bool) {
	v, ok := a[o.File][o.Key()]
	return v, ok && v != ""
}

// AnswersFileError refuses an answers file that cannot be read as one.
//
// Nothing is written: the file is the reader's, and an import that wrote over a
// file it could not read would lose what the reader put in it.
type AnswersFileError struct {
	// Path is the answers file on this host.
	Path string
	// Err is why it cannot be read as an answers file.
	Err error
}

// Error names the file and why it cannot be read.
func (e *AnswersFileError) Error() string {
	return fmt.Sprintf("the answers file %s cannot be read: %v; nothing was written", e.Path, e.Err)
}

// Unwrap returns why the file cannot be read.
func (e *AnswersFileError) Unwrap() error { return e.Err }

// SecretAnswerError refuses an answers file that answers a secret's key with
// something that is not a name.
//
// The answer is never repeated — it may be the secret itself — and nothing is
// written, so it is never carried into a draft or written back.
type SecretAnswerError struct {
	// Answers is the answers file on this host.
	Answers string
	// File is the drafted file the answer is for.
	File string
	// Key is the key path the answer is for, as the answers file writes it.
	Key string
}

// Error names the key and what a secret's answer is.
func (e *SecretAnswerError) Error() string {
	return fmt.Sprintf("the answers file %s answers %s in %s with something that is not a name, and the value there "+
		"is a secret's: it names where the secret comes from — a variable name such as OPENAI_API_KEY, a reference "+
		"such as ${OPENAI_API_KEY}, or the name of a Kubernetes Secret — and never holds the secret itself; replace "+
		"it with a name; nothing was written", e.Answers, e.Key, e.File)
}

// OwedValuesError reports an import whose drafts still leave values empty that
// no source can hold, once every answer it was given is applied.
//
// The drafts, the answers file listing each owed value under an empty answer,
// and the record are written all the same: the import did everything it could,
// and what is left is for the reader to supply. It is a refusal of its own
// rather than a registered condition, as the import's other refusals of a
// request are, and its one sentence names how many values are owed and where.
type OwedValuesError struct {
	// Owed is every value still owed, in the order the drafts are written.
	Owed []Owed
	// Answers is the answers file on this host the values are written in.
	Answers string
	// Written reports whether the answers file holds every owed key — written by
	// the import, or already as it would be written; a reporting delivery writes
	// nothing.
	Written bool
}

// Error is the one sentence: how many values, and where to write them.
func (e *OwedValuesError) Error() string {
	noun, verb := "values the drafts leave empty are", "each"
	if len(e.Owed) == 1 {
		noun, verb = "value the drafts leave empty is", "it"
	}
	place := "listed in"
	if !e.Written {
		place = "to be written in"
	}
	return fmt.Sprintf("%d %s still owed, %s the answers file %s: write %s beside its key there, then run the "+
		"import again", len(e.Owed), noun, place, e.Answers, verb)
}

// PullError reports an import whose drafts were written and whose pull of the
// drafted Release's dependencies failed.
type PullError struct {
	// Release is the drafted Release on this host.
	Release string
	// Err is why the pull failed.
	Err error
}

// Error says the drafts are on disk, why the pull failed, and what to run.
func (e *PullError) Error() string {
	return fmt.Sprintf("the drafts were written, and pulling the dependencies of the drafted Release %s failed: %v; "+
		"once that is fixed, run aggrega pull --release %s", e.Release, e.Err, e.Release)
}

// Unwrap returns why the pull failed.
func (e *PullError) Unwrap() error { return e.Err }

// Ask puts one owed value to a person and returns the answer. refused is why the
// previous answer to the same value was refused, empty on the first asking. An
// empty answer leaves the value owed; an error stops the asking, and every value
// not yet answered stays owed.
type Ask func(o Owed, refused string) (string, error)

// answerVariableName, answerVariableReference and answerKubernetesName are the
// three names a secret's answer may be.
var (
	answerVariableName      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	answerVariableReference = regexp.MustCompile(`^\$\{[A-Za-z_][A-Za-z0-9_]*\}$`)
	answerKubernetesName    = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)
)

// kubernetesNameMax is the longest name a Kubernetes object takes.
const kubernetesNameMax = 253

// AnswerIsAName reports whether an answer is a name a secret's value may be: a
// variable name, a reference to a variable written ${NAME}, or the name of a
// Kubernetes object. Anything else is refused as a secret's answer, so the
// answers file never carries what may be the secret itself.
func AnswerIsAName(answer string) bool {
	return answerVariableName.MatchString(answer) || answerVariableReference.MatchString(answer) ||
		(len(answer) <= kubernetesNameMax && answerKubernetesName.MatchString(answer))
}

// secretAnswerRefusal is what a person answering a secret's question with
// something that is not a name is told.
const secretAnswerRefusal = "that is not a name: a secret's answer names where the secret comes from — a variable " +
	"name, ${NAME}, or the name of a Kubernetes Secret — and never the secret itself"

// readAnswers reads the answers file at path. A file that does not exist answers
// nothing. Anything but a mapping of drafted file paths, each to a mapping of key
// paths to scalar answers, is refused.
func readAnswers(path string) (answers, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return answers{}, false, nil
	}
	if err != nil {
		return nil, false, &AnswersFileError{Path: path, Err: err}
	}
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	var root yaml.Node
	if err := dec.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return answers{}, true, nil
		}
		return nil, false, &AnswersFileError{Path: path, Err: err}
	}
	out := answers{}
	top := root.Content[0]
	if top.Kind == yaml.ScalarNode && top.Tag == "!!null" {
		return out, true, nil
	}
	if top.Kind != yaml.MappingNode {
		return nil, false, &AnswersFileError{Path: path, Err: fmt.Errorf("its top level is not a mapping of drafted files")}
	}
	for i := 0; i+1 < len(top.Content); i += 2 {
		file, keys := top.Content[i].Value, top.Content[i+1]
		if keys.Kind == yaml.ScalarNode && keys.Tag == "!!null" {
			continue
		}
		if keys.Kind != yaml.MappingNode {
			return nil, false, &AnswersFileError{Path: path,
				Err: fmt.Errorf("%s holds no mapping of key paths to answers", file)}
		}
		byKey := out[file]
		if byKey == nil {
			byKey = map[string]string{}
			out[file] = byKey
		}
		for j := 0; j+1 < len(keys.Content); j += 2 {
			key, value := keys.Content[j].Value, keys.Content[j+1]
			if value.Kind != yaml.ScalarNode {
				return nil, false, &AnswersFileError{Path: path,
					Err: fmt.Errorf("the answer to %s in %s is not a single value", key, file)}
			}
			if value.Tag == "!!null" {
				byKey[key] = ""
				continue
			}
			byKey[key] = strings.TrimSpace(value.Value)
		}
	}
	return out, true, nil
}

// answered is the outcome of answering the owed values: the drafts with every
// answer written in, the answers for the file, the values still owed, and the
// information line naming the answers these drafts do not ask for.
type answered struct {
	files     []abi.ScaffoldFile
	given     answers
	remaining []Owed
	notices   []abi.Diagnostic
}

// answerOwed answers each owed value from the answers file read, and where ask is
// set, from a person for each the file leaves unanswered, and writes every
// answer into the drafts.
//
// A secret's value is answered only with a name: the file answering one with
// anything else refuses the import, naming the key and never the answer, and a
// person answering one so is told why and asked again. An answer the file gives
// for a key these drafts do not leave empty is kept in the file and not applied,
// and one information line names each: one answers file serves the drafts of
// every target, and a value one target owes another does not.
func answerOwed(files []abi.ScaffoldFile, owed []Owed, fromFile answers, path string, ask Ask) (answered, error) {
	out := answered{given: answers{}}
	owes := map[string]map[string]bool{}
	for _, o := range owed {
		if owes[o.File] == nil {
			owes[o.File] = map[string]bool{}
		}
		owes[o.File][o.Key()] = true
	}
	var unasked []string
	for _, file := range slices.Sorted(maps.Keys(fromFile)) {
		for _, key := range slices.Sorted(maps.Keys(fromFile[file])) {
			if !owes[file][key] && fromFile[file][key] != "" {
				unasked = append(unasked, key+" in "+file)
			}
		}
	}
	if len(unasked) > 0 {
		out.notices = append(out.notices, infoDiagnostic(fmt.Sprintf("the answers file %s also answers %s, which "+
			"these drafts do not leave empty; those answers are kept there and not applied", path,
			strings.Join(unasked, ", "))))
	}

	asking := ask != nil
	for _, o := range owed {
		answer, ok := fromFile.get(o)
		if ok && o.Sensitive && !AnswerIsAName(answer) {
			return answered{}, &SecretAnswerError{Answers: path, File: o.File, Key: o.Key()}
		}
		if !ok && asking {
			refused := ""
			for {
				given, err := ask(o, refused)
				if err != nil {
					asking = false
					break
				}
				given = strings.TrimSpace(given)
				if given == "" {
					break
				}
				if o.Sensitive && !AnswerIsAName(given) {
					refused = secretAnswerRefusal
					continue
				}
				answer, ok = given, true
				break
			}
		}
		if out.given[o.File] == nil {
			out.given[o.File] = map[string]string{}
		}
		if !ok {
			out.given[o.File][o.Key()] = ""
			out.remaining = append(out.remaining, o)
			continue
		}
		out.given[o.File][o.Key()] = answer
	}

	out.files = make([]abi.ScaffoldFile, 0, len(files))
	for _, f := range files {
		content := f.Content
		for _, o := range owed {
			if o.File != f.Path {
				continue
			}
			answer := out.given[o.File][o.Key()]
			if answer == "" {
				continue
			}
			written, err := writeAnswer(content, o.Keys, answer)
			if err != nil {
				return answered{}, fmt.Errorf("write the answer to %s into %s: %w", o.Key(), o.File, err)
			}
			content = written
		}
		out.files = append(out.files, abi.ScaffoldFile{Path: f.Path, Content: content})
	}
	return out, nil
}

// writeAnswer writes answer in place of the empty value at keys in content,
// changing no other byte: the empty quoted scalar the draft wrote there is
// replaced by the answer as a double-quoted scalar.
func writeAnswer(content string, keys []string, answer string) (string, error) {
	top, err := readDraft(content)
	if err != nil {
		return "", err
	}
	node := top
	for _, k := range keys {
		_, next := mappingEntry(node, k)
		if next == nil {
			return "", fmt.Errorf("the draft holds no key %s", RenderKey(keys))
		}
		node = next
	}
	if node.Kind != yaml.ScalarNode || node.Value != "" {
		return "", fmt.Errorf("the value at %s is not empty", RenderKey(keys))
	}
	lines := strings.SplitAfter(content, "\n")
	if node.Line < 1 || node.Line > len(lines) {
		return "", fmt.Errorf("the value at %s lies outside the draft", RenderKey(keys))
	}
	line := lines[node.Line-1]
	runes := []rune(line)
	at := node.Column - 1
	if at < 0 || at+2 > len(runes) {
		return "", fmt.Errorf("the value at %s lies outside its line", RenderKey(keys))
	}
	if quoted := string(runes[at : at+2]); quoted != `""` && quoted != `''` {
		return "", fmt.Errorf("the value at %s is not written as an empty quoted string", RenderKey(keys))
	}
	// The answer is written through the drafts' one double-quoted scalar writer.
	// A drafted Aggregate's text is evaluated as a template before it is read, so
	// an answer written into one is escaped first, as every value an importer
	// copies into an Aggregate is, and reads back as typed.
	if draftKind(content) == entitykind.Aggregate {
		answer = importer.EscapeTemplateText(answer)
	}
	lines[node.Line-1] = string(runes[:at]) + importer.QuotedString(answer) + string(runes[at+2:])
	return strings.Join(lines, ""), nil
}

// answersHeader opens every answers file, saying what it is to a reader who
// finds it beside their source.
const answersHeader = "Values the import could not draft from the source, keyed by the drafted file and the " +
	"key path inside it. Write each one beside its key; an import of the same source, and a sync, read them " +
	"from here and ask nothing. A secret is never written here: its key asks where the secret comes from — a " +
	"variable name, ${NAME}, or the name of a Kubernetes Secret."

// answersFileContent composes the answers file for the owed values from the file
// as it stands, existing — empty where there is none — and reports whether it
// differs from it. Each owed value sits under its drafted file, with its own
// description written above its key and the answer given, or an empty one.
//
// What the file already holds that this import does not own is kept, in its
// order: another target's answers, a key the source no longer drafts, a comment
// the reader wrote. An owed key the file lacks is added after its file's other
// keys, and an owed key whose answer was given here is set; nothing else moves,
// so a file already holding every owed key with its answer is reported unchanged
// and is not written. A file that does not read as an answers file was refused
// before this runs.
func answersFileContent(existing []byte, owed []Owed, given answers) (string, bool, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(existing, &doc); err != nil {
		return "", false, err
	}
	root, top, changed := &doc, (*yaml.Node)(nil), false
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 && doc.Content[0].Kind == yaml.MappingNode {
		top = doc.Content[0]
	} else {
		top = &yaml.Node{Kind: yaml.MappingNode, HeadComment: foldAnswersComment(answersHeader)}
		root, changed = &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{top}}, true
	}
	for _, o := range owed {
		_, files := mappingEntry(top, o.File)
		if files == nil || files.Kind != yaml.MappingNode {
			files = &yaml.Node{Kind: yaml.MappingNode}
			top.Content = append(top.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: o.File}, files)
			changed = true
		}
		answer := given[o.File][o.Key()]
		if _, value := mappingEntry(files, o.Key()); value != nil {
			if strings.TrimSpace(value.Value) != answer {
				*value = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Style: yaml.DoubleQuotedStyle, Value: answer,
					LineComment: value.LineComment, FootComment: value.FootComment}
				changed = true
			}
			continue
		}
		comment := o.Question()
		if o.Sensitive {
			comment += " A secret: name where it comes from, never the value."
		}
		files.Content = append(files.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: o.Key(), HeadComment: foldAnswersComment(comment)},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Style: yaml.DoubleQuotedStyle, Value: answer})
		changed = true
	}
	if !changed {
		return string(existing), false, nil
	}
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return "", false, err
	}
	if err := enc.Close(); err != nil {
		return "", false, err
	}
	return b.String(), true, nil
}

// answersCommentWidth is the width a comment in the answers file is wrapped at.
const answersCommentWidth = 100

// foldAnswersComment folds text into comment lines no wider than answersCommentWidth,
// breaking only between words.
//
// 🔴 DELIBERATE NEAR-DUPLICATE of the fold the constraint fact generator writes Go
// comments with (wrapComment in internal/compiler/constraint/factgen). That one is
// part of a generator whose committed output a drift gate compares byte for byte:
// it measures bytes, splits paragraphs and writes the Go comment marker itself.
// This one measures characters for a person reading the answers file, and hands
// bare lines to the YAML encoder, which writes the marker. Sharing one fold would
// either move the generated bytes or wrap a reader's text by bytes; neither
// belongs to the other reader.
func foldAnswersComment(text string) string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		if line != "" && len([]rune(line))+1+len([]rune(word)) > answersCommentWidth {
			lines = append(lines, line)
			line = word
			continue
		}
		if line == "" {
			line = word
		} else {
			line += " " + word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// writeAnswersFile writes the answers file at path, held — with its links
// resolved — to the root a file there is written beneath against the workspace
// at workspaceRoot: the workspace root where it lies inside the workspace, and
// its own directory where it lies outside it.
func writeAnswersFile(workspaceRoot, path, content string) error {
	root := writescope.RootFor(workspaceRoot, filepath.Dir(path))
	if err := writescope.CheckWrite(root, path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create the directory of the answers file %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write the answers file %s: %w", path, err)
	}
	return nil
}

// answersPointer is the answers file as the record beside the drafts names it:
// its path relative to the output directory, slash-separated, or its whole path
// where no relative one leads to it.
func answersPointer(outputDir, path string) string {
	if rel, err := filepath.Rel(outputDir, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}
