package service

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/walletservice/internal/domain"
	"github.com/walletservice/internal/storage/memory"
)

// newTestService creates a WalletService with fresh in-memory stores for testing.
// Returns the service and its underlying stores for test assertions.
func newTestService() (WalletService, *memory.WalletStore, *memory.TransactionStore, *memory.IdempotencyStore) {
	ws := memory.NewWalletStore()
	ts := memory.NewTransactionStore()
	is := memory.NewIdempotencyStore()
	svc := NewWalletService(ws, ts, is)
	return svc, ws, ts, is
}

func TestWalletService_CreateWallet(t *testing.T) {
	svc, _, _, _ := newTestService()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		wallet, err := svc.CreateWallet(ctx, "Alice")
		require.NoError(t, err)
		assert.NotEmpty(t, wallet.ID)
		assert.Equal(t, "Alice", wallet.CustomerName)
		assert.Equal(t, int64(0), wallet.Balance)
		assert.False(t, wallet.CreatedAt.IsZero())
		assert.False(t, wallet.UpdatedAt.IsZero())
	})

	t.Run("empty name returns error", func(t *testing.T) {
		_, err := svc.CreateWallet(ctx, "")
		assert.ErrorIs(t, err, domain.ErrInvalidCustomerName)
	})

	t.Run("different wallets get different IDs", func(t *testing.T) {
		w1, _ := svc.CreateWallet(ctx, "Bob")
		w2, _ := svc.CreateWallet(ctx, "Carol")
		assert.NotEqual(t, w1.ID, w2.ID)
	})
}

func TestWalletService_TopUp(t *testing.T) {
	svc, _, _, _ := newTestService()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		wallet, _ := svc.CreateWallet(ctx, "Alice")

		txn, err := svc.TopUp(ctx, wallet.ID, 50000) // ₹500
		require.NoError(t, err)
		assert.NotEmpty(t, txn.ID)
		assert.Equal(t, wallet.ID, txn.WalletID)
		assert.Equal(t, int64(50000), txn.Amount)
		assert.Equal(t, domain.TransactionTypeCredit, txn.Type)

		balance, _ := svc.GetBalance(ctx, wallet.ID)
		assert.Equal(t, int64(50000), balance)
	})

	t.Run("multiple topups accumulate", func(t *testing.T) {
		wallet, _ := svc.CreateWallet(ctx, "Bob")

		svc.TopUp(ctx, wallet.ID, 10000)
		svc.TopUp(ctx, wallet.ID, 20000)
		svc.TopUp(ctx, wallet.ID, 30000)

		balance, _ := svc.GetBalance(ctx, wallet.ID)
		assert.Equal(t, int64(60000), balance)
	})

	t.Run("zero amount returns error", func(t *testing.T) {
		wallet, _ := svc.CreateWallet(ctx, "Carol")
		_, err := svc.TopUp(ctx, wallet.ID, 0)
		assert.ErrorIs(t, err, domain.ErrInvalidAmount)
	})

	t.Run("negative amount returns error", func(t *testing.T) {
		wallet, _ := svc.CreateWallet(ctx, "Dave")
		_, err := svc.TopUp(ctx, wallet.ID, -5000)
		assert.ErrorIs(t, err, domain.ErrInvalidAmount)
	})

	t.Run("wallet not found returns error", func(t *testing.T) {
		_, err := svc.TopUp(ctx, "nonexistent", 10000)
		assert.ErrorIs(t, err, domain.ErrWalletNotFound)
	})
}

