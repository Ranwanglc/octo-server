package user

import (
	"sync"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/testutil"
	"github.com/stretchr/testify/assert"
)

func TestGetDeviceFlags_Concurrent(t *testing.T) {
	_, ctx := testutil.NewTestServer()
	u := New(ctx)

	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)

	// Insert test data
	_, err = ctx.DB().InsertInto("device_flag").
		Columns("device_flag", "weight", "remark").
		Values(1, 100, "APP").
		Values(2, 50, "PC").
		Values(3, 30, "WEB").
		Exec()
	assert.NoError(t, err)

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)

	results := make([][]*deviceFlagModel, goroutines)
	errors := make([]error, goroutines)

	// Launch multiple goroutines to call getDeviceFlags concurrently
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			results[idx], errors[idx] = u.getDeviceFlags()
		}(i)
	}

	wg.Wait()

	// Verify all goroutines got the same result
	for i := 0; i < goroutines; i++ {
		assert.NoError(t, errors[i], "goroutine %d should not have error", i)
		assert.NotNil(t, results[i], "goroutine %d should have result", i)
		assert.Len(t, results[i], 3, "goroutine %d should have 3 device flags", i)
	}

	// Verify all results point to the same slice (cached result)
	firstResult := results[0]
	for i := 1; i < goroutines; i++ {
		assert.Same(t, &firstResult[0], &results[i][0], "all goroutines should get the same cached slice")
	}
}
