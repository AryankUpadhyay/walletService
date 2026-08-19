// Package domain defines the core business entities, value objects,
// and repository interfaces for the Wallet Service.
// This package has zero external dependencies — it is pure Go.
package domain

import (
	"math"
	"time"
)

// TransactionType represents the direction of a money movement.
type TransactionType string

const (
	// TransactionTypeCredit represents money added to a wallet.
	TransactionTypeCredit TransactionType = "CREDIT"
	// TransactionTypeDebit represents money deducted from a wallet.
	TransactionTypeDebit TransactionType = "DEBIT"
)

// Wallet represents a customer's wallet with a balance.
// Balance is stored in paise (1/100th of ₹) to avoid floating-point errors.
// Example: ₹100 = 10000 paise.
type Wallet struct {
	ID           string    `json:"id"`
	CustomerName string    `json:"customer_name"`
	Balance      int64     `json:"balance"` // in paise
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Transaction represents a single ledger entry — an append-only record
// of every money movement against a wallet.
type Transaction struct {
	ID          string          `json:"id"`
	WalletID    string          `json:"wallet_id"`
	Amount      int64           `json:"amount"` // in paise, always positive
	Type        TransactionType `json:"type"`
	Description string          `json:"description"`
	ReferenceID string          `json:"reference_id"` // links to order ID, topup ID, etc.
	CreatedAt   time.Time       `json:"created_at"`
}

// IdempotencyRecord stores the result of a previously processed request
// so that retries with the same idempotency key return the same response.
type IdempotencyRecord struct {
	StatusCode   int       `json:"status_code"`
	ResponseBody []byte    `json:"response_body"`
	CreatedAt    time.Time `json:"created_at"`
}

// RupeesToPaise converts a rupee amount (float) to paise (int64).
// Uses math.Round to avoid floating-point truncation errors.
// Example: 19.99 * 100 = 1998.9999... → Round → 1999.
func RupeesToPaise(rupees float64) int64 {
	return int64(math.Round(rupees * 100))
}

// PaiseToRupees converts paise (int64) to rupees (float64).
// Used when returning amounts to API consumers.
func PaiseToRupees(paise int64) float64 {
	return float64(paise) / 100.0
}
