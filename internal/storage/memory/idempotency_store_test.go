package memory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/walletservice/internal/domain"
)

func TestIdempotencyStore_GetSet(t *testing.T) {
	store := NewIdempotencyStore()
	ctx := context.Background()

	t.Run("get nonexistent returns false", func(t *testing.T) {
		record, found, err := store.Get(ctx, "nonexistent")
		require.NoError(t, err)
		assert.False(t, found)
		assert.Nil(t, record)
	})

	t.Run("set then get returns record", func(t *testing.T) {
		record := &domain.IdempotencyRecord{
			StatusCode:   200,
			ResponseBody: []byte(`{"transaction_id":"t1"}`),
			CreatedAt:    time.Now(),
		}
		err := store.Set(ctx, "key1", record)
		require.NoError(t, err)

		got, found, err := store.Get(ctx, "key1")
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, 200, got.StatusCode)
		assert.Equal(t, []byte(`{"transaction_id":"t1"}`), got.ResponseBody)
	})

	t.Run("set overwrites existing", func(t *testing.T) {
		record1 := &domain.IdempotencyRecord{StatusCode: 200, CreatedAt: time.Now()}
		record2 := &domain.IdempotencyRecord{StatusCode: 422, CreatedAt: time.Now()}

		require.NoError(t, store.Set(ctx, "key2", record1))
		require.NoError(t, store.Set(ctx, "key2", record2))

		got, found, _ := store.Get(ctx, "key2")
		assert.True(t, found)
		assert.Equal(t, 422, got.StatusCode)
	})

	t.Run("returns copy not reference", func(t *testing.T) {
		record := &domain.IdempotencyRecord{StatusCode: 200, CreatedAt: time.Now()}
		require.NoError(t, store.Set(ctx, "key3", record))

		got, _, _ := store.Get(ctx, "key3")
		got.StatusCode = 500

		got2, _, _ := store.Get(ctx, "key3")
		assert.Equal(t, 200, got2.StatusCode, "store should not be affected by external mutation")
	})

	t.Run("different keys are isolated", func(t *testing.T) {
		r1 := &domain.IdempotencyRecord{StatusCode: 200, CreatedAt: time.Now()}
		r2 := &domain.IdempotencyRecord{StatusCode: 201, CreatedAt: time.Now()}
		require.NoError(t, store.Set(ctx, "isolated-a", r1))
		require.NoError(t, store.Set(ctx, "isolated-b", r2))

		gotA, _, _ := store.Get(ctx, "isolated-a")
		gotB, _, _ := store.Get(ctx, "isolated-b")
		assert.Equal(t, 200, gotA.StatusCode)
		assert.Equal(t, 201, gotB.StatusCode)
	})
}
