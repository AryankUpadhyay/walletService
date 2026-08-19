// Package handler implements the HTTP transport layer for the Wallet Service.
// It translates HTTP requests into service calls and maps domain errors
// to appropriate HTTP status codes and JSON responses.
package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/walletservice/internal/domain"
	"github.com/walletservice/internal/service"
)

// validateRupeeAmount checks that amount is positive, at least ₹0.01 (1 paisa),
// and does not exceed 2 decimal places (no sub-paisa amounts).
func validateRupeeAmount(amount float64) (int64, string, bool) {
	if amount <= 0 {
		return 0, "Amount must be greater than zero", false
	}
	// Check for sub-paisa decimal precision (more than 2 decimal places)
	// Using a small epsilon to tolerate IEEE 754 floating point inaccuracies.
	if math.Abs(amount*100-math.Round(amount*100)) > 1e-4 {
		return 0, "Amount cannot have more than 2 decimal places", false
	}
	paise := domain.RupeesToPaise(amount)
	if paise <= 0 {
		return 0, "Amount must be at least ₹0.01 (1 paisa)", false
	}
	return paise, "", true
}

// --- Request DTOs ---

// CreateWalletRequest is the request body for POST /wallets.
type CreateWalletRequest struct {
	CustomerName string `json:"customer_name"`
}

// TopUpRequest is the request body for POST /wallets/:id/topup.
type TopUpRequest struct {
	Amount float64 `json:"amount"` // in rupees
}

// DeductRequest is the request body for POST /wallets/:id/deduct.
type DeductRequest struct {
	Amount float64 `json:"amount"` // in rupees
}

// --- Response DTOs ---

// WalletResponse is the response body for wallet operations.
type WalletResponse struct {
	ID           string  `json:"id"`
	CustomerName string  `json:"customer_name"`
	Balance      float64 `json:"balance"` // in rupees
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

// BalanceResponse is the response body for GET /wallets/:id/balance.
type BalanceResponse struct {
	WalletID string  `json:"wallet_id"`
	Balance  float64 `json:"balance"` // in rupees
}

// TransactionResponse is a single transaction in the response.
type TransactionResponse struct {
	ID          string  `json:"id"`
	WalletID    string  `json:"wallet_id"`
	Amount      float64 `json:"amount"` // in rupees
	Type        string  `json:"type"`
	Description string  `json:"description"`
	ReferenceID string  `json:"reference_id"`
	CreatedAt   string  `json:"created_at"`
}

// DeductResponse is the response body for POST /wallets/:id/deduct.
type DeductResponse struct {
	Transaction *TransactionResponse `json:"transaction,omitempty"`
	Message     string               `json:"message"`
}

// TopUpResponse is the response body for POST /wallets/:id/topup.
type TopUpResponse struct {
	Transaction *TransactionResponse `json:"transaction"`
	Message     string               `json:"message"`
}

// ErrorResponse is the standard error response body.
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// --- Handler ---

// WalletHandler handles HTTP requests for wallet operations.
// It depends on the WalletService interface, not a concrete implementation.
type WalletHandler struct {
	svc service.WalletService
}

// NewWalletHandler creates a new WalletHandler with the given service.
func NewWalletHandler(svc service.WalletService) *WalletHandler {
	return &WalletHandler{svc: svc}
}

// CreateWallet handles POST /wallets.
func (h *WalletHandler) CreateWallet(w http.ResponseWriter, r *http.Request) {
	var req CreateWalletRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Warn("create wallet: invalid JSON body",
			"error", err,
		)
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON request body")
		return
	}

	wallet, err := h.svc.CreateWallet(r.Context(), req.CustomerName)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	resp := WalletResponse{
		ID:           wallet.ID,
		CustomerName: wallet.CustomerName,
		Balance:      domain.PaiseToRupees(wallet.Balance),
		CreatedAt:    wallet.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:    wallet.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}

	slog.Info("handler: wallet created",
		"wallet_id", wallet.ID,
		"customer_name", wallet.CustomerName,
	)
	writeJSON(w, http.StatusCreated, resp)
}

// TopUp handles POST /wallets/:id/topup.
func (h *WalletHandler) TopUp(w http.ResponseWriter, r *http.Request) {
	walletID := chi.URLParam(r, "id")

	var req TopUpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Warn("topup: invalid JSON body",
			"wallet_id", walletID,
			"error", err,
		)
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON request body")
		return
	}

	amountPaise, errMsg, ok := validateRupeeAmount(req.Amount)
	if !ok {
		slog.Warn("topup: invalid amount",
			"wallet_id", walletID,
			"amount", req.Amount,
			"error", errMsg,
		)
		writeError(w, http.StatusBadRequest, "invalid_amount", errMsg)
		return
	}

	txn, err := h.svc.TopUp(r.Context(), walletID, amountPaise)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	resp := TopUpResponse{
		Transaction: toTransactionResponse(txn),
		Message:     "Top-up successful",
	}

	slog.Info("handler: topup processed",
		"wallet_id", walletID,
		"amount_rupees", req.Amount,
	)
	writeJSON(w, http.StatusOK, resp)
}

