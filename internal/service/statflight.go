package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// statResult holds the outcome of a single stat invocation.
type statResult struct {
	info os.FileInfo
	err  error
}

// statInFlight tracks a single in-progress stat for a key. The single-flight
// design ensures a frozen mount leaks at most one goroutine per unique key
// (until the OS returns), instead of one per caller. Joiners share the
// original timeout budget measured from when statFn actually started, so a
// permanently hanging flight only burns one full timeout - subsequent callers
// return immediately.
//
// startedNano is an atomic int64 (Unix nanoseconds). It is initialised to the
// caller-supplied time as a safe fallback for joiners that race the goroutine
// start, and overwritten with time.Now() just before statFn is called. Using
// an atomic avoids a mutex while still providing a happens-before guarantee
// between the goroutine write and the joiner read.
type statInFlight struct {
	startedNano atomic.Int64  // Unix ns; set at goroutine start, read by joiners
	done        chan struct{} // closed once result is populated
	result      statResult
}

// statFlightGroup deduplicates concurrent stats per key. Goroutines started
// here cannot be cancelled (stat calls have no context parameter) and are not
// joined on shutdown; blocking on them would hang the process on a frozen
// mount. Each goroutine holds a reference to the group, so the GC will not
// collect it while a stat is still pending.
type statFlightGroup struct {
	mu      sync.Mutex
	flights map[string]*statInFlight
}

// startOrJoin returns the in-flight stat for key, or starts a new one running
// statFn. A new flight kicks off a goroutine that (on completion) removes
// itself from the map. The map entry is compared by pointer so a
// still-hanging flight is not deleted when a newer flight for the same key
// has already replaced it.
func (g *statFlightGroup) startOrJoin(
	key string, now time.Time, statFn func() (os.FileInfo, error),
) (*statInFlight, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.flights == nil {
		g.flights = make(map[string]*statInFlight)
	}
	if existing, ok := g.flights[key]; ok {
		return existing, false
	}

	flight := &statInFlight{done: make(chan struct{})}
	flight.startedNano.Store(now.UnixNano()) // fallback for joiners that arrive before goroutine starts
	g.flights[key] = flight

	go func() {
		flight.startedNano.Store(time.Now().UnixNano()) // actual OS-call start time
		info, err := statFn()
		flight.result = statResult{info: info, err: err}
		close(flight.done) // happens-before vs. <-flight.done in readers

		g.mu.Lock()
		if g.flights[key] == flight {
			delete(g.flights, key)
		}
		g.mu.Unlock()
	}()

	return flight, true
}

// errStatTimeout marks a wait that exhausted its budget; match with errors.Is.
var errStatTimeout = errors.New("stat timeout")

func statTimeoutError(timeout time.Duration) error {
	return fmt.Errorf("%w after %s", errStatTimeout, timeout)
}

// waitStatFlight waits for a flight's result within the shared timeout budget.
//
// Budget origin:
//   - New flight (isNew=true): the goroutine has been spawned but may not have
//     started executing yet, so startedNano is not reliable. The caller gets
//     the full configured timeout from this point forward.
//   - Joiner (isNew=false): shares the budget from startedNano. Once the
//     goroutine has started, startedNano holds the actual OS-call start time;
//     before that it holds the caller-supplied fallback. Either way the total
//     wait is bounded by one timeout. If the budget is exhausted the joiner
//     returns immediately so a hung mount cannot stall its worker.
//
// ctx bounds the wait when non-nil; a nil ctx waits on the flight and timer only.
func waitStatFlight(
	flight *statInFlight, isNew bool, timeout time.Duration, ctx context.Context,
) (os.FileInfo, error) {
	remaining := timeout
	if !isNew {
		remaining = timeout - time.Since(time.Unix(0, flight.startedNano.Load()))
		if remaining <= 0 {
			return nil, statTimeoutError(timeout)
		}
	}

	timer := time.NewTimer(remaining)
	defer timer.Stop()

	var ctxDone <-chan struct{}
	if ctx != nil {
		ctxDone = ctx.Done()
	}

	select {
	case <-flight.done:
		return flight.result.info, flight.result.err
	case <-timer.C:
		return nil, statTimeoutError(timeout)
	case <-ctxDone:
		return nil, ctx.Err()
	}
}
