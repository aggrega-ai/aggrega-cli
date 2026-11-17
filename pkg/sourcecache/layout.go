// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package sourcecache

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegment"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// extractedDirName is the directory inside a cache slot that holds the files the
// slot's artifact carries.
const extractedDirName = "extracted"

// layoutFileMode is the permission every laid-out file is written with.
const layoutFileMode = 0o644

// ExtractedDir returns the directory inside the cache slot at slot under which the
// files the slot's artifact carries are laid out. It is derived beside the artifact
// rather than recorded anywhere, and what it holds is rebuilt from the artifact on
// every read, so nothing in it is authoritative.
func ExtractedDir(slot string) string {
	return filepath.Join(slot, extractedDirName)
}

// ExtractedTree returns the directory inside [ExtractedDir] where the files of the
// artifact whose content digest is contentDigest are laid out. Each artifact content
// has a tree of its own, named by the digest through the one spelling of a derived
// name (pathsegment.Encode) — `sha256:<hex>` is the directory `sha256+zx00003a<hex>`
// — so a slot whose artifact is replaced while a build is still reading the
// previous one never has that build's tree rewritten to the new content under it.
func ExtractedTree(slot, contentDigest string) string {
	return filepath.Join(ExtractedDir(slot), pathsegment.Encode(contentDigest))
}

// RegenerateExtracted lays out the files archive carries — a gzipped tar the caller
// has already verified — in the slot's tree for that archive ([ExtractedTree]), and
// returns the tree's path. An archive entry that would land outside the tree is
// refused with a wrapped ErrFetchIntegrity before anything is written, and where the
// process runs on Windows so is an entry holding a segment Windows cannot hold as
// written, with pathscope.ErrNameWindowsCannotHold naming it.
//
// It is the one statement of how a cache slot's laid-out files are rebuilt, for a
// source artifact's slot and a catalog entity's slot alike, and it rebuilds on every
// call rather than when something looks out of date: every file the archive carries
// is compared, byte for byte, with what the tree holds in its place and replaced
// when it is missing, differs or is not a regular file, and anything in the tree the
// archive does not carry is removed. A file changed or added in the tree by hand is
// therefore discarded rather than trusted, while the archive the tree is rebuilt from
// is the thing the caller's digest check covers.
//
// A file that already holds the archive's bytes is left exactly as it is, and that is
// what makes the tree safe to read while another read of the same slot rebuilds it,
// in the same process or in another one sharing the workspace: a reader never finds
// a file it was handed missing or partly written, including a reader that opens the
// files well after the call that laid them out returned. A file that does need
// replacing is written beside the tree and renamed over its place, so a reader sees
// the file it held or the new one. Emptying the tree and extracting it again would
// open exactly the window this closes.
//
// Every regular file and directory entry is laid out; an entry of any other type (a
// link, a device) is not, and whatever the tree holds in its place is removed.
// A directory is created 0755 before the process umask and a file is written 0644;
// the archive's own modes, owners and times are not carried.
//
// workspaceRoot and cacheDir are the workspace the cache belongs to and its
// artifact cache directory, the two the slot was composed from. The tree is held,
// before anything is laid out, to the root that cache directory picks
// (writescope.RootForCache): the cache directory itself. A directory on the way to
// the slot that is a link leading out of the cache directory — or a cache directory
// leading out of the workspace — is refused with the halt the write-scope rule
// declares rather than written through.
func RegenerateExtracted(workspaceRoot, cacheDir, slot string, archive []byte) (string, error) {
	return regenerate(workspaceRoot, cacheDir, slot, archive, tarGzEntries(archive))
}

// RegenerateExtractedDocument is [RegenerateExtracted] for an artifact that is a
// single document rather than an archive: the slot's tree for it holds that one file,
// under name, rebuilt the same way on every call. name must be a plain relative file
// name; one that would land outside the tree is refused with a wrapped
// ErrFetchIntegrity, and on Windows one Windows cannot hold as written is refused as
// an archive entry is.
func RegenerateExtractedDocument(workspaceRoot, cacheDir, slot, name string, document []byte) (string, error) {
	return regenerate(workspaceRoot, cacheDir, slot, document, documentEntry(name, document))
}

