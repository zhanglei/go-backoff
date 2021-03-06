package backoff

import (
	"context"

	"github.com/pkg/errors"
)

// Executer represents an operation that can be performed within the Retry
// utility method.
type Executer interface {
	Execute(context.Context) error
}

// ExecuteFunc is an Executer that is represented by a single function
type ExecuteFunc func(context.Context) error

// Execute executes the operation
func (f ExecuteFunc) Execute(ctx context.Context) error {
	return f(ctx)
}

type PermanentError interface {
	IsPermanent() bool
}

type permanentError struct {
	error
}

func (e *permanentError) IsPermanent() bool {
	return true
}

func MarkPermanent(err error) error {
	return &permanentError{error: err}
}

// IsPermanentError returns true if the given error is a permanent error. Permanent
// errors are those that implements the `PermanentError` interface and returns
// `true` for the `IsPermanent` method.
func IsPermanentError(err error) bool {
	if err == nil {
		return false
	}
	if perr, ok := err.(PermanentError); ok {
		return perr.IsPermanent()
	}
	if cerr := errors.Cause(err); cerr != err {
		return IsPermanentError(cerr)
	}
	return false
}

// Retry is a convenience wrapper around the backoff algorithm. If your target
// operation can be nicely enclosed in the `Executer` interface, this will
// remove your need to write much of the boilerplate.
func Retry(ctx context.Context, p Policy, e Executer) error {
	b, cancel := p.Start(ctx)
	defer cancel()

	for {
		err := e.Execute(ctx)
		if err == nil {
			return nil
		}

		if IsPermanentError(err) {
			return errors.Wrap(err, `permanent error`)
		}

		select {
		case <-b.Done():
			return errors.Wrap(err, `retry attempts failed`)
		case <-b.Next():
			// no op, continue
		}
	}
}

func newBaseBackoff(ctx context.Context, maxRetries int) *baseBackoff {
	backoffCtx, cancel := context.WithCancel(ctx)
	return &baseBackoff{
		cancelFunc: cancel,
		ctx:        backoffCtx,
		maxRetries: maxRetries,
		next:       make(chan struct{}),
	}
}

func (b *baseBackoff) Done() <-chan struct{} {
	return b.ctx.Done()
}

func (b *baseBackoff) cancelLocked() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.cancel()
}

// note: caller must lock
func (b *baseBackoff) cancel() {
	if b.next != nil {
		close(b.next)
		b.next = nil
	}
	b.cancelFunc()
}

func (b *baseBackoff) fire() {
	select {
	case <-b.ctx.Done():
		return
	default:
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.next == nil {
		return
	}

	b.next <- struct{}{}
	if b.maxRetries > 0 {
		if b.maxRetries <= b.callCount {
			b.cancel()
		} else {
			b.callCount++
		}
	}
}
