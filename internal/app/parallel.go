package app

import (
	"context"
	"sync"
)

// parallelBounded runs workFn over [0, n) with at most concurrency goroutines.
// Returns the index where dispatch stopped (n on success, earlier on ctx
// cancellation). All dispatched goroutines are awaited before return.
//
// The fast-path ctx.Err() check before the select is load-bearing: Go's select
// pseudo-randomly picks between a free semaphore slot and a cancelled context,
// so without it pre-cancelled-ctx behavior is untestable.
func parallelBounded(ctx context.Context, n, concurrency int, workFn func(i int)) int {
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	defer wg.Wait()
	for i := range n {
		if ctx.Err() != nil {
			return i
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return i
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			workFn(i)
		}(i)
	}
	return n
}
