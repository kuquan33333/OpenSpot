package gobackend

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/dop251/goja"
)

const extensionResolutionTimeout = 60 * time.Second

// resolutionBudget counts total resolver time across URL refreshes and retries.
// Only native transfers with received bytes and bounded native conversion work
// pause it; progress/status callbacks cannot reset the allowance.
type resolutionBudget struct {
	ctx        context.Context
	cancel     context.CancelCauseFunc
	mu         sync.Mutex
	remaining  time.Duration
	started    time.Time
	timer      *time.Timer
	generation uint64
	pauses     int
	charges    int
	stopped    bool
}

func newResolutionBudget(parent context.Context, allowance time.Duration) *resolutionBudget {
	ctx, cancel := context.WithCancelCause(parent)
	b := &resolutionBudget{ctx: ctx, cancel: cancel, remaining: allowance}
	b.mu.Lock()
	b.armLocked()
	b.mu.Unlock()
	return b
}

func (b *resolutionBudget) armLocked() {
	b.started = time.Now()
	b.generation++
	generation := b.generation
	b.timer = time.AfterFunc(b.remaining, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.stopped || (b.pauses > 0 && b.charges == 0) || b.generation != generation {
			return
		}
		b.remaining = 0
		b.cancel(context.DeadlineExceeded)
	})
}

func (b *resolutionBudget) pause() func() {
	b.mu.Lock()
	if b.stopped || b.ctx.Err() != nil {
		b.mu.Unlock()
		return func() {}
	}
	if b.pauses == 0 && b.charges == 0 {
		b.timer.Stop()
		b.generation++
		b.remaining -= time.Since(b.started)
		if b.remaining <= 0 {
			b.remaining = 0
			b.cancel(context.DeadlineExceeded)
			b.mu.Unlock()
			return func() {}
		}
	}
	b.pauses++
	b.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.pauses--
			if b.pauses == 0 && b.charges == 0 && !b.stopped && b.ctx.Err() == nil {
				b.armLocked()
			}
		})
	}
}

// Resolver work must keep spending the allowance even while parallel segment
// workers are paused in native network reads.
func (b *resolutionBudget) charge() func() {
	b.mu.Lock()
	if b.stopped || b.ctx.Err() != nil {
		b.mu.Unlock()
		return func() {}
	}
	if b.charges == 0 && b.pauses > 0 {
		b.armLocked()
	}
	b.charges++
	b.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.charges--
			if b.charges == 0 && b.pauses > 0 && !b.stopped && b.ctx.Err() == nil {
				b.timer.Stop()
				b.generation++
				b.remaining -= time.Since(b.started)
				if b.remaining <= 0 {
					b.remaining = 0
					b.cancel(context.DeadlineExceeded)
				}
			}
		})
	}
}

func (b *resolutionBudget) remainingTime() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.remaining
	if (b.pauses == 0 || b.charges > 0) && !b.stopped {
		remaining -= time.Since(b.started)
	}
	if remaining < 0 || b.ctx.Err() != nil {
		return 0
	}
	return remaining
}

func (b *resolutionBudget) stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopped = true
	b.generation++
	b.timer.Stop()
	b.cancel(context.Canceled)
}

func (r *extensionRuntime) currentResolutionBudget() *resolutionBudget {
	r.resolutionMu.RLock()
	defer r.resolutionMu.RUnlock()
	return r.resolutionBudget
}

func (r *extensionRuntime) beginResolutionBudget(ctx context.Context, allowance time.Duration) (context.Context, func()) {
	b := newResolutionBudget(ctx, allowance)
	r.resolutionMu.Lock()
	r.resolutionBudget = b
	r.resolutionMu.Unlock()
	return b.ctx, func() {
		b.stop()
		r.resolutionMu.Lock()
		if r.resolutionBudget == b {
			r.resolutionBudget = nil
		}
		r.resolutionMu.Unlock()
	}
}

func (r *extensionRuntime) getResolutionRemainingMs(goja.FunctionCall) goja.Value {
	if b := r.currentResolutionBudget(); b != nil {
		return r.vm.ToValue(b.remainingTime().Milliseconds())
	}
	return r.vm.ToValue(extensionResolutionTimeout.Milliseconds())
}

// A successful segment/range establishes the current native transfer. Later
// parts need no new resolution, so their headers and first byte are transfer
// time too. Initial requests and retries still spend the resolver allowance.
func (r *extensionRuntime) doResolutionTransfer(client *http.Client, req *http.Request, continuation bool) (*http.Response, error) {
	b := r.currentResolutionBudget()
	resp, err := func() (*http.Response, error) {
		if b != nil {
			if continuation {
				defer b.pause()()
			} else {
				defer b.charge()()
			}
		}
		return client.Do(req)
	}()
	if b != nil && resp != nil && resp.Body != nil && (resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent) {
		resp.Body = &resolutionTransferBody{ReadCloser: resp.Body, budget: b, receivedBytes: continuation}
	}
	return resp, err
}

type resolutionTransferBody struct {
	io.ReadCloser
	budget        *resolutionBudget
	receivedBytes bool // accessed only by the body's reader
}

func (b *resolutionTransferBody) Read(p []byte) (int, error) {
	// Pause only the native read, so JS progress callbacks (which can invoke
	// more resolvers) and retry waits continue spending the same allowance.
	if b.receivedBytes {
		defer b.budget.pause()()
	} else {
		defer b.budget.charge()()
	}
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.receivedBytes = true
	}
	return n, err
}

func (r *extensionRuntime) waitResolutionRetry(ctx context.Context, delay time.Duration) error {
	if b := r.currentResolutionBudget(); b != nil {
		defer b.charge()()
	}
	return waitTransferRetry(ctx, delay)
}