// RegenerateExtractedZip is [RegenerateExtracted] for an artifact that is a zip
// package: the slot's tree for it holds the package's directories and regular files,
// laid out by the one placement rule an archive's entries are. An archive that does
// not open, or an entry that cannot be read, is refused with a wrapped
// ErrFetchIntegrity, and an entry of any type but a directory or a regular file (a
// link) is not laid out.
func RegenerateExtractedZip(workspaceRoot, cacheDir, slot string, archive []byte) (string, error) {
	return regenerate(workspaceRoot, cacheDir, slot, archive, zipEntries(archive))
}

// CheckZipLayout reports whether archive is a zip package that lays out: it opens,
// every entry places beneath a tree ([entryPlace]), and a non-empty moduleSubdir is a
// directory of the tree the package lays out, named by an entry or implied by the
// path of one. A package that does not open or has an entry that may not be laid out
// is refused with a wrapped ErrFetchIntegrity, and a moduleSubdir the package lacks
// with a wrapped ErrFetchNotFound naming the value as written. It writes nothing.
func CheckZipLayout(archive []byte, moduleSubdir string) error {
	plan, err := planTree("tree", zipEntries(archive))
	if err != nil {
		return err
	}
	rel := moduleSubdirPlace(moduleSubdir)
	if rel != "" && !plan.dirs[rel] {
		return absentSubdir(moduleSubdir)
	}
	return nil
}

