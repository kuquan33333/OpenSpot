package gobackend

import (
	"context"
	"errors"
	"sync"
)

var ErrDownloadCancelled = errors.New("download cancelled")
var ErrExtensionRequestCancelled = errors.New("extension request cancelled")

type cancelEntry struct {
	ctx      context.Context
	cancel   context.CancelFunc
	canceled bool
	refs     int
}

type cancelRegistry struct {
	mu      sync.Mutex
	entries map[string]*cancelEntry
}

var (
	downloadCancels         = &cancelRegistry{entries: make(map[string]*cancelEntry)}
	extensionRequestCancels = &cancelRegistry{entries: make(map[string]*cancelEntry)}
)

func (r *cancelRegistry) init(id string) context.Context {
	if id == "" {
		return context.Background()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if entry, ok := r.entries[id]; ok {
		if entry.ctx == nil {
			ctx, cancel := context.WithCancel(context.Background())
			entry.ctx = ctx
			entry.cancel = cancel
			if entry.canceled && entry.cancel != nil {
				entry.cancel()
			}
		}
		entry.refs++
		return entry.ctx
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.entries[id] = &cancelEntry{
		ctx:      ctx,
		cancel:   cancel,
		canceled: false,
		refs:     1,
	}
	return ctx
}

func (r *cancelRegistry) context(id string) context.Context {
	if id == "" {
		return context.Background()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry, ok := r.entries[id]; ok && entry.ctx != nil {
		return entry.ctx
	}
	return context.Background()
}

func (r *cancelRegistry) requestCancel(id string) {
	if id == "" {
		return
	}

	r.mu.Lock()
	if entry, ok := r.entries[id]; ok {
		entry.canceled = true
		if entry.cancel != nil {
			entry.cancel()
		}
	} else {
		r.entries[id] = &cancelEntry{canceled: true}
	}
	r.mu.Unlock()
}

func (r *cancelRegistry) requestCancelActive() []string {
	r.mu.Lock()
	ids := make([]string, 0, len(r.entries))
	for id, entry := range r.entries {
		if entry == nil || entry.refs <= 0 {
			continue
		}
		entry.canceled = true
		if entry.cancel != nil {
			entry.cancel()
		}
		ids = append(ids, id)
	}
	r.mu.Unlock()
	return ids
}

func (r *cancelRegistry) isCancelled(id string) bool {
	if id == "" {
		return false
	}

	r.mu.Lock()
	entry, ok := r.entries[id]
	canceled := ok && entry.canceled
	r.mu.Unlock()
	return canceled
}

func (r *cancelRegistry) resetIfIdle(id string) {
	if id == "" {
		return
	}

	r.mu.Lock()
	if entry, ok := r.entries[id]; ok && entry.refs <= 0 {
		delete(r.entries, id)
	}
	r.mu.Unlock()
}

func (r *cancelRegistry) release(id string) {
	if id == "" {
		return
	}

	r.mu.Lock()
	if entry, ok := r.entries[id]; ok {
		entry.refs--
		if entry.refs <= 0 {
			delete(r.entries, id)
		}
	}
	r.mu.Unlock()
}

func initDownloadCancel(itemID string) context.Context {
	return downloadCancels.init(itemID)
}

func downloadCancelContext(itemID string) context.Context {
	return downloadCancels.context(itemID)
}

func cancelDownload(itemID string) {
	if itemID == "" {
		return
	}
	downloadCancels.requestCancel(itemID)
	RemoveItemProgress(itemID)
}

func cancelAllActiveDownloads() []string {
	itemIDs := downloadCancels.requestCancelActive()
	for _, itemID := range itemIDs {
		RemoveItemProgress(itemID)
	}
	return itemIDs
}

func isDownloadCancelled(itemID string) bool {
	return downloadCancels.isCancelled(itemID)
}

func resetDownloadCancel(itemID string) {
	downloadCancels.resetIfIdle(itemID)
}

func clearDownloadCancel(itemID string) {
	downloadCancels.release(itemID)
}

func initExtensionRequestCancel(requestID string) context.Context {
	return extensionRequestCancels.init(requestID)
}

func extensionRequestCancelContext(requestID string) context.Context {
	return extensionRequestCancels.context(requestID)
}

func cancelExtensionRequest(requestID string) {
	extensionRequestCancels.requestCancel(requestID)
}

func isExtensionRequestCancelled(requestID string) bool {
	return extensionRequestCancels.isCancelled(requestID)
}

func clearExtensionRequestCancel(requestID string) {
	extensionRequestCancels.release(requestID)
}
