package keylock

import (
	"sync"
	"testing"
)

func TestKeyLock_LockUnlock(t *testing.T) {
	kl := NewKeyLock()

	// Basic lock/unlock should not deadlock
	kl.Lock("test-key")
	kl.Unlock("test-key")
}

func TestKeyLock_ConcurrentLockUnlock(t *testing.T) {
	kl := NewKeyLock()

	var wg sync.WaitGroup
	const numGoroutines = 100
	const numIterations = 50

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numIterations; j++ {
				kl.Lock("shared-key")
				// Simulate some work
				kl.Unlock("shared-key")
			}
		}(i)
	}

	wg.Wait()
}

func TestKeyLock_CleanRaceCondition(t *testing.T) {
	// This test verifies that Clean() uses atomic.LoadInt64() to read count,
	// preventing race conditions with concurrent Lock/Unlock operations.
	// Run with -race flag to detect data races.
	kl := NewKeyLock()

	var wg sync.WaitGroup
	done := make(chan struct{})

	// Start multiple goroutines doing Lock/Unlock
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					kl.Lock("race-test-key")
					kl.Unlock("race-test-key")
				}
			}
		}()
	}

	// Concurrently call Clean() multiple times
	for i := 0; i < 100; i++ {
		kl.Clean()
	}

	close(done)
	wg.Wait()
}

func TestKeyLock_MultipleKeys(t *testing.T) {
	kl := NewKeyLock()

	var wg sync.WaitGroup
	keys := []string{"key1", "key2", "key3", "key4", "key5"}

	for _, key := range keys {
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				kl.Lock(k)
				kl.Unlock(k)
			}
		}(key)
	}

	wg.Wait()
}

func TestKeyLock_CleanRemovesUnusedLocks(t *testing.T) {
	kl := NewKeyLock()

	// Create and release some locks
	kl.Lock("temp-key")
	kl.Unlock("temp-key")

	// Clean should remove unused locks
	kl.Clean()

	// Verify by trying to lock again (should create new inner lock)
	kl.Lock("temp-key")
	kl.Unlock("temp-key")
}

func TestKeyLock_CleanDoesNotRemoveActiveLocks(t *testing.T) {
	kl := NewKeyLock()

	// Lock without unlocking
	kl.Lock("active-key")

	// Clean should not remove active lock
	kl.Clean()

	// Unlock should work without panic
	kl.Unlock("active-key")
}

func TestKeyLock_StartStopCleanLoop(t *testing.T) {
	kl := NewKeyLock()
	kl.cleanInterval = 1 // Very short interval for testing

	kl.StartCleanLoop()
	kl.StopCleanLoop()
}
