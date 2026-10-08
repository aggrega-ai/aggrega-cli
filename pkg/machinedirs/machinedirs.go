// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package machinedirs decides where the files that belong to a machine, rather
// than to any one workspace, are kept: the global configuration file, and the
// root the machine-wide caches live under.
//
// Each platform has its own convention for both, and the locations follow it:
//
//   - On Windows the global configuration file is %APPDATA%\aggrega\config.yaml,
//     and the machine-wide caches are kept under %LOCALAPPDATA%\aggrega\cache.
//   - On every other platform the global configuration file is
//     $XDG_CONFIG_HOME/aggrega/config.yaml, or $HOME/.config/aggrega/config.yaml
//     when XDG_CONFIG_HOME is empty, and the machine-wide caches are kept in a
//     directory named aggrega under the per-user cache directory the standard
//     library's os package names: $XDG_CACHE_HOME, or $HOME/.cache, on Linux and
//     the other Unix systems, and $HOME/Library/Caches on macOS.
//
// A workspace keeps caches of its own inside the workspace; those are not
// decided here.
//
// # Read through a lookup, for a named platform
//
// Every answer here takes the environment as a lookup function and the platform
// as a value, and reads neither from the running process. That is what lets one
// resolution read one view of the environment throughout, and what lets a test
// state a whole machine — another platform's included — without changing the
// process it runs in. [Host] names the platform this program was built for,
// which is the one every production caller passes.
package machinedirs

import (
	"errors"
	"path/filepath"
	"runtime"
)

// Platform names an operating system the way the Go toolchain names it — the
// values runtime.GOOS takes — because that is the one spelling the program is
// built for and the one a caller can state without translating.
type Platform string

const (
	// Windows is the platform whose conventions place the machine's files under
	// %APPDATA% and %LOCALAPPDATA%.
	Windows Platform = "windows"

	// Darwin is macOS, whose per-user cache directory is $HOME/Library/Caches.
	Darwin Platform = "darwin"
)

// Host returns the platform this program was built for.
func Host() Platform { return Platform(runtime.GOOS) }

const (
	// dirName is the directory the program owns beneath each per-user base
	// directory, for its configuration and for its caches alike.
	dirName = "aggrega"

	// configFileName is the global configuration file's name inside dirName.
	configFileName = "config.yaml"

	// windowsCacheDirName is the directory the machine-wide caches take inside
	// dirName on Windows. %LOCALAPPDATA% holds an application's local data of
	// every kind rather than its caches alone, so the caches take a directory of
	// their own there; every other platform's base is already a directory for
	// caches.
	windowsCacheDirName = "cache"
)

// GlobalConfigFile returns where the global configuration file is kept on p
// when nothing names one, read through lookup, and whether a location resolves.
//
// On Windows it is APPDATA's aggrega\config.yaml. Elsewhere it is
// XDG_CONFIG_HOME's aggrega/config.yaml, or HOME's .config/aggrega/config.yaml
// when XDG_CONFIG_HOME is empty. It reports false when the variables the
// platform's rule reads are empty, so a caller treats the location as
// unresolvable rather than reading or writing a relative path.
//
// It is the one place this location is decided. Loading the global layer, the
// file a configuration-writing command writes globally and what `aggrega config
// init` reports all reach it, so the three cannot name different files.
func GlobalConfigFile(p Platform, lookup func(string) string) (string, bool) {
	base := configBase(p, lookup)
	if base == "" {
		return "", false
	}
	return filepath.Join(base, dirName, configFileName), true
}

// configBase returns the per-user directory the global configuration file's
// own directory sits in on p, or "" when none resolves.
func configBase(p Platform, lookup func(string) string) string {
	if p == Windows {
		return lookup("APPDATA")
	}
	if base := lookup("XDG_CONFIG_HOME"); base != "" {
		return base
	}
	if home := lookup("HOME"); home != "" {
		return filepath.Join(home, ".config")
	}
	return ""
}

// CacheRoot returns the root the machine-wide caches are kept under on p, read
// through lookup: LOCALAPPDATA's aggrega\cache on Windows, and the per-user
// cache directory's aggrega elsewhere. When no root resolves, the error names
// what the platform's rule found missing.
//
// It is the one place this location is decided. The caches themselves and what
// `aggrega config init` reports both reach it.
func CacheRoot(p Platform, lookup func(string) string) (string, error) {
	return cacheRootNamed(p, lookup, dirName)
}

// SiblingCacheRoot returns the root of a set of caches kept apart from the ones
// [CacheRoot] names: the same layout, under a directory named like CacheRoot's
// with suffix appended and sitting beside it, so neither set can reach a byte of
// the other's.
func SiblingCacheRoot(p Platform, lookup func(string) string, suffix string) (string, error) {
	return cacheRootNamed(p, lookup, dirName+suffix)
}

// cacheRootNamed is the cache-root layout for a directory named name beneath
// the per-user cache directory.
func cacheRootNamed(p Platform, lookup func(string) string, name string) (string, error) {
	base, err := userCacheDir(p, lookup)
	if err != nil {
		return "", err
	}
	if p == Windows {
		return filepath.Join(base, name, windowsCacheDirName), nil
	}
	return filepath.Join(base, name), nil
}

// userCacheDir returns the per-user cache directory on p, read through lookup.
//
// It is the standard library's own rule for that directory — the one the os
// package applies to the running process — stated over a lookup and a platform
// the caller supplies, which the standard library's form cannot take. Restating
// it is what lets the rule be read for a platform other than the host and from
// an environment other than the process's. The restatement is held to the
// standard library's answer on the host by the cache-location guard beside the
// compiled-module cache, which asks the standard library independently, so the
// two cannot come apart unnoticed.
func userCacheDir(p Platform, lookup func(string) string) (string, error) {
	switch p {
	case Windows:
		if dir := lookup("LOCALAPPDATA"); dir != "" {
			return dir, nil
		}
		return "", errors.New("LOCALAPPDATA is not set")
	case Darwin, "ios":
		if home := lookup("HOME"); home != "" {
			return home + "/Library/Caches", nil
		}
		return "", errors.New("HOME is not set")
	case "plan9":
		if home := lookup("home"); home != "" {
			return home + "/lib/cache", nil
		}
		return "", errors.New("home is not set")
	default:
		if dir := lookup("XDG_CACHE_HOME"); dir != "" {
			if !filepath.IsAbs(dir) {
				return "", errors.New("XDG_CACHE_HOME is not an absolute path")
			}
			return dir, nil
		}
		if home := lookup("HOME"); home != "" {
			return home + "/.cache", nil
		}
		return "", errors.New("neither XDG_CACHE_HOME nor HOME is set")
	}
}
