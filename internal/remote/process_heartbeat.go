package remote

import (
	"context"
	"fmt"
	"io"
	"time"
)

const (
	processHeartbeatInterval   = time.Second
	processHeartbeatMaxElapsed = 2 * time.Second
)

func startProcessHeartbeat(ctx context.Context, out io.Writer) func() {
	if ctx == nil || out == nil {
		return func() {}
	}
	monitorCtx, cancel := context.WithCancel(ctx)
	ticker := time.NewTicker(processHeartbeatInterval)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runProcessHeartbeat(monitorCtx, ticker.C, out, processHeartbeatInterval, processHeartbeatMaxElapsed)
	}()
	return func() {
		cancel()
		ticker.Stop()
		<-done
	}
}

func runProcessHeartbeat(ctx context.Context, ticks <-chan time.Time, out io.Writer, interval time.Duration, maxElapsed time.Duration) {
	var previous time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case tick, ok := <-ticks:
			if !ok {
				return
			}
			if !previous.IsZero() {
				elapsed := tick.Sub(previous)
				if elapsed > maxElapsed {
					lag := elapsed - interval
					if lag < 0 {
						lag = 0
					}
					_, _ = fmt.Fprintf(out, "WARNING process heartbeat delayed: heartbeat_elapsed=%s heartbeat_lag=%s expected_interval=%s\n", elapsed, lag, interval)
				}
			}
			previous = tick
		}
	}
}
