package memory

import (
	"context"
	"sync"

	"github.com/walletservice/internal/domain"
)

// TransactionStore is a thread-safe, in-memory implementation of domain.TransactionRepository.
// Transactions are stored in append-only slices keyed by wallet ID.
type TransactionStore struct {
	mu           sync.RWMutex
	transactions map[string][]domain.Transaction // walletID → ordered list
}

// NewTransactionStore creates a new empty in-memory transaction store.
func NewTransactionStore() *TransactionStore {
	return &TransactionStore{
		transactions: make(map[string][]domain.Transaction),
	}
}

// Create appends a new transaction to the ledger for the given wallet.
func (s *TransactionStore) Create(_ context.Context, txn *domain.Transaction) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.transactions[txn.WalletID] = append(s.transactions[txn.WalletID], *txn)
	return nil
}

// GetByWalletID returns all transactions for a wallet, ordered by creation time (ascending).
// Returns an empty slice (not nil) if no transactions exist.
func (s *TransactionStore) GetByWalletID(_ context.Context, walletID string) ([]domain.Transaction, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	txns, exists := s.transactions[walletID]
	if !exists {
		return []domain.Transaction{}, nil
	}

	// Return a copy to prevent external mutation.
	result := make([]domain.Transaction, len(txns))
	copy(result, txns)
	return result, nil
}

// Compile-time check that TransactionStore implements domain.TransactionRepository.
var _ domain.TransactionRepository = (*TransactionStore)(nil)
