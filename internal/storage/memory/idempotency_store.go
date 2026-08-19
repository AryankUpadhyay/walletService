package memory

import (
	"context"
	"sync"

	"github.com/walletservice/internal/domain"
)

// IdempotencyStore is a thread-safe, in-memory implementation of domain.IdempotencyStore.
// In production, this would be backed by Redis with TTL-based expiration.
type IdempotencyStore struct {
	mu      sync.RWMutex
	records map[string]*domain.IdempotencyRecord
}

// NewIdempotencyStore creates a new empty in-memory idempotency store.
func NewIdempotencyStore() *IdempotencyStore {
	return &IdempotencyStore{
		records: make(map[string]*domain.IdempotencyRecord),
	}
}

// Get retrieves an idempotency record by key.
// Returns (record, true, nil) if found, or (nil, false, nil) if not.
func (s *IdempotencyStore) Get(_ context.Context, key string) (*domain.IdempotencyRecord, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	record, exists := s.records[key]
	if !exists {
		return nil, false, nil
	}

	// Return a copy.
	cp := *record
	return &cp, true, nil
}

// Set stores an idempotency record. Overwrites if the key already exists.
func (s *IdempotencyStore) Set(_ context.Context, key string, record *domain.IdempotencyRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cp := *record
	s.records[key] = &cp
	return nil
}

// Compile-time check that IdempotencyStore implements domain.IdempotencyStore.
var _ domain.IdempotencyStore = (*IdempotencyStore)(nil)
