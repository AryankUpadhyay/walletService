// Package service implements the business logic for the Wallet Service.
// It depends only on interfaces defined in the domain package — never on
// concrete storage implementations. This makes the service layer fully
// testable with mock repositories and trivially swappable to any backend.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/walletservice/internal/domain"
)

// WalletService defines the business operations available on wallets.
// The handler layer depends on this interface, enabling isolated handler testing.
type WalletService interface {
	// CreateWallet creates a new wallet for a customer with zero balance.
	CreateWallet(ctx context.Context, customerName string) (*domain.Wallet, error)

	// TopUp adds funds (in paise) to an existing wallet.
	TopUp(ctx context.Context, walletID string, amountPaise int64) (*domain.Transaction, error)

	// Deduct removes funds (in paise) from an existing wallet.
	// Idempotency is enforced via the idempotencyKey: retries with the same key
	// return the original result without re-deducting.
	Deduct(ctx context.Context, walletID string, amountPaise int64, idempotencyKey string) (*domain.Transaction, bool, error)

	// GetBalance returns the current balance (in paise) for a wallet.
	GetBalance(ctx context.Context, walletID string) (int64, error)

	// GetTransactions returns the full transaction history for a wallet.
	GetTransactions(ctx context.Context, walletID string) ([]domain.Transaction, error)
}

// walletServiceImpl is the concrete implementation of WalletService.
// It orchestrates the repository interfaces to enforce business rules.
type walletServiceImpl struct {
	walletRepo     domain.WalletRepository
	txnRepo        domain.TransactionRepository
	idempotencyStr domain.IdempotencyStore

	// walletMu provides per-wallet locking for atomic balance operations.
	// In a database-backed implementation, this would be replaced by
	// SELECT ... FOR UPDATE or optimistic concurrency control.
	walletMu sync.Map // map[string]*sync.Mutex
}

// NewWalletService creates a new WalletService with the given repository implementations.
func NewWalletService(
	walletRepo domain.WalletRepository,
	txnRepo domain.TransactionRepository,
	idempotencyStore domain.IdempotencyStore,
) WalletService {
	return &walletServiceImpl{
		walletRepo:     walletRepo,
		txnRepo:        txnRepo,
		idempotencyStr: idempotencyStore,
	}
}

