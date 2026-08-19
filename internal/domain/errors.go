package domain

import "errors"

// Sentinel errors for the wallet domain.
// These are business-level errors — the HTTP handler layer maps them to
// appropriate HTTP status codes and JSON error responses.
var (
	// ErrWalletNotFound is returned when a wallet lookup by ID fails.
	ErrWalletNotFound = errors.New("wallet not found")

	// ErrInsufficientBalance is returned when a deduction would cause
	// the wallet balance to go negative.
	ErrInsufficientBalance = errors.New("insufficient balance")

	// ErrDuplicateWallet is returned when attempting to create a wallet
	// with an ID that already exists.
	ErrDuplicateWallet = errors.New("wallet already exists")

	// ErrInvalidAmount is returned when the amount provided is not
	// a positive number.
	ErrInvalidAmount = errors.New("amount must be greater than zero")

	// ErrMissingIdempotencyKey is returned when a deduct request does
	// not include an idempotency key.
	ErrMissingIdempotencyKey = errors.New("idempotency key is required for deduct operations")

	// ErrInvalidCustomerName is returned when the customer name is empty.
	ErrInvalidCustomerName = errors.New("customer name must not be empty")
)
