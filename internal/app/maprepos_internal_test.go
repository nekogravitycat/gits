package app

import (
	"context"
	"fmt"
	"testing"

	"github.com/nekogravitycat/gits/internal/domain"
)

// TestMapRepos_CancelledWorkersUseTheCancelledCallback pins the fix for the interrupt bug: a repo
// that never got a concurrency slot before ctx was cancelled must come back through the caller's
// cancelled() callback, never as the zero value of T, so every result-bucket reconciliation in the
// codebase (SummarizeResults, domain.Summarize, ForeachResult.Failed) still adds up to len(repos).
func TestMapRepos_CancelledWorkersUseTheCancelledCallback(t *testing.T) {
	repos := make([]domain.Repo, 3)
	for i := range repos {
		repos[i] = domain.Repo{Name: fmt.Sprintf("r%d", i)}
	}

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{}, 2)
	release := make(chan struct{})

	// Only r0 and r1 ever get a semaphore slot (jobs=2): both block until released, which holds
	// the semaphore full and forces r2's dispatch to resolve via ctx.Done(), deterministically,
	// regardless of goroutine scheduling order.
	fn := func(ctx context.Context, r domain.Repo) string {
		started <- struct{}{}
		<-release
		return "ran:" + r.Name
	}
	cancelled := func(r domain.Repo) string { return "cancelled:" + r.Name }

	done := make(chan []string, 1)
	go func() {
		done <- mapRepos(ctx, 2, repos, fn, cancelled)
	}()

	<-started
	<-started
	cancel()
	close(release)

	results := <-done

	if len(results) != len(repos) {
		t.Fatalf("got %d results, want %d (len(repos)) -- a cancelled worker left a zero value out of the slice", len(results), len(repos))
	}
	for i, r := range results {
		if r == "" {
			t.Errorf("results[%d] is the zero value, not the cancelled() marker", i)
		}
	}
	if results[0] != "ran:r0" || results[1] != "ran:r1" {
		t.Errorf("expected r0 and r1 to run (they held the two job slots), got %v", results[:2])
	}
	if results[2] != "cancelled:r2" {
		t.Errorf("expected r2 to be reported via cancelled(), got %q", results[2])
	}
}

// TestMapRepos_GatesSpawnOnTheSemaphore proves --jobs bounds how many goroutines are ever
// in-flight, not just how many run fn concurrently at steady state: with jobs=1, only one call to
// fn should be active at any instant across the whole run.
func TestMapRepos_GatesSpawnOnTheSemaphore(t *testing.T) {
	repos := make([]domain.Repo, 4)
	for i := range repos {
		repos[i] = domain.Repo{Name: fmt.Sprintf("r%d", i)}
	}

	var active, maxActive int
	gate := make(chan struct{})
	fn := func(ctx context.Context, r domain.Repo) struct{} {
		active++
		if active > maxActive {
			maxActive = active
		}
		<-gate
		active--
		return struct{}{}
	}
	cancelled := func(domain.Repo) struct{} { return struct{}{} }

	done := make(chan []struct{}, 1)
	go func() {
		done <- mapRepos(context.Background(), 1, repos, fn, cancelled)
	}()

	for range repos {
		gate <- struct{}{}
	}
	<-done

	if maxActive > 1 {
		t.Errorf("jobs=1 allowed %d concurrent fn calls, want at most 1", maxActive)
	}
}
