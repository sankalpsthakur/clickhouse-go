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
// finished or will never run: a stop that races with ctx cancellation wins, so
// the callback is not started once stop has been called.
func contextWatchdog(ctx context.Context, callback func()) (stop func()) {
	var (
		once sync.Once
		wg   sync.WaitGroup
	)
	exit := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case <-exit:
			return
		case <-ctx.Done():
			once.Do(callback)
		}
	}()

	return func() {
		// Claim the once so a callback that has not started is skipped. If the
		// callback is already running, this waits for it to finish.
		// callback must not call stop: that would deadlock on sync.Once.
		once.Do(func() {})
		close(exit)
		wg.Wait()
	}
}
