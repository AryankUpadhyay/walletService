package domain

import "context"

// WalletRepository defines the contract for wallet persistence.
// Implementations may be backed by in-memory maps, Redis, DynamoDB, PostgreSQL, etc.
// The service layer depends on this interface, never on a concrete implementation.
type WalletRepository interface {
	// Create persists a new wallet. Returns ErrDuplicateWallet if the ID already exists.
	Create(ctx context.Context, wallet *Wallet) error

	// GetByID retrieves a wallet by its ID. Returns ErrWalletNotFound if not found.
	GetByID(ctx context.Context, id string) (*Wallet, error)

	// UpdateBalance atomically sets the wallet's balance to newBalance.
	// Returns ErrWalletNotFound if the wallet does not exist.
	UpdateBalance(ctx context.Context, id string, newBalance int64) error
}

// TransactionRepository defines the contract for transaction (ledger) persistence.
// Transactions are append-only — once created, they are never modified or deleted.
type TransactionRepository interface {
	// Create persists a new transaction entry.
	Create(ctx context.Context, txn *Transaction) error

	// GetByWalletID returns all transactions for a given wallet, ordered by creation time (ascending).
	GetByWalletID(ctx context.Context, walletID string) ([]Transaction, error)
}

// IdempotencyStore defines the contract for idempotency key storage.
// Used to ensure that retried deduct requests are not processed more than once.
type IdempotencyStore interface {
	// Get retrieves an idempotency record by key.
	// Returns the record, true if found, or nil, false if not found.
	Get(ctx context.Context, key string) (*IdempotencyRecord, bool, error)

	// Set stores an idempotency record. Overwrites if key already exists.
	Set(ctx context.Context, key string, record *IdempotencyRecord) error
}
