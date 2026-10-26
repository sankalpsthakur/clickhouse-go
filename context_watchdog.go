package clickhouse

import (
	"context"
	"sync"
)

// contextWatchdog runs callback if ctx is done before the returned stop function
// is called. It is used to interrupt in-flight work (for example by closing a
// TCP connection) when the caller still cares about cancellation, without
// acting on a cancel that races with a successful return.
//
// Example:
//
//	stopCW := contextWatchdog(ctx, func() { /* interrupt work */ })
//	defer stopCW()
//
// The watchdog goroutine exits when stop is called, or after callback returns.
// stop blocks until that exit. After stop returns, callback has either already
// finished or will never run: if both the exit channel and ctx.Done() are
// ready, stop wins and the callback is skipped.
func contextWatchdog(ctx context.Context, callback func()) (stop func()) {
	var wg sync.WaitGroup
	exit := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case <-exit:
			return
		case <-ctx.Done():
			// Prefer stop when it races with cancellation so a connection is
			// not closed after the caller has already released it to the pool.
			select {
			case <-exit:
				return
			default:
				callback()
			}
		}
	}()

	return func() {
		close(exit)
		wg.Wait()
	}
}