// Deduct handles POST /wallets/:id/deduct.
func (h *WalletHandler) Deduct(w http.ResponseWriter, r *http.Request) {
	walletID := chi.URLParam(r, "id")
	idempotencyKey := r.Header.Get("Idempotency-Key")

	if idempotencyKey == "" {
		slog.Warn("deduct: missing idempotency key",
			"wallet_id", walletID,
		)
		writeError(w, http.StatusBadRequest, "missing_idempotency_key", "Idempotency-Key header is required")
		return
	}

	var req DeductRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Warn("deduct: invalid JSON body",
			"wallet_id", walletID,
			"idempotency_key", idempotencyKey,
			"error", err,
		)
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON request body")
		return
	}

	amountPaise, errMsg, ok := validateRupeeAmount(req.Amount)
	if !ok {
		slog.Warn("deduct: invalid amount",
			"wallet_id", walletID,
			"amount", req.Amount,
			"idempotency_key", idempotencyKey,
			"error", errMsg,
		)
		writeError(w, http.StatusBadRequest, "invalid_amount", errMsg)
		return
	}

	txn, isReplay, err := h.svc.Deduct(r.Context(), walletID, amountPaise, idempotencyKey)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	if isReplay {
		slog.Info("handler: deduct idempotent replay",
			"wallet_id", walletID,
			"idempotency_key", idempotencyKey,
		)
		resp := DeductResponse{
			Message: "Deduction already processed (idempotent replay)",
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	resp := DeductResponse{
		Transaction: toTransactionResponse(txn),
		Message:     "Deduction successful",
	}

	slog.Info("handler: deduct processed",
		"wallet_id", walletID,
		"amount_rupees", req.Amount,
		"idempotency_key", idempotencyKey,
	)
	writeJSON(w, http.StatusOK, resp)
}

// GetBalance handles GET /wallets/:id/balance.
func (h *WalletHandler) GetBalance(w http.ResponseWriter, r *http.Request) {
	walletID := chi.URLParam(r, "id")

	balance, err := h.svc.GetBalance(r.Context(), walletID)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	resp := BalanceResponse{
		WalletID: walletID,
		Balance:  domain.PaiseToRupees(balance),
	}

	writeJSON(w, http.StatusOK, resp)
}

// GetTransactions handles GET /wallets/:id/transactions.
func (h *WalletHandler) GetTransactions(w http.ResponseWriter, r *http.Request) {
	walletID := chi.URLParam(r, "id")

	txns, err := h.svc.GetTransactions(r.Context(), walletID)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	resp := make([]TransactionResponse, 0, len(txns))
	for i := range txns {
		resp = append(resp, *toTransactionResponse(&txns[i]))
	}

	writeJSON(w, http.StatusOK, resp)
}

// --- Helpers ---

func toTransactionResponse(txn *domain.Transaction) *TransactionResponse {
	return &TransactionResponse{
		ID:          txn.ID,
		WalletID:    txn.WalletID,
		Amount:      domain.PaiseToRupees(txn.Amount),
		Type:        string(txn.Type),
		Description: txn.Description,
		ReferenceID: txn.ReferenceID,
		CreatedAt:   txn.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("failed to write JSON response",
			"error", err,
		)
	}
}

func writeError(w http.ResponseWriter, status int, errCode, message string) {
	resp := ErrorResponse{
		Error:   errCode,
		Message: message,
	}
	writeJSON(w, status, resp)
}

// handleServiceError maps domain/service errors to HTTP responses.
func handleServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrWalletNotFound):
		writeError(w, http.StatusNotFound, "wallet_not_found", "Wallet not found")
	case errors.Is(err, domain.ErrInsufficientBalance):
		writeError(w, http.StatusUnprocessableEntity, "insufficient_balance", "Insufficient balance to complete this transaction")
	case errors.Is(err, domain.ErrDuplicateWallet):
		writeError(w, http.StatusConflict, "duplicate_wallet", "A wallet with this ID already exists")
	case errors.Is(err, domain.ErrInvalidAmount):
		writeError(w, http.StatusBadRequest, "invalid_amount", "Amount must be greater than zero")
	case errors.Is(err, domain.ErrMissingIdempotencyKey):
		writeError(w, http.StatusBadRequest, "missing_idempotency_key", "Idempotency-Key header is required")
	case errors.Is(err, domain.ErrInvalidCustomerName):
		writeError(w, http.StatusBadRequest, "invalid_customer_name", "Customer name must not be empty")
	default:
		slog.Error("unhandled service error",
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred")
	}
}
