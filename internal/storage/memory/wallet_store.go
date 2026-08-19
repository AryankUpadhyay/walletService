// Package memory provides in-memory implementations of the domain repository interfaces.
// These are suitable for development, testing, and single-instance deployments.
// For production multi-instance deployments, swap with Redis/DynamoDB implementations.
package memory

import (
	"context"
	"sync"
	"time"

	"github.com/walletservice/internal/domain"
)

// WalletStore is a thread-safe, in-memory implementation of domain.WalletRepository.
type WalletStore struct {
	mu      sync.RWMutex
	wallets map[string]*domain.Wallet
}

// NewWalletStore creates a new empty in-memory wallet store.
func NewWalletStore() *WalletStore {
	return &WalletStore{
		wallets: make(map[string]*domain.Wallet),
	}
}

// Create persists a new wallet. Returns ErrDuplicateWallet if the ID already exists.
func (s *WalletStore) Create(_ context.Context, wallet *domain.Wallet) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.wallets[wallet.ID]; exists {
		return domain.ErrDuplicateWallet
	}

	// Deep copy to prevent external mutation of internal state.
	stored := *wallet
	s.wallets[wallet.ID] = &stored
	return nil
}

// GetByID retrieves a wallet by its ID. Returns ErrWalletNotFound if not found.
func (s *WalletStore) GetByID(_ context.Context, id string) (*domain.Wallet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	w, exists := s.wallets[id]
	if !exists {
		return nil, domain.ErrWalletNotFound
	}

	// Return a copy so the caller cannot mutate internal state.
	copy := *w
	return &copy, nil
}

// UpdateBalance atomically sets the wallet's balance.
// Returns ErrWalletNotFound if the wallet does not exist.
func (s *WalletStore) UpdateBalance(_ context.Context, id string, newBalance int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	w, exists := s.wallets[id]
	if !exists {
		return domain.ErrWalletNotFound
	}

	w.Balance = newBalance
	w.UpdatedAt = time.Now()
	return nil
}

// Compile-time check that WalletStore implements domain.WalletRepository.
var _ domain.WalletRepository = (*WalletStore)(nil)
