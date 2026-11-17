// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// The ways a part can drift from what the source drafts now.
const (
	// DriftAdded is a part the source gained.
	DriftAdded = "added"
	// DriftRemoved is a part the source dropped.
	DriftRemoved = "removed"
	// DriftChanged is a part the source changed.
	DriftChanged = "changed"
)

// Drift is one part a sync brings up to date with the source: a drafted file,
// or, inside a drafted Aggregate or Release, one of its entries.
//
// Change is [DriftAdded], [DriftRemoved] or [DriftChanged]. File is the drafted
// path, relative to the directory the drafted paths are relative to. Key is the
// keys leading to the entry from the top of the document, and is empty where the
// part is the whole file.
type Drift struct {
	Change string
	File   string
	Key    []string
}

// DriftLine is the one sentence every surface prints for a part that drifted.
func DriftLine(d Drift) string {
	if len(d.Key) == 0 {
		return d.Change + " " + d.File
	}
	return d.Change + " " + RenderKey(d.Key) + " in " + d.File
}

// SyncConflict is one part a sync would have to change or remove although an
// author wrote or edited it since the import wrote it, or one the source gained
// where an author already wrote something.
//
// File and Key name the part as a [Drift] does; Reason says what meets there.
type SyncConflict struct {
	File   string
	Key    []string
	Reason string
}

// What meets at a part that stops a sync.
const (
	conflictEditedChanged  = "edited since the import wrote it, and the source changes it"
	conflictEditedDropped  = "edited since the import wrote it, and the source no longer has it"
	conflictRemovedChanged = "removed since the import wrote it, and the source changes it"
	conflictAuthorGained   = "written by an author, and the source now drafts it"
	conflictAuthorInside   = "written by an author inside a part the source no longer has"
	conflictAuthorLines    = "holds lines an author wrote, and the source no longer has it"
	conflictUnplaceable    = "added by the source, and what would hold it is no longer the mapping the import wrote"
	conflictUnreadable     = "no longer reads as the YAML mapping the import wrote, and the source changes it"
	conflictNotRegularFile = "no longer a file, and the source changes it"
)

// ConflictLine is the one sentence every surface prints for a conflicting part.
func ConflictLine(c SyncConflict) string {
	if len(c.Key) == 0 {
		return c.File + ": " + c.Reason
	}
	return c.File + " " + RenderKey(c.Key) + ": " + c.Reason
}

// SyncConflictError reports a sync refused because bringing the drafts up to
// date would change or remove what an author wrote. No draft is written; an
// answer a person gave before the conflict was judged is already in the answers
// file, so the next sync asks for it no more.
//
// It carries every conflicting part, in the order the draft names them, so the
// reader can reconcile them all before syncing again rather than one per run.
// Its message names the command line's flag; a protocol surface recognises the
// refusal by its type and words it for its own caller.
type SyncConflictError struct {
	// OutputDir is the directory the drafted paths are relative to.
	OutputDir string
	// Conflicts is every part that stops the sync.
	Conflicts []SyncConflict
}

// Error names the directory and each conflicting part.
func (e *SyncConflictError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "--sync stopped: bringing the drafts beneath %q up to date with the source would change or "+
		"remove what an author wrote, so no draft was written; reconcile each part below by hand, or put back what "+
		"the import wrote there, and sync again:", e.OutputDir)
	for _, c := range e.Conflicts {
		b.WriteString("\n  ")
		b.WriteString(ConflictLine(c))
	}
	return b.String()
}

// ImportRecordError reports a sync refused because the record an import writes
// beside its drafts cannot answer which parts an import wrote: it is not there,
// it does not list the entity the sync would bring up to date, or it cannot be
// read. Nothing is written.
//
// Path and Kind are set where an entity the sync would bring up to date — an
// Aggregate, Component, Feature, Recipe or FeatureRecipe — is on disk and no
// record lists it; Err is set where the record is there and cannot be
// read. Its message names the command line's flag; a protocol surface recognises
// the refusal by its type and words it for its own caller.
type ImportRecordError struct {
	// Record is the record's path, relative to OutputDir.
	Record string
	// OutputDir is the directory the drafted paths are relative to.
	OutputDir string
	// Path is the drafted path of the entity no record lists, relative to
	// OutputDir.
	Path string
	// Kind is the kind the file at Path declares.
	Kind entitykind.Kind
	// Err is why the record could not be read.
	Err error
}

