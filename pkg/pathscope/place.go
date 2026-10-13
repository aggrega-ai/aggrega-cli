// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pathscope

import (
	"fmt"
	"path"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/internal/windowshost"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegment"
)

// PlaceBeneath reports the host path a slash-separated logical name occupies
// beneath the directory root, for a writer about to put a file or a directory
// there, and whether the name stays beneath root at all.
//
// It is the one way a name a writer did not choose — an archive entry laid out in
// the cache, a draft an importer returned, a rendered artifact's name, the path of
// a document an edit writes back — becomes a path on disk, and it composes every
// judgement such a name owes, once:
//
//   - The climb, as [IsLocalSlash] judges it. A name that is empty, absolute, or
//     climbs above root — through slashes, or through backslashes as a Windows join
//     reads them — reports false and no error, and the writer refuses it in its own
//     words, since what a name leaving its directory means differs from writer to
//     writer. That verdict is the same on every platform, and it is taken first, so
//     a name that climbs is reported as climbing whatever else it holds.
//   - root is made a directory path by [DirectoryPath] before the name is joined
//     onto it, so a root named relative to the working directory, or as a bare
//     Windows drive such as "C:", still has every name placed beneath it. An error
//     here means root could not be made one.
//   - On Windows only, a name holding a segment Windows cannot hold as written
//     ([pathsegment.WindowsCannotHold]) — a name it reserves for a device, or a
//     segment ending in a full stop or a space — is refused with
//     [ErrNameWindowsCannotHold], naming the name, the segment and the directory.
//     On every other platform such a name is placed like any other.
//
// When the error is not nil the other two results mean nothing. Otherwise a true
// comes with the path to write: root made a directory path, joined with the name
// cleaned as a slash-separated name and spelled with the host's separator. Nothing
// is written here and no file is read, so a writer asks this for every name of a
// set before it writes any, and a set holding one refused name leaves nothing on
// disk.
//
// A writer judging a name with [IsLocalSlash] and joining it onto a root itself
// would skip the Windows refusal and the directory path, which is why this is the
// one function a writer reaches a path through; a test of this package fails
// where code outside it judges a name with IsLocalSlash and is not one of the
// judgements it names as writing nothing.
func PlaceBeneath(root, name string) (string, bool, error) {
	if !IsLocalSlash(name) {
		return "", false, nil
	}
	dir, err := DirectoryPath(root)
	if err != nil {
		return "", false, err
	}
	if windowshost.Running() {
		if segment, held := pathsegment.WindowsCannotHold(name); held {
			return "", false, refuseWindowsName(name, segment, dir)
		}
	}
	return filepath.Join(dir, filepath.FromSlash(path.Clean(name))), true, nil
}

// DirectoryPath returns dir as a directory path: absolute, made so against the
// process's working directory, and cleaned.
//
// It is the one place a directory somebody named — an output directory typed on
// the command line, one a request or the configuration names, a root a writer is
// handed — is made a path a name can be joined onto. A relative dir resolves
// against the working directory, as every path a person types does, and the empty
// string is the working directory itself, the place a join onto it lands. On
// Windows a bare drive such as "C:" is the current directory on that drive, and a
// path opening with a separator, such as `\out`, is that path on the current
// drive; joined onto either as written, a name opening with a separator would
// start from the top of the drive instead of beneath the directory named.
//
// An error means dir could not be made absolute, as when the working directory a
// relative dir resolves against cannot be read.
func DirectoryPath(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve the directory %q against the working directory: %w", dir, err)
	}
	return abs, nil
}
