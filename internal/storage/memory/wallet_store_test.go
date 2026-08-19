package memory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/walletservice/internal/domain"
)

func TestWalletStore_Create(t *testing.T) {
	store := NewWalletStore()
	ctx := context.Background()

	wallet := &domain.Wallet{
		ID:           "w1",
		CustomerName: "Alice",
		Balance:      0,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	t.Run("create succeeds", func(t *testing.T) {
		err := store.Create(ctx, wallet)
		require.NoError(t, err)
	})

	t.Run("duplicate ID returns error", func(t *testing.T) {
		err := store.Create(ctx, wallet)
		assert.ErrorIs(t, err, domain.ErrDuplicateWallet)
	})
}

func TestWalletStore_GetByID(t *testing.T) {
	store := NewWalletStore()
	ctx := context.Background()

	t.Run("not found returns error", func(t *testing.T) {
		_, err := store.GetByID(ctx, "nonexistent")
		assert.ErrorIs(t, err, domain.ErrWalletNotFound)
	})

	t.Run("found returns wallet copy", func(t *testing.T) {
		wallet := &domain.Wallet{
			ID:           "w2",
			CustomerName: "Bob",
			Balance:      5000,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}
		require.NoError(t, store.Create(ctx, wallet))

		got, err := store.GetByID(ctx, "w2")
		require.NoError(t, err)
		assert.Equal(t, "w2", got.ID)
		assert.Equal(t, "Bob", got.CustomerName)
		assert.Equal(t, int64(5000), got.Balance)

		// Verify it returns a copy — mutating returned wallet should not affect store.
		got.Balance = 9999
		stored, _ := store.GetByID(ctx, "w2")
		assert.Equal(t, int64(5000), stored.Balance, "store should not be affected by external mutation")
	})
}

func TestWalletStore_UpdateBalance(t *testing.T) {
	store := NewWalletStore()
	ctx := context.Background()

	t.Run("update nonexistent wallet returns error", func(t *testing.T) {
		err := store.UpdateBalance(ctx, "nonexistent", 1000)
		assert.ErrorIs(t, err, domain.ErrWalletNotFound)
	})

	t.Run("update succeeds", func(t *testing.T) {
		wallet := &domain.Wallet{
			ID:           "w3",
			CustomerName: "Carol",
			Balance:      5000,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}
		require.NoError(t, store.Create(ctx, wallet))

		err := store.UpdateBalance(ctx, "w3", 8000)
		require.NoError(t, err)

		got, _ := store.GetByID(ctx, "w3")
		assert.Equal(t, int64(8000), got.Balance)
	})

	t.Run("update sets UpdatedAt", func(t *testing.T) {
		wallet := &domain.Wallet{
			ID:           "w4",
			CustomerName: "Dave",
			Balance:      0,
			CreatedAt:    time.Now().Add(-time.Hour),
			UpdatedAt:    time.Now().Add(-time.Hour),
		}
		require.NoError(t, store.Create(ctx, wallet))

		before := time.Now()
		_ = store.UpdateBalance(ctx, "w4", 100)
		got, _ := store.GetByID(ctx, "w4")
		assert.True(t, got.UpdatedAt.After(before) || got.UpdatedAt.Equal(before))
	})
}

func TestWalletStore_Create_DeepCopy(t *testing.T) {
	store := NewWalletStore()
	ctx := context.Background()

	wallet := &domain.Wallet{
		ID:           "w5",
		CustomerName: "Eve",
		Balance:      1000,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	require.NoError(t, store.Create(ctx, wallet))

	// Mutate the original — should not affect stored version.
	wallet.Balance = 9999
	wallet.CustomerName = "Mutated"

	got, _ := store.GetByID(ctx, "w5")
	assert.Equal(t, int64(1000), got.Balance, "stored wallet should not be affected")
	assert.Equal(t, "Eve", got.CustomerName)
}