// getWalletMutex returns a per-wallet mutex for serializing balance mutations.
func (s *walletServiceImpl) getWalletMutex(walletID string) *sync.Mutex {
	mu, _ := s.walletMu.LoadOrStore(walletID, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// scopedIdempotencyKey returns an idempotency key scoped to a specific wallet.
// This prevents collisions when the same idempotency key is used across different wallets.
func scopedIdempotencyKey(walletID, key string) string {
	return walletID + ":" + key
}

func (s *walletServiceImpl) CreateWallet(ctx context.Context, customerName string) (*domain.Wallet, error) {
	if customerName == "" {
		slog.Warn("create wallet failed: empty customer name")
		return nil, domain.ErrInvalidCustomerName
	}

	now := time.Now()
	wallet := &domain.Wallet{
		ID:           uuid.New().String(),
		CustomerName: customerName,
		Balance:      0,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := s.walletRepo.Create(ctx, wallet); err != nil {
		slog.Error("create wallet: repository error",
			"wallet_id", wallet.ID,
			"customer_name", customerName,
			"error", err,
		)
		return nil, fmt.Errorf("create wallet: %w", err)
	}

	slog.Info("wallet created",
		"wallet_id", wallet.ID,
		"customer_name", customerName,
	)
	return wallet, nil
}

func (s *walletServiceImpl) TopUp(ctx context.Context, walletID string, amountPaise int64) (*domain.Transaction, error) {
	if amountPaise <= 0 {
		slog.Warn("topup failed: invalid amount",
			"wallet_id", walletID,
			"amount_paise", amountPaise,
		)
		return nil, domain.ErrInvalidAmount
	}

	// Acquire per-wallet lock for atomic read-modify-write.
	mu := s.getWalletMutex(walletID)
	mu.Lock()
	defer mu.Unlock()

	wallet, err := s.walletRepo.GetByID(ctx, walletID)
	if err != nil {
		slog.Error("topup failed: wallet lookup error",
			"wallet_id", walletID,
			"error", err,
		)
		return nil, fmt.Errorf("topup get wallet: %w", err)
	}

	newBalance := wallet.Balance + amountPaise
	if err := s.walletRepo.UpdateBalance(ctx, walletID, newBalance); err != nil {
		slog.Error("topup failed: balance update error",
			"wallet_id", walletID,
			"amount_paise", amountPaise,
			"error", err,
		)
		return nil, fmt.Errorf("topup update balance: %w", err)
	}

	txn := &domain.Transaction{
		ID:          uuid.New().String(),
		WalletID:    walletID,
		Amount:      amountPaise,
		Type:        domain.TransactionTypeCredit,
		Description: "Wallet top-up",
		ReferenceID: uuid.New().String(),
		CreatedAt:   time.Now(),
	}

	if err := s.txnRepo.Create(ctx, txn); err != nil {
		// Rollback: revert the balance update since the transaction record failed.
		slog.Error("topup failed: transaction create error, rolling back balance",
			"wallet_id", walletID,
			"amount_paise", amountPaise,
			"error", err,
		)
		if rbErr := s.walletRepo.UpdateBalance(ctx, walletID, wallet.Balance); rbErr != nil {
			slog.Error("topup rollback failed: could not revert balance",
				"wallet_id", walletID,
				"original_balance", wallet.Balance,
				"error", rbErr,
			)
		}
		return nil, fmt.Errorf("topup create transaction: %w", err)
	}

	slog.Info("topup successful",
		"wallet_id", walletID,
		"amount_paise", amountPaise,
		"new_balance_paise", newBalance,
		"transaction_id", txn.ID,
	)
	return txn, nil
}

func (s *walletServiceImpl) Deduct(ctx context.Context, walletID string, amountPaise int64, idempotencyKey string) (*domain.Transaction, bool, error) {
	if idempotencyKey == "" {
		slog.Warn("deduct failed: missing idempotency key",
			"wallet_id", walletID,
		)
		return nil, false, domain.ErrMissingIdempotencyKey
	}

	if amountPaise <= 0 {
		slog.Warn("deduct failed: invalid amount",
			"wallet_id", walletID,
			"amount_paise", amountPaise,
			"idempotency_key", idempotencyKey,
		)
		return nil, false, domain.ErrInvalidAmount
	}

	// Acquire per-wallet lock BEFORE the idempotency check.
	// This prevents a race condition where two concurrent requests with the
	// same idempotency key both pass the Get() check (neither sees the other's
	// record yet), and both proceed to deduct.
	mu := s.getWalletMutex(walletID)
	mu.Lock()
	defer mu.Unlock()

	// Check idempotency store — now inside the lock, so concurrent retries
	// are serialized and only one can pass this check.
	scopedKey := scopedIdempotencyKey(walletID, idempotencyKey)
	_, found, err := s.idempotencyStr.Get(ctx, scopedKey)
	if err != nil {
		slog.Error("deduct failed: idempotency store lookup error",
			"wallet_id", walletID,
			"idempotency_key", idempotencyKey,
			"error", err,
		)
		return nil, false, fmt.Errorf("deduct check idempotency: %w", err)
	}
	if found {
		slog.Info("deduct: idempotent replay detected",
			"wallet_id", walletID,
			"idempotency_key", idempotencyKey,
		)
		return nil, true, nil
	}

	wallet, err := s.walletRepo.GetByID(ctx, walletID)
	if err != nil {
		slog.Error("deduct failed: wallet lookup error",
			"wallet_id", walletID,
			"idempotency_key", idempotencyKey,
			"error", err,
		)
		return nil, false, fmt.Errorf("deduct get wallet: %w", err)
	}

	if wallet.Balance < amountPaise {
		slog.Warn("deduct failed: insufficient balance",
			"wallet_id", walletID,
			"amount_paise", amountPaise,
			"balance_paise", wallet.Balance,
			"idempotency_key", idempotencyKey,
		)
		return nil, false, domain.ErrInsufficientBalance
	}

	newBalance := wallet.Balance - amountPaise
	if err := s.walletRepo.UpdateBalance(ctx, walletID, newBalance); err != nil {
		slog.Error("deduct failed: balance update error",
			"wallet_id", walletID,
			"amount_paise", amountPaise,
			"idempotency_key", idempotencyKey,
			"error", err,
		)
		return nil, false, fmt.Errorf("deduct update balance: %w", err)
	}

	txn := &domain.Transaction{
		ID:          uuid.New().String(),
		WalletID:    walletID,
		Amount:      amountPaise,
		Type:        domain.TransactionTypeDebit,
		Description: "Order deduction",
		ReferenceID: idempotencyKey, // Link transaction to the idempotency key
		CreatedAt:   time.Now(),
	}

	if err := s.txnRepo.Create(ctx, txn); err != nil {
		// Rollback: revert the balance update since the transaction record failed.
		slog.Error("deduct failed: transaction create error, rolling back balance",
			"wallet_id", walletID,
			"amount_paise", amountPaise,
			"idempotency_key", idempotencyKey,
			"error", err,
		)
		if rbErr := s.walletRepo.UpdateBalance(ctx, walletID, wallet.Balance); rbErr != nil {
			slog.Error("deduct rollback failed: could not revert balance",
				"wallet_id", walletID,
				"original_balance", wallet.Balance,
				"error", rbErr,
			)
		}
		return nil, false, fmt.Errorf("deduct create transaction: %w", err)
	}

	// Store idempotency record so future retries with the same key
	// return the cached result without re-deducting.
	idempRecord := &domain.IdempotencyRecord{
		StatusCode:   200,
		ResponseBody: []byte(txn.ID), // Store the transaction ID for lookup
		CreatedAt:    time.Now(),
	}
	if err := s.idempotencyStr.Set(ctx, scopedKey, idempRecord); err != nil {
		// Log but don't fail — the deduction was already applied.
		// Worst case: a retry will be treated as a new request (rare).
		slog.Warn("deduct: failed to store idempotency record",
			"wallet_id", walletID,
			"idempotency_key", idempotencyKey,
			"error", err,
		)
	}

	slog.Info("deduct successful",
		"wallet_id", walletID,
		"amount_paise", amountPaise,
		"new_balance_paise", newBalance,
		"transaction_id", txn.ID,
		"idempotency_key", idempotencyKey,
	)
	return txn, false, nil
}

func (s *walletServiceImpl) GetBalance(ctx context.Context, walletID string) (int64, error) {
	wallet, err := s.walletRepo.GetByID(ctx, walletID)
	if err != nil {
		slog.Error("get balance failed",
			"wallet_id", walletID,
			"error", err,
		)
		return 0, fmt.Errorf("get balance: %w", err)
	}

	slog.Info("balance retrieved",
		"wallet_id", walletID,
		"balance_paise", wallet.Balance,
	)
	return wallet.Balance, nil
}

func (s *walletServiceImpl) GetTransactions(ctx context.Context, walletID string) ([]domain.Transaction, error) {
	// First verify the wallet exists.
	if _, err := s.walletRepo.GetByID(ctx, walletID); err != nil {
		slog.Error("get transactions failed: wallet lookup error",
			"wallet_id", walletID,
			"error", err,
		)
		return nil, fmt.Errorf("get transactions: %w", err)
	}

	txns, err := s.txnRepo.GetByWalletID(ctx, walletID)
	if err != nil {
		slog.Error("get transactions failed: transaction lookup error",
			"wallet_id", walletID,
			"error", err,
		)
		return nil, fmt.Errorf("get transactions: %w", err)
	}

	slog.Info("transactions retrieved",
		"wallet_id", walletID,
		"count", len(txns),
	)
	return txns, nil
}