// moduleSubdirPlace returns the slash-separated place in a laid-out tree that
// moduleSubdir names, read as the checkout join reads it: a backslash is a
// separator, and separators leading or trailing the value are dropped. Empty means
// the tree itself.
func moduleSubdirPlace(moduleSubdir string) string {
	rel := path.Clean(strings.ReplaceAll(moduleSubdir, `\`, "/"))
	rel = strings.Trim(rel, "/")
	if rel == "." {
		return ""
	}
	return rel
}

// absentSubdir is the refusal for a module_subdir that is not a directory of the
// package.
func absentSubdir(moduleSubdir string) error {
	return fmt.Errorf("module_subdir %q is not a directory of the package: %w", moduleSubdir, ErrFetchNotFound())
}

// regenerate lays out what entries yields in the tree ExtractedTree names for
// artifact, once the tree is held to the root the cache directory picks.
func regenerate(workspaceRoot, cacheDir, slot string, artifact []byte, entries entrySource) (string, error) {
	tree := ExtractedTree(slot, digest.ContentDigest(artifact))
	if err := writescope.CheckWrite(writescope.RootForCache(workspaceRoot, cacheDir), tree); err != nil {
		return "", err
	}
	if err := layOut(slot, tree, entries); err != nil {
		return "", fmt.Errorf("extract into %s: %w", tree, err)
	}
	return tree, nil
}

// entrySource walks the entries an artifact carries, in order, handing visit each
// entry's header and a reader over its body. It can be walked more than once.
type entrySource func(visit func(hdr *tar.Header, body io.Reader) error) error

// tarGzEntries walks the entries of the gzipped tar archive.
func tarGzEntries(archive []byte) entrySource {
	return func(visit func(*tar.Header, io.Reader) error) error {
		gz, err := gzip.NewReader(bytes.NewReader(archive))
		if err != nil {
			return fmt.Errorf("gunzip: %w", err)
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return fmt.Errorf("tar next: %w", err)
			}
			if err := visit(hdr, tr); err != nil {
				return err
			}
		}
	}
}

// zipEntries walks the entries of the zip archive in central-directory order, each
// as the tar header the planner reads: a directory, a regular file, or — for a link
// or any other kind of entry — a header of that other type, which the layout does
// not carry.
func zipEntries(archive []byte) entrySource {
	return func(visit func(*tar.Header, io.Reader) error) error {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		// An entry named with a climbing or absolute path opens with
		// zip.ErrInsecurePath beside a usable reader; entryPlace judges every name,
		// so the walk goes on.
		if err != nil && !(errors.Is(err, zip.ErrInsecurePath) && zr != nil) {
			return fmt.Errorf("open zip: %w: %w", err, ErrFetchIntegrity())
		}
		for _, f := range zr.File {
			hdr := &tar.Header{Name: f.Name, Size: int64(f.UncompressedSize64)}
			switch mode := f.Mode(); {
			case f.FileInfo().IsDir():
				hdr.Typeflag = tar.TypeDir
			case mode.IsRegular():
				hdr.Typeflag = tar.TypeReg
			case mode&fs.ModeSymlink != 0:
				hdr.Typeflag = tar.TypeSymlink
			default:
				hdr.Typeflag = tar.TypeChar
			}
			if hdr.Typeflag != tar.TypeReg {
				if err := visit(hdr, bytes.NewReader(nil)); err != nil {
					return err
				}
				continue
			}
			if err := visitZipFile(f, hdr, visit); err != nil {
				return err
			}
		}
		return nil
	}
}

// visitZipFile hands visit the body of the zip entry f, closed again when visit
// returns.
func visitZipFile(f *zip.File, hdr *tar.Header, visit func(*tar.Header, io.Reader) error) error {
	body, err := f.Open()
	if err != nil {
		return fmt.Errorf("open zip entry %q: %w: %w", f.Name, err, ErrFetchIntegrity())
	}
	defer body.Close()
	return visit(hdr, body)
}

// documentEntry walks one regular-file entry, name, holding document.
func documentEntry(name string, document []byte) entrySource {
	return func(visit func(*tar.Header, io.Reader) error) error {
		hdr := &tar.Header{Name: name, Typeflag: tar.TypeReg, Size: int64(len(document))}
		return visit(hdr, bytes.NewReader(document))
	}
}

// treePlan is what a tree must hold: every directory an entry names or implies, and
// every file, keyed by its slash-separated place in the tree, with the ordinal of the
// entry that decides it. Where an archive names one file twice the later entry
// decides it, as it would in an extraction that wrote them in order.
type treePlan struct {
	dirs  map[string]bool
	files map[string]int
}

// carries reports whether rel is a place the artifact lays something out at.
func (p treePlan) carries(rel string) bool {
	if p.dirs[rel] {
		return true
	}
	_, ok := p.files[rel]
	return ok
}

// addDir records rel and every directory above it.
func (p treePlan) addDir(rel string) {
	for rel != "." && rel != "/" && rel != "" && !p.dirs[rel] {
		p.dirs[rel] = true
		rel = path.Dir(rel)
	}
}

// sortedDirs returns the directories in lexical order, which puts every directory
// before the ones below it.
func (p treePlan) sortedDirs() []string {
	out := make([]string, 0, len(p.dirs))
	for d := range p.dirs {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// entryPlace returns the slash-separated place an entry lands at in tree, false for
// an entry naming the tree itself, and a refusal for an entry that may not be laid
// out: a wrapped ErrFetchIntegrity for one that would land outside the tree, and,
// where the process runs on Windows, the halt pathscope.ErrNameWindowsCannotHold
// names for one holding a segment Windows cannot hold as written.
//
// An entry name is slash-separated by the archive format's own definition, and it
// is judged by pathscope.PlaceBeneath, the one way a name somebody else chose is
// placed on disk. Whether it climbs out of the tree is the same answer on every
// platform; an entry Windows cannot hold — `NUL`, `aux/x.yaml`, `report.` — is
// refused on Windows and laid out as it is named everywhere else. An entry name
// that merely begins with two dots is an ordinary name. Every host path the layout
// touches is placed by the same function ([placeIn]), so no second join of an entry
// onto the tree exists to disagree with the judgement.
func entryPlace(tree string, hdr *tar.Header) (string, bool, error) {
	rel := path.Clean(hdr.Name)
	if rel == "." || rel == "/" {
		return "", false, nil
	}
	_, inside, err := pathscope.PlaceBeneath(tree, hdr.Name)
	if err != nil {
		return "", false, err
	}
	if !inside {
		return "", false, fmt.Errorf("path traversal attempt %q in archive: %w", hdr.Name, ErrFetchIntegrity())
	}
	return rel, true, nil
}

// placeIn returns the host path the slash-separated place rel occupies in tree.
//
// Every place a plan holds is an entry's own place or a directory above one, so
// every one of them was judged by [entryPlace] before the layout touched anything;
// the refusals here are that judgement's, met again only if a plan was built from
// another tree.
func placeIn(tree, rel string) (string, error) {
	host, inside, err := pathscope.PlaceBeneath(tree, rel)
	if err != nil {
		return "", err
	}
	if !inside {
		return "", fmt.Errorf("path traversal attempt %q in archive: %w", rel, ErrFetchIntegrity())
	}
	return host, nil
}

// planTree reads what entries lays out in tree, refusing an entry that may not be
// laid out there ([entryPlace]) and an archive naming one place as both a file and a
// directory, the latter with a wrapped ErrFetchIntegrity.
func planTree(tree string, entries entrySource) (treePlan, error) {
	plan := treePlan{dirs: map[string]bool{}, files: map[string]int{}}
	ordinal := -1
	err := entries(func(hdr *tar.Header, _ io.Reader) error {
		ordinal++
		rel, ok, err := entryPlace(tree, hdr)
		if err != nil || !ok {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			plan.addDir(rel)
		case tar.TypeReg:
			plan.addDir(path.Dir(rel))
			plan.files[rel] = ordinal
		}
		return nil
	})
	if err != nil {
		return treePlan{}, err
	}
	names := make([]string, 0, len(plan.files))
	for rel := range plan.files {
		names = append(names, rel)
	}
	sort.Strings(names)
	for _, rel := range names {
		if plan.dirs[rel] {
			return treePlan{}, fmt.Errorf("archive entry %q is both a file and a directory: %w", rel, ErrFetchIntegrity())
		}
	}
	return plan, nil
}

// layOut makes tree hold exactly what entries carries; see [RegenerateExtracted].
// Temporary files are written in the slot's extracted directory, beside the tree
// rather than in it, so no reader of the tree ever lists one and no concurrent
// layout of the same tree ever removes another's.
func layOut(slot, tree string, entries entrySource) error {
	plan, err := planTree(tree, entries)
	if err != nil {
		return err
	}
	dirs, err := hostPaths(tree, plan.sortedDirs())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(slot, 0o755); err != nil {
		return fmt.Errorf("mkdir %q: %w", slot, err)
	}
	staging := ExtractedDir(slot)
	for _, dir := range append([]string{staging, tree}, dirs...) {
		if err := ensureDir(dir); err != nil {
			return err
		}
	}

	// Compare every carried file first, and write only the ones that differ.
	stale := map[int]bool{}
	if err := eachDecidingFile(tree, entries, plan, func(ordinal int, place string, hdr *tar.Header, body io.Reader) error {
		if !holdsBytes(place, hdr.Size, body) {
			stale[ordinal] = true
		}
		return nil
	}); err != nil {
		return err
	}
	if len(stale) > 0 {
		if err := eachDecidingFile(tree, entries, plan, func(ordinal int, place string, _ *tar.Header, body io.Reader) error {
			if !stale[ordinal] {
				return nil
			}
			return replaceFile(staging, place, body)
		}); err != nil {
			return err
		}
	}
	return removeUncarried(tree, plan)
}

// hostPaths places each slash-separated place under tree as a host path
// ([placeIn]).
func hostPaths(tree string, rels []string) ([]string, error) {
	out := make([]string, 0, len(rels))
	for _, rel := range rels {
		host, err := placeIn(tree, rel)
		if err != nil {
			return nil, err
		}
		out = append(out, host)
	}
	return out, nil
}

// eachDecidingFile calls fn for every regular-file entry that decides its place in
// the plan, with the entry's ordinal, the host path of its place in tree and its
// body.
func eachDecidingFile(tree string, entries entrySource, plan treePlan, fn func(ordinal int, place string, hdr *tar.Header, body io.Reader) error) error {
	ordinal := -1
	return entries(func(hdr *tar.Header, body io.Reader) error {
		ordinal++
		if hdr.Typeflag != tar.TypeReg {
			return nil
		}
		rel, ok, err := entryPlace(tree, hdr)
		if err != nil || !ok {
			return err
		}
		if plan.files[rel] != ordinal {
			return nil
		}
		place, err := placeIn(tree, rel)
		if err != nil {
			return err
		}
		return fn(ordinal, place, hdr, body)
	})
}

// ensureDir makes p a real directory. Something else in its place — a file, or a
// link, which is removed rather than followed — is replaced. A directory another
// layout of the same tree created first is accepted.
func ensureDir(p string) error {
	fi, err := os.Lstat(p)
	switch {
	case err == nil && fi.IsDir():
		return nil
	case err == nil:
		if err := os.RemoveAll(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove %q: %w", p, err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("stat %q: %w", p, err)
	}
	if err := os.Mkdir(p, 0o755); err != nil {
		if fi, lerr := os.Lstat(p); lerr == nil && fi.IsDir() {
			return nil
		}
		return fmt.Errorf("mkdir %q: %w", p, err)
	}
	return nil
}

// holdsBytes reports whether p is a regular file whose content is exactly the size
// bytes body yields. Anything else in its place — nothing, a directory, a link, a
// file that cannot be read, other bytes — reports false, and the caller replaces it.
func holdsBytes(p string, size int64, body io.Reader) bool {
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() != size {
		return false
	}
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	return sameStream(f, body)
}

// sameFiles reports whether a is a regular file holding exactly the bytes of b.
func sameFiles(a, b string) bool {
	fb, err := os.Open(b)
	if err != nil {
		return false
	}
	defer fb.Close()
	fi, err := fb.Stat()
	if err != nil {
		return false
	}
	return holdsBytes(a, fi.Size(), fb)
}

// sameStream reports whether a and b yield the same bytes to their ends.
func sameStream(a, b io.Reader) bool {
	bufA := make([]byte, 32*1024)
	bufB := make([]byte, 32*1024)
	for {
		na, errA := io.ReadFull(a, bufA)
		nb, errB := io.ReadFull(b, bufB)
		if na != nb || !bytes.Equal(bufA[:na], bufB[:nb]) {
			return false
		}
		endA := errA == io.EOF || errA == io.ErrUnexpectedEOF
		endB := errB == io.EOF || errB == io.ErrUnexpectedEOF
		if endA || endB {
			return endA && endB
		}
		if errA != nil || errB != nil {
			return false
		}
	}
}

// replaceFile writes body to a temporary file in staging and renames it over target,
// so a reader of target sees the file it held or the new one and never a missing or
// partly written one.
func replaceFile(staging, target string, body io.Reader) error {
	tmp, err := os.CreateTemp(staging, ".layout-*")
	if err != nil {
		return fmt.Errorf("create a file for %q: %w", target, err)
	}
	tmpName := tmp.Name()
	discard := func() { _ = os.Remove(tmpName) }
	if _, err := io.Copy(tmp, body); err != nil {
		_ = tmp.Close()
		discard()
		return fmt.Errorf("copy %q: %w", target, err)
	}
	if err := tmp.Close(); err != nil {
		discard()
		return fmt.Errorf("close %q: %w", target, err)
	}
	if err := os.Chmod(tmpName, layoutFileMode); err != nil {
		discard()
		return fmt.Errorf("chmod %q: %w", target, err)
	}
	// A rename cannot replace a directory, so whatever is not a regular file in the
	// file's place goes first; a link goes with it rather than being relied on to be
	// replaced.
	if fi, err := os.Lstat(target); err == nil && !fi.Mode().IsRegular() {
		if err := os.RemoveAll(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
			discard()
			return fmt.Errorf("remove %q: %w", target, err)
		}
	}
	if err := os.Rename(tmpName, target); err != nil {
		// Another read laying out the same artifact may already have put the file in
		// place, and a platform may refuse to replace a file another reader holds
		// open. Either way the place stands only if it already holds these bytes.
		same := sameFiles(target, tmpName)
		discard()
		if same {
			return nil
		}
		return fmt.Errorf("rename into %q: %w", target, err)
	}
	return nil
}

// removeUncarried removes everything below tree that the plan does not lay out. A
// place the plan does lay out is kept whatever it holds now: the passes before this
// one made it right, and a place found otherwise at this point was changed by
// something racing the read, which the next read answers.
func removeUncarried(tree string, plan treePlan) error {
	return filepath.WalkDir(tree, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if p == tree {
			return nil
		}
		rel, err := filepath.Rel(tree, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if plan.carries(rel) {
			if d.IsDir() && !plan.dirs[rel] {
				return filepath.SkipDir
			}
			return nil
		}
		if err := os.RemoveAll(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove %q: %w", p, err)
		}
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
}