func TestWalletService_Deduct(t *testing.T) {
	svc, _, _, _ := newTestService()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		wallet, _ := svc.CreateWallet(ctx, "Alice")
		svc.TopUp(ctx, wallet.ID, 20000) // ₹200

		txn, isReplay, err := svc.Deduct(ctx, wallet.ID, 10000, "idem-1") // ₹100
		require.NoError(t, err)
		assert.False(t, isReplay)
		assert.NotEmpty(t, txn.ID)
		assert.Equal(t, domain.TransactionTypeDebit, txn.Type)
		assert.Equal(t, int64(10000), txn.Amount)

		balance, _ := svc.GetBalance(ctx, wallet.ID)
		assert.Equal(t, int64(10000), balance)
	})

	t.Run("insufficient balance", func(t *testing.T) {
		wallet, _ := svc.CreateWallet(ctx, "Bob")
		svc.TopUp(ctx, wallet.ID, 5000) // ₹50

		_, _, err := svc.Deduct(ctx, wallet.ID, 10000, "idem-2") // ₹100
		assert.ErrorIs(t, err, domain.ErrInsufficientBalance)

		// Balance should be unchanged.
		balance, _ := svc.GetBalance(ctx, wallet.ID)
		assert.Equal(t, int64(5000), balance)
	})

	t.Run("exact balance", func(t *testing.T) {
		wallet, _ := svc.CreateWallet(ctx, "ExactBal")
		svc.TopUp(ctx, wallet.ID, 10000) // ₹100

		_, _, err := svc.Deduct(ctx, wallet.ID, 10000, "idem-exact") // ₹100
		require.NoError(t, err)

		balance, _ := svc.GetBalance(ctx, wallet.ID)
		assert.Equal(t, int64(0), balance)
	})

	t.Run("zero balance after deduct, second deduct fails", func(t *testing.T) {
		wallet, _ := svc.CreateWallet(ctx, "ZeroBal")
		svc.TopUp(ctx, wallet.ID, 10000)
		svc.Deduct(ctx, wallet.ID, 10000, "idem-zb1")

		_, _, err := svc.Deduct(ctx, wallet.ID, 10000, "idem-zb2")
		assert.ErrorIs(t, err, domain.ErrInsufficientBalance)
	})

	t.Run("wallet not found", func(t *testing.T) {
		_, _, err := svc.Deduct(ctx, "nonexistent", 10000, "idem-3")
		assert.ErrorIs(t, err, domain.ErrWalletNotFound)
	})

	t.Run("missing idempotency key", func(t *testing.T) {
		wallet, _ := svc.CreateWallet(ctx, "Carol")
		_, _, err := svc.Deduct(ctx, wallet.ID, 10000, "")
		assert.ErrorIs(t, err, domain.ErrMissingIdempotencyKey)
	})

	t.Run("invalid amount", func(t *testing.T) {
		wallet, _ := svc.CreateWallet(ctx, "Dave")
		_, _, err := svc.Deduct(ctx, wallet.ID, 0, "idem-4")
		assert.ErrorIs(t, err, domain.ErrInvalidAmount)

		_, _, err = svc.Deduct(ctx, wallet.ID, -100, "idem-5")
		assert.ErrorIs(t, err, domain.ErrInvalidAmount)
	})
}

func TestWalletService_Deduct_Idempotency(t *testing.T) {
	svc, _, _, _ := newTestService()
	ctx := context.Background()

	t.Run("same key deducts only once", func(t *testing.T) {
		wallet, _ := svc.CreateWallet(ctx, "Alice")
		svc.TopUp(ctx, wallet.ID, 30000) // ₹300

		// First deduct with key "order-123"
		txn1, isReplay1, err := svc.Deduct(ctx, wallet.ID, 10000, "order-123")
		require.NoError(t, err)
		assert.False(t, isReplay1)
		assert.NotNil(t, txn1)

		balance1, _ := svc.GetBalance(ctx, wallet.ID)
		assert.Equal(t, int64(20000), balance1)

		// Second deduct with same key "order-123" — should NOT deduct again
		txn2, isReplay2, err := svc.Deduct(ctx, wallet.ID, 10000, "order-123")
		require.NoError(t, err)
		assert.True(t, isReplay2)
		assert.Nil(t, txn2)

		// Balance should still be 20000
		balance2, _ := svc.GetBalance(ctx, wallet.ID)
		assert.Equal(t, int64(20000), balance2)
	})

	t.Run("different keys deduct separately", func(t *testing.T) {
		wallet, _ := svc.CreateWallet(ctx, "Bob")
		svc.TopUp(ctx, wallet.ID, 30000)

		svc.Deduct(ctx, wallet.ID, 10000, "order-a")
		svc.Deduct(ctx, wallet.ID, 10000, "order-b")

		balance, _ := svc.GetBalance(ctx, wallet.ID)
		assert.Equal(t, int64(10000), balance)
	})
}

