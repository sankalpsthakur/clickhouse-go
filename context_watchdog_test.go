package clickhouse

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gateDoneCtx delays returning the Done channel until allow is closed.
// Holding the watchdog in ctx.Done() evaluation lets stop run first, so both
// select cases are ready when the goroutine proceeds — the race in issue 2076.
type gateDoneCtx struct {
	context.Context
	entered chan struct{}
	allow   chan struct{}
	once    sync.Once
}

func (c *gateDoneCtx) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	<-c.allow
	return c.Context.Done()
}

func TestContextWatchdog(t *testing.T) {
	t.Run("callback should be called once", func(t *testing.T) {
		called := atomic.Int32{}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		stopCW := contextWatchdog(ctx, func() {
			called.Add(1)
		})

		<-ctx.Done()

		// Give it some more time to make sure watch dog has enough time to
		// call callback multiple times
		time.Sleep(100 * time.Millisecond)
		assert.Equal(t, int32(1), called.Load(), "callback should be called only once")

		stopCW()
		assert.Equal(t, int32(1), called.Load(), "callback should be called only once even after stopping watchdog")
	})

	t.Run("callback should not be called during normal exit before context cancellation", func(t *testing.T) {
		called := atomic.Int32{}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		stopCW := contextWatchdog(ctx, func() {
			called.Add(1)
		})
		stopCW() // normal exit

		assert.Equal(t, int32(0), called.Load(), "callback should not be called during normal exit")
	})

	t.Run("stop waits for in-flight callback", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			started := make(chan struct{})
			unblock := make(chan struct{})
			stopCW := contextWatchdog(ctx, func() {
				close(started)
				<-unblock
			})

			cancel()
			<-started

			done := make(chan struct{})
			go func() {
				stopCW()
				close(done)
			}()

			synctest.Wait()
			select {
			case <-done:
				t.Fatal("stop returned before the in-flight callback finished")
			default:
			}

			close(unblock)
			<-done
		})
	})

	t.Run("No goroutines should be left out after stopping ContextWatchdog", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			// when context is cancelled
			called := atomic.Int32{}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()

			stopCW := contextWatchdog(ctx, func() {
				called.Add(1)
			})

			<-ctx.Done()

			// Give it some more time to make sure watch dog has enough time to
			// call callback multiple times
			time.Sleep(100 * time.Millisecond)
			assert.Equal(t, int32(1), called.Load(), "callback should be called only once")

			stopCW()
			assert.Equal(t, int32(1), called.Load(), "callback should be called only once even after stopping watchdog")

			synctest.Wait()
		})

		synctest.Test(t, func(t *testing.T) {
			// during normal exit
			called := atomic.Int32{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			stopCW := contextWatchdog(ctx, func() {
				called.Add(1)
			})
			stopCW() // normal exit

			assert.Equal(t, int32(0), called.Load(), "callback should not be called during normal exit")

			synctest.Wait()
		})
	})
}

func TestIssue2076WatchdogDoesNotClosePooledConnAfterStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		entered := make(chan struct{})
		allow := make(chan struct{})
		gctx := &gateDoneCtx{Context: ctx, entered: entered, allow: allow}

		var (
			stopReturned    atomic.Bool
			closedAfterStop atomic.Bool
			closeCount      atomic.Int32
		)

		netConn := &mockNetConn{}
		conn := createMockConnect(netConn)

		var pooled bool
		var releaseErr error
		b := &batch{
			ctx:  gctx,
			conn: conn,
			connRelease: func(c *connect, err error) {
				releaseErr = err
				if err != nil {
					return
				}
				pooled = true
			},
		}

		interrupted := false
		stopCW := contextWatchdog(b.ctx, func() {
			if b.conn == nil {
				return
			}
			closeCount.Add(1)
			if stopReturned.Load() {
				closedAfterStop.Store(true)
			}
			_ = b.conn.conn.Close()
			interrupted = true
		})

		<-entered

		// Cancel while the watchdog is still inside Done(), then let Done
		// return. stopCW is waiting, so both select cases become ready at once.
		go func() {
			cancel()
			close(allow)
		}()

		stopCW()
		stopReturned.Store(true)
		b.releaseAfterSend(nil, interrupted)

		synctest.Wait()

		assert.False(t, closedAfterStop.Load(), "watchdog must not close the connection after stop returns")
		assert.Equal(t, int32(0), closeCount.Load(), "stop must skip the callback when it races with an already-cancelled context")
		assert.True(t, pooled, "healthy connection should be returned to the pool")
		assert.NoError(t, releaseErr)
		assert.False(t, netConn.closed)
		assert.False(t, interrupted)
	})
}

func TestIssue2076InFlightWatchdogDoesNotPoolClosedConn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		started := make(chan struct{})
		unblock := make(chan struct{})

		netConn := &mockNetConn{}
		conn := createMockConnect(netConn)

		var pooled bool
		var releaseErr error
		b := &batch{
			ctx:  ctx,
			conn: conn,
			connRelease: func(c *connect, err error) {
				releaseErr = err
				if err != nil {
					return
				}
				pooled = true
			},
		}

		var stopReturned atomic.Bool
		var closedAfterStop atomic.Bool
		interrupted := false
		stopCW := contextWatchdog(b.ctx, func() {
			close(started)
			<-unblock
			if stopReturned.Load() {
				closedAfterStop.Store(true)
			}
			_ = b.conn.conn.Close()
			interrupted = true
		})

		cancel()
		<-started

		go func() {
			close(unblock)
		}()

		stopCW()
		stopReturned.Store(true)
		b.releaseAfterSend(nil, interrupted)

		synctest.Wait()

		assert.False(t, closedAfterStop.Load(), "watchdog must not close the connection after stop returns")
		assert.True(t, interrupted)
		assert.True(t, netConn.closed)
		assert.False(t, pooled, "closed connection must not be returned to the pool")
		require.Error(t, releaseErr)
		assert.ErrorIs(t, releaseErr, context.Canceled)
	})
}
