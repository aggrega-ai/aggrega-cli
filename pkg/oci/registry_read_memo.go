// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package oci

import (
	"context"
	"errors"
	"sync"

	"oras.land/oras-go/v2"
)

// WithRegistryReadMemo returns ctx holding a memo of registry reads that lasts as
// long as ctx: the tag listings, artifact pulls and plugin-entity reads made
// through MemoizedListTags, MemoizedPull and MemoizedFetchPluginEntity under it.
//
// The memo lets a run read ahead. Reads made under PrefetchingRegistryReads go to
// the registry, several at once where the caller runs them so, and their answers —
// failures included — are kept. A later read of the same shape, reference and tag
// takes the kept answer instead of asking the registry again, and takes it alone:
// each kept answer is handed out once, so a run that reads ahead sends the registry
// the requests a run reading one at a time sends, and no more for any read it went
// on to make. A read finding no kept answer goes to the registry as it would
// without a memo.
//
// A kept failure is an answer like any other: the read that takes it fails with
// it, exactly as it would had it asked the registry itself and been refused. A read
// made ahead that failed — the registry refused it, or did not answer — therefore
// fails the read that takes it, where a read made one at a time might have been
// asked a moment later; keeping the failure is what keeps the first failure a run
// meets, and the number of requests it sends, the same as reading one at a time.
//
// Nothing in the memo is written to disk, and it ends with ctx, so one run's
// answers are never handed to another. A context holding no memo reads the
// registry every time, which is what every caller that never reads ahead holds.
func WithRegistryReadMemo(ctx context.Context) context.Context {
	return context.WithValue(ctx, registryReadMemoKey{}, &registryReadMemo{answers: map[registryRead]*keptAnswer{}})
}

// HoldsRegistryReadMemo reports whether ctx holds a memo of registry reads
// (WithRegistryReadMemo). A caller that would read ahead asks here first: reading
// ahead with nowhere to keep the answers would send every request twice.
func HoldsRegistryReadMemo(ctx context.Context) bool {
	return registryReadMemoOf(ctx) != nil
}

// PrefetchingRegistryReads returns ctx under which the memoized reads go to the
// registry and keep their answers in the memo ctx holds, for the read that later
// asks for the same shape, reference and tag to take. Two reads ahead of the same
// thing ask the registry once and both see that one answer, and a read ahead of
// something already kept and not yet taken asks nothing. Without a memo in ctx it
// changes nothing.
func PrefetchingRegistryReads(ctx context.Context) context.Context {
	return context.WithValue(ctx, prefetchingKey{}, true)
}

// MemoizedListTags is ListTags of target through the memo ctx holds, if any;
// reference names the repository target addresses and keys the answer.
func MemoizedListTags(ctx context.Context, reference string, target oras.ReadOnlyTarget) ([]string, error) {
	return memoizedRead(ctx, registryRead{shape: readTagList, reference: reference}, func() ([]string, error) {
		return ListTags(ctx, target)
	})
}

// MemoizedPull is Pull of tag from target, with no expected content media type,
// through the memo ctx holds, if any; reference names the repository target
// addresses and keys the answer together with tag.
func MemoizedPull(ctx context.Context, reference string, target oras.ReadOnlyTarget, tag string) (*PullResult, error) {
	return memoizedRead(ctx, registryRead{shape: readContent, reference: reference, tag: tag}, func() (*PullResult, error) {
		return Pull(ctx, target, tag)
	})
}

// MemoizedFetchPluginEntity is FetchPluginEntity of tag from target through the
// memo ctx holds, if any; reference names the repository target addresses and
// keys the answer together with tag. It is kept apart from MemoizedPull of the same
// tag, because the two read different parts of one manifest.
func MemoizedFetchPluginEntity(ctx context.Context, reference string, target oras.ReadOnlyTarget, tag string) (*PullResult, error) {
	return memoizedRead(ctx, registryRead{shape: readPluginEntity, reference: reference, tag: tag}, func() (*PullResult, error) {
		return FetchPluginEntity(ctx, target, tag)
	})
}

// registryReadShape is what a memoized read asks of a repository. It is part of
// the key, because one reference and tag answer differently to a tag listing, a
// pull of the content and a read of a plugin manifest's entity.
type registryReadShape uint8

const (
	readTagList registryReadShape = iota + 1
	readContent
	readPluginEntity
)

// registryRead keys one answer in the memo.
type registryRead struct {
	shape     registryReadShape
	reference string
	tag       string
}

// keptAnswer is one read's answer, or the read still under way. ready closes once
// value and err are set.
type keptAnswer struct {
	ready chan struct{}
	value any
	err   error
}

// registryReadMemo holds the answers reads ahead have kept and no read has taken.
type registryReadMemo struct {
	mu      sync.Mutex
	answers map[registryRead]*keptAnswer
}

type registryReadMemoKey struct{}

type prefetchingKey struct{}

func registryReadMemoOf(ctx context.Context) *registryReadMemo {
	memo, _ := ctx.Value(registryReadMemoKey{}).(*registryReadMemo)
	return memo
}

// errReadAheadAbandoned answers a read ahead that waited on another read ahead of
// the same thing which ended in a panic rather than an answer. Only a read ahead can
// see it; the panicking read's slot is removed, so a later read asks the registry.
var errReadAheadAbandoned = errors.New("a read ahead of this registry read ended without an answer")

// memoizedRead answers read through the memo ctx holds: under PrefetchingRegistryReads
// it keeps what fetch answers; otherwise it takes a kept answer if one is held and
// calls fetch if not.
//
// A read under a context that has already ended takes nothing, so it fails at the
// same request it would fail at with no memo, rather than being answered by a read
// made before the context ended.
func memoizedRead[T any](ctx context.Context, read registryRead, fetch func() (T, error)) (T, error) {
	memo := registryReadMemoOf(ctx)
	if memo == nil {
		return fetch()
	}
	if _, ahead := ctx.Value(prefetchingKey{}).(bool); ahead {
		return keepAnswer(memo, read, fetch)
	}
	if ctx.Err() == nil {
		if kept, ok := memo.take(read); ok {
			<-kept.ready
			value, _ := kept.value.(T)
			return value, kept.err
		}
	}
	return fetch()
}

// keepAnswer makes read once for the memo and keeps its answer; a read ahead of
// something already kept, or under way, waits for that answer instead of asking
// again.
func keepAnswer[T any](memo *registryReadMemo, read registryRead, fetch func() (T, error)) (T, error) {
	memo.mu.Lock()
	kept, held := memo.answers[read]
	if !held {
		kept = &keptAnswer{ready: make(chan struct{})}
		memo.answers[read] = kept
	}
	memo.mu.Unlock()
	if held {
		<-kept.ready
		value, _ := kept.value.(T)
		return value, kept.err
	}
	answered := false
	defer func() {
		if !answered {
			kept.err = errReadAheadAbandoned
			memo.mu.Lock()
			delete(memo.answers, read)
			memo.mu.Unlock()
		}
		close(kept.ready)
	}()
	value, err := fetch()
	kept.value, kept.err = value, err
	answered = true
	return value, err
}

// take removes and returns the answer kept for read, if one is.
func (m *registryReadMemo) take(read registryRead) (*keptAnswer, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept, ok := m.answers[read]
	if ok {
		delete(m.answers, read)
	}
	return kept, ok
}