func TestWalletService_GetBalance(t *testing.T) {
	svc, _, _, _ := newTestService()
	ctx := context.Background()

	t.Run("new wallet has zero balance", func(t *testing.T) {
		wallet, _ := svc.CreateWallet(ctx, "Alice")
		balance, err := svc.GetBalance(ctx, wallet.ID)
		require.NoError(t, err)
		assert.Equal(t, int64(0), balance)
	})

	t.Run("wallet not found", func(t *testing.T) {
		_, err := svc.GetBalance(ctx, "nonexistent")
		assert.ErrorIs(t, err, domain.ErrWalletNotFound)
	})
}

func TestWalletService_GetTransactions(t *testing.T) {
	svc, _, _, _ := newTestService()
	ctx := context.Background()

	t.Run("new wallet has empty transaction history", func(t *testing.T) {
		wallet, _ := svc.CreateWallet(ctx, "Alice")
		txns, err := svc.GetTransactions(ctx, wallet.ID)
		require.NoError(t, err)
		assert.Len(t, txns, 0)
	})

	t.Run("topup and deduct appear in history", func(t *testing.T) {
		wallet, _ := svc.CreateWallet(ctx, "Bob")
		svc.TopUp(ctx, wallet.ID, 30000)
		svc.Deduct(ctx, wallet.ID, 10000, "order-tx1")

		txns, err := svc.GetTransactions(ctx, wallet.ID)
		require.NoError(t, err)
		assert.Len(t, txns, 2)
		assert.Equal(t, domain.TransactionTypeCredit, txns[0].Type)
		assert.Equal(t, domain.TransactionTypeDebit, txns[1].Type)
	})

	t.Run("wallet not found", func(t *testing.T) {
		_, err := svc.GetTransactions(ctx, "nonexistent")
		assert.ErrorIs(t, err, domain.ErrWalletNotFound)
	})
}

func TestWalletService_ConcurrentDeductions(t *testing.T) {
	svc, _, _, _ := newTestService()
	ctx := context.Background()

	// Create wallet with ₹500 (50000 paise)
	wallet, _ := svc.CreateWallet(ctx, "ConcurrentUser")
	svc.TopUp(ctx, wallet.ID, 50000)

	// Launch 10 goroutines, each trying to deduct ₹100 (10000 paise)
	// Only 5 should succeed (balance = ₹500, deduction = ₹100 each)
	var wg sync.WaitGroup
	successCount := 0
	failCount := 0
	var mu sync.Mutex

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := fmt.Sprintf("concurrent-%d", idx)
			_, _, err := svc.Deduct(ctx, wallet.ID, 10000, key)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successCount++
			} else {
				failCount++
			}
		}(i)
	}

	wg.Wait()

	assert.Equal(t, 5, successCount, "exactly 5 deductions should succeed")
	assert.Equal(t, 5, failCount, "exactly 5 deductions should fail")

	// Balance must never be negative.
	balance, _ := svc.GetBalance(ctx, wallet.ID)
	assert.Equal(t, int64(0), balance)
	assert.True(t, balance >= 0, "balance must never be negative")
}

func TestWalletService_ConcurrentSameIdempotencyKey(t *testing.T) {
	// This test verifies the fix for the idempotency race condition.
	// 10 goroutines send the SAME idempotency key concurrently.
	// Only 1 deduction should occur — the rest should be detected as replays.
	svc, _, _, _ := newTestService()
	ctx := context.Background()

	wallet, _ := svc.CreateWallet(ctx, "IdempRaceUser")
	svc.TopUp(ctx, wallet.ID, 50000) // ₹500

	var wg sync.WaitGroup
	deductCount := 0
	replayCount := 0
	var mu sync.Mutex

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// ALL goroutines use the SAME idempotency key
			txn, isReplay, err := svc.Deduct(ctx, wallet.ID, 10000, "same-key")
			mu.Lock()
			defer mu.Unlock()
			if err == nil && !isReplay && txn != nil {
				deductCount++
			} else if err == nil && isReplay {
				replayCount++
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, 1, deductCount, "exactly 1 deduction should occur")
	assert.Equal(t, 9, replayCount, "exactly 9 should be replays")

	// Balance should reflect exactly one deduction: 50000 - 10000 = 40000
	balance, _ := svc.GetBalance(ctx, wallet.ID)
	assert.Equal(t, int64(40000), balance, "balance should reflect exactly one deduction")
}

