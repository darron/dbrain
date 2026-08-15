package remote

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

type synchronizedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}

func (b *synchronizedBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Len()
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

func TestRunProcessHeartbeatLogsDelayedTick(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time, 2)
	var out synchronizedBuffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		runProcessHeartbeat(ctx, ticks, &out, time.Second, 2*time.Second)
	}()

	base := time.Unix(0, 0)
	ticks <- base
	ticks <- base.Add(3 * time.Second)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && out.Len() == 0 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done

	for _, want := range []string{"process heartbeat delayed", "heartbeat_lag=2s", "heartbeat_elapsed=3s"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("heartbeat log missing %q: %s", want, out.String())
		}
	}
}

func TestRunProcessHeartbeatIgnoresOnTimeTicks(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time, 2)
	var out synchronizedBuffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		runProcessHeartbeat(ctx, ticks, &out, time.Second, 2*time.Second)
	}()

	base := time.Unix(0, 0)
	ticks <- base
	ticks <- base.Add(2 * time.Second)
	time.Sleep(10 * time.Millisecond)
	cancel()
	<-done

	if out.Len() != 0 {
		t.Fatalf("unexpected heartbeat log: %s", out.String())
	}
}
