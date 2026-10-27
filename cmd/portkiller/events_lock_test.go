package main

import (
	"errors"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestLockEventsSerializesWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	held := lockEvents(path, true)
	if held == nil {
		t.Fatal("first lock must succeed")
	}

	acquired := make(chan struct{})
	done := make(chan struct{})
	go func() {
		l := lockEvents(path, true)
		close(acquired)
		unlockEvents(l)
		close(done)
	}()
	select {
	case <-acquired:
		t.Fatal("second writer acquired the lock while it was held")
	case <-time.After(100 * time.Millisecond):
	}

	unlockEvents(held)
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("lock was not released")
	}
	<-done
}

func TestLockEventsFlockFailure(t *testing.T) {
	old := eventsFlock
	t.Cleanup(func() { eventsFlock = old })
	eventsFlock = func(int, int) error { return errors.New("flock denied") }

	if l := lockEvents(filepath.Join(t.TempDir(), "e.jsonl"), true); l != nil {
		unlockEvents(l)
		t.Fatal("a flock failure must yield no lock")
	}
	unlockEvents(nil) // must be a no-op
}

func TestLockEventsRetriesEINTR(t *testing.T) {
	old := eventsFlock
	t.Cleanup(func() { eventsFlock = old })
	calls := 0
	eventsFlock = func(int, int) error {
		calls++
		if calls == 1 {
			return syscall.EINTR
		}
		return nil
	}

	l := lockEvents(filepath.Join(t.TempDir(), "e.jsonl"), false)
	if l == nil {
		t.Fatal("EINTR must be retried")
	}
	unlockEvents(l)
	if calls < 2 {
		t.Errorf("flock calls = %d, want a retry", calls)
	}
}

func TestLockEventsMissingDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "e.jsonl")
	if l := lockEvents(path, true); l != nil {
		unlockEvents(l)
		t.Fatal("a missing directory must not yield a lock")
	}
	if evs := loadEvents(path); evs != nil {
		t.Errorf("loadEvents = %+v, want nil", evs)
	}
}

func TestAppendEventsConcurrentNoLoss(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "events.jsonl")
	const writers, each = 20, 5
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < each; j++ {
				appendEvents(path, []Event{{Time: time.Now(), Kind: "appeared", Proto: "tcp", Port: "1", PID: "1"}})
			}
		}()
	}
	wg.Wait()

	if got := loadEvents(path); len(got) != writers*each {
		t.Errorf("events = %d, want %d (lost writes)", len(got), writers*each)
	}
}
