package memory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/walletservice/internal/domain"
)

func TestTransactionStore_Create(t *testing.T) {
	store := NewTransactionStore()
	ctx := context.Background()

	txn := &domain.Transaction{
		ID:          "t1",
		WalletID:    "w1",
		Amount:      10000,
		Type:        domain.TransactionTypeCredit,
		Description: "Top-up",
		ReferenceID: "ref1",
		CreatedAt:   time.Now(),
	}

	err := store.Create(ctx, txn)
	require.NoError(t, err)

	// Verify it was stored.
	txns, err := store.GetByWalletID(ctx, "w1")
	require.NoError(t, err)
	assert.Len(t, txns, 1)
	assert.Equal(t, "t1", txns[0].ID)
}

func TestTransactionStore_GetByWalletID(t *testing.T) {
	store := NewTransactionStore()
	ctx := context.Background()

	t.Run("empty wallet returns empty slice not nil", func(t *testing.T) {
		txns, err := store.GetByWalletID(ctx, "nonexistent")
		require.NoError(t, err)
		assert.NotNil(t, txns)
		assert.Len(t, txns, 0)
	})

	t.Run("multiple transactions in order", func(t *testing.T) {
		for i, id := range []string{"t10", "t11", "t12"} {
			txn := &domain.Transaction{
				ID:          id,
				WalletID:    "w10",
				Amount:      int64((i + 1) * 1000),
				Type:        domain.TransactionTypeCredit,
				Description: "Top-up",
				CreatedAt:   time.Now().Add(time.Duration(i) * time.Second),
			}
			require.NoError(t, store.Create(ctx, txn))
		}

		txns, err := store.GetByWalletID(ctx, "w10")
		require.NoError(t, err)
		assert.Len(t, txns, 3)
		assert.Equal(t, "t10", txns[0].ID)
		assert.Equal(t, "t11", txns[1].ID)
		assert.Equal(t, "t12", txns[2].ID)
	})

	t.Run("different wallets are isolated", func(t *testing.T) {
		txnA := &domain.Transaction{ID: "tA", WalletID: "wA", Amount: 100, Type: domain.TransactionTypeCredit, CreatedAt: time.Now()}
		txnB := &domain.Transaction{ID: "tB", WalletID: "wB", Amount: 200, Type: domain.TransactionTypeDebit, CreatedAt: time.Now()}
		require.NoError(t, store.Create(ctx, txnA))
		require.NoError(t, store.Create(ctx, txnB))

		txnsA, _ := store.GetByWalletID(ctx, "wA")
		txnsB, _ := store.GetByWalletID(ctx, "wB")
		assert.Len(t, txnsA, 1)
		assert.Len(t, txnsB, 1)
		assert.Equal(t, "tA", txnsA[0].ID)
		assert.Equal(t, "tB", txnsB[0].ID)
	})

	t.Run("returns copy not reference", func(t *testing.T) {
		store2 := NewTransactionStore()
		txn := &domain.Transaction{ID: "tc1", WalletID: "wc1", Amount: 500, Type: domain.TransactionTypeCredit, CreatedAt: time.Now()}
		require.NoError(t, store2.Create(ctx, txn))

		txns, _ := store2.GetByWalletID(ctx, "wc1")
		txns[0].Amount = 99999

		txns2, _ := store2.GetByWalletID(ctx, "wc1")
		assert.Equal(t, int64(500), txns2[0].Amount, "store should not be affected by external mutation")
	})
}