// Error names the record and what is wrong with it.
func (e *ImportRecordError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("--sync cannot read the record of what an import wrote beneath %q, %q: %v; no files were "+
			"written", e.OutputDir, e.Record, e.Err)
	}
	return fmt.Sprintf("--sync cannot bring the %s %q beneath %q up to date with the source: no record of what "+
		"an import wrote there lists it (%q, which every import writes beside its drafts), so its parts cannot be "+
		"told from parts an author wrote; no files were written", e.Kind, e.Path, e.OutputDir, e.Record)
}

// Unwrap returns why the record could not be read.
func (e *ImportRecordError) Unwrap() error { return e.Err }

// syncPlan is what a sync would do: the files it writes, in order, the record
// last; the drafted files it deletes; and every part that drifted.
type syncPlan struct {
	writes    []abi.ScaffoldFile
	removes   []string
	drift     []Drift
	conflicts []SyncConflict
}

// readRecord reads the record beside the drafts beneath outputDir, held to the
// root the output directory picks; a record that is not there reads as nil.
//
// It also returns the record's bytes as they are on disk, so a sync rewrites the
// record only where what it would write differs from them.
//
// Every path the record lists is placed and held as a drafted path is, before
// anything is read through it or removed: the record is a file on disk anyone
// can edit, so a path in it is no more trusted than one an importer returns.
func readRecord(root, outputDir string) (*importRecord, string, error) {
	place, err := draftPlace(outputDir, RecordFileName)
	if err != nil {
		return nil, "", err
	}
	if err := writescope.CheckWrite(root, place); err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(place)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", &ImportRecordError{Record: RecordFileName, OutputDir: outputDir, Err: err}
	}
	rec, err := parseRecord(data)
	if err != nil {
		return nil, "", &ImportRecordError{Record: RecordFileName, OutputDir: outputDir, Err: err}
	}
	recordPlace := place
	for _, f := range rec.Files {
		place, err := draftPlace(outputDir, f.Path)
		if err == nil {
			err = writescope.CheckWrite(root, place)
		}
		if err != nil {
			return nil, "", &ImportRecordError{Record: RecordFileName, OutputDir: outputDir,
				Err: fmt.Errorf("it lists %q, which a sync may not read or remove: %w", f.Path, err)}
		}
		if place == recordPlace {
			return nil, "", &ImportRecordError{Record: RecordFileName, OutputDir: outputDir,
				Err: fmt.Errorf("it lists itself")}
		}
	}
	return &rec, string(data), nil
}

// syncImport brings the drafts beneath outputDir up to date with drafted, the
// fresh draft composed for target, as [Import] does under Options.Sync, and fills
// res with what it writes, what it removes and every part that drifted.
//
// rec and onDisk are the record beside the drafts and its bytes as [readRecord]
// read them — rec nil where there is none — read by [Import] before the draft was
// composed, because a sync given no target takes the one the record names. The
// record the sync writes names target, the one the draft was composed for, and
// catalogSnapshot, the published catalog snapshot it was composed against, empty
// where it read catalog folders, and answers, the answers file the draft's owed
// values were answered from, empty where the draft owes none.
//
// An Aggregate on disk at a drafted path that rec does not list was refused by
// [checkDraftPlaces] before the draft's owed values were answered.
//
// Nothing is written or removed until every part is judged, and a sync with any
// conflict writes no draft; a sync whose importer reported a halt writes nothing
// either, because a draft the importer could not finish would read as the
// source having dropped the parts it did not reach.
func syncImport(workspaceRoot, outputDir, target, catalogSnapshot, answers string, opts Options, res Result,
	drafted []draftedFile, rec *importRecord, onDisk string) (Result, error) {
	root := writescope.RootFor(workspaceRoot, outputDir)
	res.Files = nil
	if res.Halted() {
		if opts.BeforeWrite != nil {
			opts.BeforeWrite(res)
		}
		return res, nil
	}
	plan, err := planSync(outputDir, drafted, rec)
	if err != nil {
		return Result{}, err
	}
	if len(plan.conflicts) > 0 {
		return Result{}, &SyncConflictError{OutputDir: outputDir, Conflicts: plan.conflicts}
	}
	files := plan.writes
	if rec != nil || len(drafted) > 0 {
		if record := recordScaffold(target, catalogSnapshot, answers, drafted); record.Content != onDisk {
			files = append(files, record)
		}
	}
	res.Files = files
	res.Drift = plan.drift
	if opts.BeforeWrite != nil {
		opts.BeforeWrite(res)
	}
	if opts.Delivery.Reports() {
		return res, nil
	}
	written, err := writeFiles(outputDir, files)
	if err != nil {
		return Result{}, err
	}
	removed, err := removeFiles(root, outputDir, plan.removes)
	if err != nil {
		return Result{}, err
	}
	res.Written = written
	res.Removed = removed
	return res, nil
}

