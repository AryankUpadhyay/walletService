package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/walletservice/internal/domain"
)

// --- Mock Repositories for error injection ---

// errorWalletRepo is a mock that returns errors for specific operations.
type errorWalletRepo struct {
	createErr        error
	getByIDErr       error
	updateBalanceErr error
	wallet           *domain.Wallet
}

func (r *errorWalletRepo) Create(_ context.Context, _ *domain.Wallet) error {
	return r.createErr
}

func (r *errorWalletRepo) GetByID(_ context.Context, _ string) (*domain.Wallet, error) {
	if r.getByIDErr != nil {
		return nil, r.getByIDErr
	}
	if r.wallet != nil {
		cp := *r.wallet
		return &cp, nil
	}
	return &domain.Wallet{ID: "mock-id", Balance: 50000}, nil
}

func (r *errorWalletRepo) UpdateBalance(_ context.Context, _ string, _ int64) error {
	return r.updateBalanceErr
}

// errorTxnRepo is a mock that returns errors for Create.
type errorTxnRepo struct {
	createErr error
}

func (r *errorTxnRepo) Create(_ context.Context, _ *domain.Transaction) error {
	return r.createErr
}

func (r *errorTxnRepo) GetByWalletID(_ context.Context, _ string) ([]domain.Transaction, error) {
	return []domain.Transaction{}, nil
}

// errorIdempotencyStore is a mock that returns errors.
type errorIdempotencyStore struct {
	getErr error
	setErr error
}

func (s *errorIdempotencyStore) Get(_ context.Context, _ string) (*domain.IdempotencyRecord, bool, error) {
	return nil, false, s.getErr
}

func (s *errorIdempotencyStore) Set(_ context.Context, _ string, _ *domain.IdempotencyRecord) error {
	return s.setErr
}

// noopIdempotencyStore never has existing records.
type noopIdempotencyStore struct{}

func (s *noopIdempotencyStore) Get(_ context.Context, _ string) (*domain.IdempotencyRecord, bool, error) {
	return nil, false, nil
}

func (s *noopIdempotencyStore) Set(_ context.Context, _ string, _ *domain.IdempotencyRecord) error {
	return nil
}

// --- Tests for error propagation in service methods ---

func TestWalletService_CreateWallet_RepoError(t *testing.T) {
	repo := &errorWalletRepo{createErr: errors.New("db connection failed")}
	svc := NewWalletService(repo, &errorTxnRepo{}, &noopIdempotencyStore{})

	_, err := svc.CreateWallet(context.Background(), "Alice")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create wallet")
}

func TestWalletService_TopUp_UpdateBalanceError(t *testing.T) {
	repo := &errorWalletRepo{updateBalanceErr: errors.New("write failed")}
	svc := NewWalletService(repo, &errorTxnRepo{}, &noopIdempotencyStore{})

	_, err := svc.TopUp(context.Background(), "w1", 10000)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "topup update balance")
}

func TestWalletService_TopUp_TxnCreateError(t *testing.T) {
	repo := &errorWalletRepo{} // GetByID and UpdateBalance succeed
	txnRepo := &errorTxnRepo{createErr: errors.New("txn write failed")}
	svc := NewWalletService(repo, txnRepo, &noopIdempotencyStore{})

	_, err := svc.TopUp(context.Background(), "w1", 10000)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "topup create transaction")
}

func TestWalletService_TopUp_GetWalletError(t *testing.T) {
	repo := &errorWalletRepo{getByIDErr: domain.ErrWalletNotFound}
	svc := NewWalletService(repo, &errorTxnRepo{}, &noopIdempotencyStore{})

	_, err := svc.TopUp(context.Background(), "nonexistent", 10000)
	assert.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrWalletNotFound)
}

func TestWalletService_Deduct_IdempotencyGetError(t *testing.T) {
	repo := &errorWalletRepo{}
	idempStore := &errorIdempotencyStore{getErr: errors.New("redis down")}
	svc := NewWalletService(repo, &errorTxnRepo{}, idempStore)

	_, _, err := svc.Deduct(context.Background(), "w1", 10000, "key1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "deduct check idempotency")
}

func TestWalletService_Deduct_UpdateBalanceError(t *testing.T) {
	repo := &errorWalletRepo{updateBalanceErr: errors.New("write failed")}
	svc := NewWalletService(repo, &errorTxnRepo{}, &noopIdempotencyStore{})

	_, _, err := svc.Deduct(context.Background(), "w1", 10000, "key1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "deduct update balance")
}

func TestWalletService_Deduct_TxnCreateError(t *testing.T) {
	repo := &errorWalletRepo{}
	txnRepo := &errorTxnRepo{createErr: errors.New("txn write failed")}
	svc := NewWalletService(repo, txnRepo, &noopIdempotencyStore{})

	_, _, err := svc.Deduct(context.Background(), "w1", 10000, "key1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "deduct create transaction")
}

func TestWalletService_Deduct_GetWalletError(t *testing.T) {
	repo := &errorWalletRepo{getByIDErr: domain.ErrWalletNotFound}
	svc := NewWalletService(repo, &errorTxnRepo{}, &noopIdempotencyStore{})

	_, _, err := svc.Deduct(context.Background(), "nonexistent", 10000, "key1")
	assert.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrWalletNotFound)
}

func TestWalletService_Deduct_IdempotencySetError_StillSucceeds(t *testing.T) {
	// When idempotency SET fails, deduction should still succeed
	// (we log the error but don't fail the transaction).
	repo := &errorWalletRepo{}
	txnRepo := &errorTxnRepo{} // Create succeeds
	idempStore := &errorIdempotencyStore{setErr: errors.New("redis down on write")}
	svc := NewWalletService(repo, txnRepo, idempStore)

	txn, isReplay, err := svc.Deduct(context.Background(), "w1", 10000, "key1")
	assert.NoError(t, err)
	assert.False(t, isReplay)
	assert.NotNil(t, txn)
}

func TestWalletService_GetBalance_Error(t *testing.T) {
	repo := &errorWalletRepo{getByIDErr: errors.New("db error")}
	svc := NewWalletService(repo, &errorTxnRepo{}, &noopIdempotencyStore{})

	_, err := svc.GetBalance(context.Background(), "w1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get balance")
}

func TestWalletService_GetTransactions_WalletNotFound(t *testing.T) {
	repo := &errorWalletRepo{getByIDErr: domain.ErrWalletNotFound}
	svc := NewWalletService(repo, &errorTxnRepo{}, &noopIdempotencyStore{})

	_, err := svc.GetTransactions(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrWalletNotFound)
}

func TestWalletService_GetTransactions_TxnRepoError(t *testing.T) {
	repo := &errorWalletRepo{} // GetByID succeeds
	txnRepo := &errorTxnRepoWithGetError{getErr: errors.New("txn read failed")}
	svc := NewWalletService(repo, txnRepo, &noopIdempotencyStore{})

	_, err := svc.GetTransactions(context.Background(), "w1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get transactions")
}

// errorTxnRepoWithGetError fails on GetByWalletID
type errorTxnRepoWithGetError struct {
	getErr error
}

func (r *errorTxnRepoWithGetError) Create(_ context.Context, _ *domain.Transaction) error {
	return nil
}

func (r *errorTxnRepoWithGetError) GetByWalletID(_ context.Context, _ string) ([]domain.Transaction, error) {
	return nil, r.getErr
}