// recordLists reports whether the record lists a drafted path.
func recordLists(rec importRecord, path string) bool {
	for _, f := range rec.Files {
		if f.Path == path {
			return true
		}
	}
	return false
}

// removeFiles removes each drafted file a sync no longer drafts, held to root as
// every removal is, and returns the paths removed in order. A file already gone
// is not an error.
func removeFiles(root, outputDir string, paths []string) ([]string, error) {
	removed := make([]string, 0, len(paths))
	for _, p := range paths {
		place, err := draftPlace(outputDir, p)
		if err != nil {
			return nil, err
		}
		if err := writescope.CheckDelete(root, place); err != nil {
			return nil, err
		}
		if err := os.Remove(place); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("remove import file %q: %w", p, err)
		}
		removed = append(removed, p)
	}
	return removed, nil
}

// diskFile is what is on disk at a drafted path.
type diskFile struct {
	exists  bool
	regular bool
	content string
}

// readDisk reads what is at a drafted path beneath outputDir.
func readDisk(outputDir, path string) (diskFile, error) {
	place, err := draftPlace(outputDir, path)
	if err != nil {
		return diskFile{}, err
	}
	info, err := os.Stat(place)
	if errors.Is(err, fs.ErrNotExist) {
		return diskFile{}, nil
	}
	if err != nil {
		return diskFile{}, fmt.Errorf("read %q to sync it: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return diskFile{exists: true}, nil
	}
	data, err := os.ReadFile(place)
	if err != nil {
		return diskFile{}, fmt.Errorf("read %q to sync it: %w", path, err)
	}
	return diskFile{exists: true, regular: true, content: string(data)}, nil
}

// planSync judges every part the draft and the record name against what is on
// disk beneath outputDir, and plans what bringing them up to date writes.
//
// Each part is judged three ways: what the record says the import last wrote
// there, what is on disk, and what the source drafts now. A part the source left
// as the import last wrote it is left exactly as it is on disk, edited or not. A
// part the source changed is written anew where it is on disk as the import last
// wrote it, left where it already reads as the source drafts it, and a conflict
// otherwise. A part the source dropped is removed where it is as the import last
// wrote it and holds nothing an author added, and a conflict otherwise. A part
// the source gained is added where nothing is at its key, and a conflict where an
// author wrote something there.
func planSync(outputDir string, fresh []draftedFile, rec *importRecord) (syncPlan, error) {
	var plan syncPlan
	entries := map[string]*recordFile{}
	if rec != nil {
		for i := range rec.Files {
			entries[rec.Files[i].Path] = &rec.Files[i]
		}
	}
	drafted := make(map[string]bool, len(fresh))
	for _, f := range fresh {
		drafted[f.path] = true
		disk, err := readDisk(outputDir, f.path)
		if err != nil {
			return syncPlan{}, err
		}
		if err := plan.judgeFile(f, entries[f.path], disk); err != nil {
			return syncPlan{}, err
		}
	}
	if rec != nil {
		for _, e := range rec.Files {
			if drafted[e.Path] {
				continue
			}
			disk, err := readDisk(outputDir, e.Path)
			if err != nil {
				return syncPlan{}, err
			}
			switch {
			case !disk.exists:
			case disk.regular && fileUnedited(e, disk.content):
				plan.removes = append(plan.removes, e.Path)
				plan.drift = append(plan.drift, Drift{Change: DriftRemoved, File: e.Path})
			default:
				plan.conflicts = append(plan.conflicts, SyncConflict{File: e.Path, Reason: conflictEditedDropped})
			}
		}
	}
	return plan, nil
}

// judgeFile judges one drafted file against its record entry, nil where the
// record lists none, and what is on disk.
func (p *syncPlan) judgeFile(f draftedFile, e *recordFile, disk diskFile) error {
	conflict := func(reason string) { p.conflicts = append(p.conflicts, SyncConflict{File: f.path, Reason: reason}) }
	write := func(content string, change string) {
		p.writes = append(p.writes, abi.ScaffoldFile{Path: f.path, Content: content})
		p.drift = append(p.drift, Drift{Change: change, File: f.path})
	}
	fresh := recordEntryFor(f)
	switch {
	case e == nil:
		if disk.exists {
			conflict(conflictAuthorGained)
			return nil
		}
		write(f.content, DriftAdded)
	case sameEntry(*e, fresh):
		// The source drafts what the import last wrote: whatever is on disk stays.
	case !disk.exists:
		conflict(conflictRemovedChanged)
	case !disk.regular:
		conflict(conflictNotRegularFile)
	case disk.content == f.content:
		// It already reads as the source drafts it.
	case e.Digest != "" && f.doc == nil, e.Digest == "" && f.doc == nil, e.Digest != "" && f.doc != nil:
		// Owned whole, now or before: the file is written anew only where it is
		// exactly what the import last wrote.
		if !fileUnedited(*e, disk.content) {
			conflict(conflictEditedChanged)
			return nil
		}
		write(f.content, DriftChanged)
	default:
		content, drift, conflicts, err := syncParts(f, *e, disk.content)
		if err != nil {
			return err
		}
		p.conflicts = append(p.conflicts, conflicts...)
		if len(conflicts) == 0 && content != disk.content {
			p.writes = append(p.writes, abi.ScaffoldFile{Path: f.path, Content: content})
		}
		p.drift = append(p.drift, drift...)
	}
	return nil
}

// sameEntry reports whether two record entries describe the same text.
func sameEntry(a, b recordFile) bool {
	if a.Digest != b.Digest || len(a.Parts) != len(b.Parts) {
		return false
	}
	for i := range a.Parts {
		if a.Parts[i].Digest != b.Parts[i].Digest || joinKey(a.Parts[i].Key) != joinKey(b.Parts[i].Key) {
			return false
		}
	}
	return true
}

// fileUnedited reports whether content is exactly what the import last wrote,
// as the record entry e describes it: the same bytes for a file owned whole; for
// a file owned entry by entry, the same entries with the same text, and no line
// no entry owns.
func fileUnedited(e recordFile, content string) bool {
	if e.Digest != "" {
		return digestOf(content) == e.Digest
	}
	doc, err := indexYAML(content)
	if err != nil {
		return false
	}
	if len(doc.units) != len(e.Parts) {
		return false
	}
	for _, part := range e.Parts {
		u, ok := doc.byKey[joinKey(part.Key)]
		if !ok || digestOf(u.content) != part.Digest {
			return false
		}
	}
	owned := doc.ownedLines()
	for i, line := range doc.lines {
		if !owned[i] && strings.TrimSpace(line) != "" {
			return false
		}
	}
	return true
}

// lineEdit replaces the lines [from, to) of a document with text; from equal to
// to inserts.
type lineEdit struct {
	from, to int
	text     []string
	seq      int
}

// partsSync is the state of the sync of one drafted file owned entry by entry.
type partsSync struct {
	file      string
	fresh     *yamlDoc
	disk      *yamlDoc
	recorded  map[string]string // the record's digest for each part
	edits     []lineEdit
	covered   map[string]bool // parts whose whole subtree an edit already decides
	expect    map[string]string
	drift     []Drift
	conflicts []SyncConflict
}

// syncParts brings the drafted Aggregate or Release on disk up to date with f,
// entry by entry, as e records the import last wrote it, and returns the content
// it would write, the parts that drifted and the parts that conflict.
func syncParts(f draftedFile, e recordFile, disk string) (string, []Drift, []SyncConflict, error) {
	d, err := indexYAML(disk)
	if err != nil {
		return disk, nil, []SyncConflict{{File: f.path, Reason: conflictUnreadable}}, nil
	}
	s := &partsSync{
		file:     f.path,
		fresh:    f.doc,
		disk:     d,
		recorded: make(map[string]string, len(e.Parts)),
		covered:  map[string]bool{},
		expect:   make(map[string]string, len(d.units)),
	}
	recordAt := make(map[string]int, len(e.Parts))
	for i, part := range e.Parts {
		s.recorded[joinKey(part.Key)] = part.Digest
		recordAt[joinKey(part.Key)] = i
	}
	for _, u := range d.units {
		s.expect[joinKey(u.key)] = u.content
	}

	// The parts are judged in the order the fresh draft names them, each part the
	// source dropped judged where the record placed it, so the drift reads in the
	// document's own order and a part is always judged before the parts beneath it.
	judged := map[string]bool{}
	next := 0
	dropped := func(upTo int) {
		for ; next < upTo; next++ {
			k := joinKey(e.Parts[next].Key)
			if _, still := f.doc.byKey[k]; !still && !judged[k] {
				judged[k] = true
				s.judge(e.Parts[next].Key)
			}
		}
	}
	for _, u := range f.doc.units {
		k := joinKey(u.key)
		if at, ok := recordAt[k]; ok {
			dropped(at)
		}
		judged[k] = true
		s.judge(u.key)
	}
	dropped(len(e.Parts))

	if len(s.conflicts) > 0 {
		return disk, nil, s.conflicts, nil
	}
	if len(s.edits) == 0 {
		return disk, s.drift, nil, nil
	}
	out, err := applyEdits(d, s.edits)
	if err != nil {
		return "", nil, nil, err
	}
	if err := s.verify(out); err != nil {
		return "", nil, nil, fmt.Errorf("the sync of %q could not place its changes, so no files were written: %w",
			f.path, err)
	}
	return out, s.drift, nil, nil
}

// underCovered reports whether an edit already decides key, through key itself
// or a part above it.
func (s *partsSync) underCovered(key []string) bool {
	for i := 1; i <= len(key); i++ {
		if s.covered[joinKey(key[:i])] {
			return true
		}
	}
	return false
}

// conflict records a part that stops the sync, and stops judging beneath it.
func (s *partsSync) conflict(key []string, reason string) {
	s.conflicts = append(s.conflicts, SyncConflict{File: s.file, Key: key, Reason: reason})
	s.covered[joinKey(key)] = true
}

// judge judges one part, three ways.
func (s *partsSync) judge(key []string) {
	if s.underCovered(key) {
		return
	}
	k := joinKey(key)
	recorded, inRecord := s.recorded[k]
	fu, inFresh := s.fresh.byKey[k]
	du, onDisk := s.disk.byKey[k]
	switch {
	case inRecord && inFresh:
		freshDigest := digestOf(fu.content)
		switch {
		case freshDigest == recorded:
			// The source left it as the import last wrote it: it stays as it is on
			// disk, edited or not.
		case !onDisk:
			s.conflict(key, conflictRemovedChanged)
		case du.content == fu.content:
			// It already reads as the source drafts it.
		case digestOf(du.content) != recorded:
			s.conflict(key, conflictEditedChanged)
		case du.interior != fu.interior:
			if s.subtreeRemovable(du) {
				s.replaceSubtree(du, fu)
				s.drift = append(s.drift, Drift{Change: DriftChanged, File: s.file, Key: key})
			}
		default:
			s.replaceOwned(du, fu)
			s.drift = append(s.drift, Drift{Change: DriftChanged, File: s.file, Key: key})
		}
	case inRecord:
		switch {
		case !onDisk:
			// Already gone.
		case digestOf(du.content) != recorded:
			s.conflict(key, conflictEditedDropped)
		case s.subtreeRemovable(du):
			s.edits = append(s.edits, lineEdit{from: du.start, to: du.end + 1, seq: len(s.edits)})
			s.forgetSubtree(du.key)
			s.covered[k] = true
			s.drift = append(s.drift, Drift{Change: DriftRemoved, File: s.file, Key: key})
		}
	case inFresh:
		if onDisk {
			s.conflict(key, conflictAuthorGained)
			return
		}
		s.insert(fu)
	}
}

// subtreeRemovable reports whether the part du and everything beneath it on
// disk is what the import last wrote, with nothing an author added, recording a
// conflict for each part that is not and covering du where any is.
func (s *partsSync) subtreeRemovable(du *yamlUnit) bool {
	ok := true
	prefix := joinKey(du.key) + "\x00"
	lines := map[int]bool{}
	for i := du.start; i <= du.ownedLast(); i++ {
		lines[i] = true
	}
	// A part an author added is named once, not again for every entry beneath it.
	var authored []string
	for _, u := range s.disk.units {
		k := joinKey(u.key)
		if !strings.HasPrefix(k, prefix) || slices.ContainsFunc(authored, func(a string) bool {
			return strings.HasPrefix(k, a+"\x00")
		}) {
			continue
		}
		recorded, inRecord := s.recorded[k]
		switch {
		case !inRecord:
			s.conflicts = append(s.conflicts, SyncConflict{File: s.file, Key: u.key, Reason: conflictAuthorInside})
			authored = append(authored, k)
			ok = false
		case digestOf(u.content) != recorded:
			s.conflicts = append(s.conflicts, SyncConflict{File: s.file, Key: u.key, Reason: conflictEditedDropped})
			ok = false
		}
		for i := u.start; i <= u.ownedLast(); i++ {
			lines[i] = true
		}
	}
	if ok {
		for i := du.start; i <= du.end; i++ {
			if !lines[i] && strings.TrimSpace(s.disk.lines[i]) != "" {
				s.conflicts = append(s.conflicts, SyncConflict{File: s.file, Key: du.key, Reason: conflictAuthorLines})
				ok = false
				break
			}
		}
	}
	if !ok {
		s.covered[joinKey(du.key)] = true
	}
	return ok
}

// replaceOwned writes the text fu owns over the text du owns: the whole entry
// for a leaf, the head comment and key line for an interior entry, whose entries
// are judged on their own.
func (s *partsSync) replaceOwned(du, fu *yamlUnit) {
	s.edits = append(s.edits, lineEdit{
		from: du.start,
		to:   du.ownedLast() + 1,
		text: s.freshLines(fu.start, fu.ownedLast(), fu.col, du.col),
		seq:  len(s.edits),
	})
	s.expect[joinKey(du.key)] = fu.content
}

// replaceSubtree writes fu, with everything beneath it, over du with
// everything beneath it: the edit for a part whose value turned from a mapping
// into something else, or the other way round.
func (s *partsSync) replaceSubtree(du, fu *yamlUnit) {
	s.edits = append(s.edits, lineEdit{
		from: du.start,
		to:   du.end + 1,
		text: s.freshLines(fu.start, fu.end, fu.col, du.col),
		seq:  len(s.edits),
	})
	s.forgetSubtree(du.key)
	s.expectFreshSubtree(fu.key)
	s.covered[joinKey(du.key)] = true
}

// insert places a part the source gained, with everything beneath it, among the
// entries of the mapping holding it on disk: after the nearest entry the fresh
// draft names before it that is on disk, or else before the nearest one it names
// after it, or else after the mapping's last entry.
func (s *partsSync) insert(fu *yamlUnit) {
	parent := fu.key[:len(fu.key)-1]
	if len(parent) > 0 {
		pu, ok := s.disk.byKey[joinKey(parent)]
		if !ok || !pu.interior {
			s.conflict(fu.key, conflictUnplaceable)
			return
		}
	}
	diskSiblings := s.disk.children[joinKey(parent)]
	if len(diskSiblings) == 0 {
		s.conflict(fu.key, conflictUnplaceable)
		return
	}
	freshSiblings := s.fresh.children[joinKey(parent)]
	at := -1
	for i, sib := range freshSiblings {
		if sib == fu {
			at = i
			break
		}
	}
	anchor := -1
	for i := at - 1; i >= 0 && anchor < 0; i-- {
		if du, ok := s.disk.byKey[joinKey(freshSiblings[i].key)]; ok {
			anchor = du.end + 1
		}
	}
	for i := at + 1; i < len(freshSiblings) && anchor < 0; i++ {
		if du, ok := s.disk.byKey[joinKey(freshSiblings[i].key)]; ok {
			anchor = du.start
		}
	}
	if anchor < 0 {
		anchor = diskSiblings[len(diskSiblings)-1].end + 1
	}
	s.edits = append(s.edits, lineEdit{
		from: anchor,
		to:   anchor,
		text: s.freshLines(fu.start, fu.end, fu.col, diskSiblings[0].col),
		seq:  len(s.edits),
	})
	s.expectFreshSubtree(fu.key)
	s.covered[joinKey(fu.key)] = true
	s.drift = append(s.drift, Drift{Change: DriftAdded, File: s.file, Key: fu.key})
}

// freshLines is the fresh draft's lines [from, to], moved from column fromCol to
// column toCol, each ended as the disk document's lines are.
func (s *partsSync) freshLines(from, to, fromCol, toCol int) []string {
	ending := "\n"
	if s.disk.crlf {
		ending = "\r\n"
	}
	out := make([]string, 0, to-from+1)
	for i := from; i <= to; i++ {
		text := lineText(s.fresh.lines[i])
		switch {
		case strings.TrimSpace(text) == "":
			text = ""
		case toCol > fromCol:
			text = strings.Repeat(" ", toCol-fromCol) + text
		case toCol < fromCol:
			text = dedent(text, fromCol-toCol)
		}
		out = append(out, text+ending)
	}
	return out
}

// forgetSubtree drops key and everything beneath it from what the result is
// expected to hold.
func (s *partsSync) forgetSubtree(key []string) {
	k := joinKey(key)
	for _, u := range s.disk.units {
		if uk := joinKey(u.key); uk == k || strings.HasPrefix(uk, k+"\x00") {
			delete(s.expect, uk)
		}
	}
}

// expectFreshSubtree records that the result holds the fresh draft's key and
// everything beneath it.
func (s *partsSync) expectFreshSubtree(key []string) {
	k := joinKey(key)
	for _, u := range s.fresh.units {
		if uk := joinKey(u.key); uk == k || strings.HasPrefix(uk, k+"\x00") {
			s.expect[uk] = u.content
		}
	}
}

// verify holds the synced text to what the sync meant to write: every entry it
// holds is one expected, with exactly the text expected — an entry an author
// wrote with exactly the text it had on disk.
func (s *partsSync) verify(out string) error {
	doc, err := indexYAML(out)
	if err != nil {
		return err
	}
	if len(doc.units) != len(s.expect) {
		return fmt.Errorf("it holds %d entries where %d were expected", len(doc.units), len(s.expect))
	}
	for _, u := range doc.units {
		want, ok := s.expect[joinKey(u.key)]
		if !ok {
			return fmt.Errorf("it holds %s, which was not expected", RenderKey(u.key))
		}
		if u.content != want {
			return fmt.Errorf("%s does not read as expected", RenderKey(u.key))
		}
	}
	return nil
}

// applyEdits applies non-overlapping line edits to a document's lines. Edits at
// one place apply inserts first, in the order they were planned.
func applyEdits(d *yamlDoc, edits []lineEdit) (string, error) {
	sorted := append([]lineEdit(nil), edits...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.from != b.from {
			return a.from < b.from
		}
		if (a.from == a.to) != (b.from == b.to) {
			return a.from == a.to
		}
		return a.seq < b.seq
	})
	var b strings.Builder
	at := 0
	for _, e := range sorted {
		if e.from < at {
			return "", fmt.Errorf("two of its changes meet at line %d", e.from+1)
		}
		for ; at < e.from; at++ {
			b.WriteString(d.lines[at])
		}
		if len(e.text) > 0 && at > 0 && at == len(d.lines) && !strings.HasSuffix(d.lines[at-1], "\n") {
			if d.crlf {
				b.WriteString("\r\n")
			} else {
				b.WriteString("\n")
			}
		}
		for _, line := range e.text {
			b.WriteString(line)
		}
		at = e.to
	}
	for ; at < len(d.lines); at++ {
		b.WriteString(d.lines[at])
	}
	return b.String(), nil
}
