package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/walletservice/internal/domain"
	"github.com/walletservice/internal/service"
	"github.com/walletservice/internal/storage/memory"
)

// newTestRouter creates a fully wired router with in-memory storage for testing.
func newTestRouter() *http.Handler {
	ws := memory.NewWalletStore()
	ts := memory.NewTransactionStore()
	is := memory.NewIdempotencyStore()
	svc := service.NewWalletService(ws, ts, is)
	h := NewWalletHandler(svc)
	router := NewRouter(h)
	var handler http.Handler = router
	return &handler
}

// Helper to create a wallet and return its ID.
func createWalletHelper(t *testing.T, handler http.Handler, name string) string {
	t.Helper()
	body := map[string]string{"customer_name": name}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/wallets", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	var resp WalletResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	return resp.ID
}

func TestHandler_CreateWallet(t *testing.T) {
	router := *newTestRouter()

	t.Run("success", func(t *testing.T) {
		body := `{"customer_name":"Alice"}`
		req := httptest.NewRequest("POST", "/wallets", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)

		var resp WalletResponse
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.NotEmpty(t, resp.ID)
		assert.Equal(t, "Alice", resp.CustomerName)
		assert.Equal(t, 0.0, resp.Balance)
	})

	t.Run("empty name returns 400", func(t *testing.T) {
		body := `{"customer_name":""}`
		req := httptest.NewRequest("POST", "/wallets", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid JSON returns 400", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/wallets", bytes.NewBufferString("not json"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestHandler_TopUp(t *testing.T) {
	router := *newTestRouter()
	walletID := createWalletHelper(t, router, "Alice")

	t.Run("success", func(t *testing.T) {
		body := `{"amount":500}`
		req := httptest.NewRequest("POST", "/wallets/"+walletID+"/topup", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp TopUpResponse
		json.Unmarshal(w.Body.Bytes(), &resp)
		assert.Equal(t, "Top-up successful", resp.Message)
		assert.Equal(t, 500.0, resp.Transaction.Amount)
		assert.Equal(t, "CREDIT", resp.Transaction.Type)
	})

	t.Run("negative amount returns 400", func(t *testing.T) {
		body := `{"amount":-100}`
		req := httptest.NewRequest("POST", "/wallets/"+walletID+"/topup", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("zero amount returns 400", func(t *testing.T) {
		body := `{"amount":0}`
		req := httptest.NewRequest("POST", "/wallets/"+walletID+"/topup", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		var resp ErrorResponse
		json.Unmarshal(w.Body.Bytes(), &resp)
		assert.Equal(t, "invalid_amount", resp.Error)
		assert.Equal(t, "Amount must be greater than zero", resp.Message)
	})

	t.Run("sub-paisa decimal precision returns 400", func(t *testing.T) {
		body := `{"amount":0.001}`
		req := httptest.NewRequest("POST", "/wallets/"+walletID+"/topup", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		var resp ErrorResponse
		json.Unmarshal(w.Body.Bytes(), &resp)
		assert.Equal(t, "invalid_amount", resp.Error)
		assert.Equal(t, "Amount cannot have more than 2 decimal places", resp.Message)
	})

	t.Run("minimum amount 1 paisa (0.01) succeeds", func(t *testing.T) {
		body := `{"amount":0.01}`
		req := httptest.NewRequest("POST", "/wallets/"+walletID+"/topup", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("wallet not found returns 404", func(t *testing.T) {
		body := `{"amount":100}`
		req := httptest.NewRequest("POST", "/wallets/nonexistent/topup", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

func TestHandler_Deduct(t *testing.T) {
	router := *newTestRouter()
	walletID := createWalletHelper(t, router, "Alice")

	// Top up ₹500
	topupBody := `{"amount":500}`
	topupReq := httptest.NewRequest("POST", "/wallets/"+walletID+"/topup", bytes.NewBufferString(topupBody))
	topupReq.Header.Set("Content-Type", "application/json")
	topupW := httptest.NewRecorder()
	router.ServeHTTP(topupW, topupReq)
	require.Equal(t, http.StatusOK, topupW.Code)

	t.Run("zero amount returns 400", func(t *testing.T) {
		body := `{}`
		req := httptest.NewRequest("POST", "/wallets/"+walletID+"/deduct", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "order-001")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var resp ErrorResponse
		json.Unmarshal(w.Body.Bytes(), &resp)
		assert.Equal(t, "invalid_amount", resp.Error)
	})

	t.Run("success with explicit amount", func(t *testing.T) {
		body := `{"amount":100}`
		req := httptest.NewRequest("POST", "/wallets/"+walletID+"/deduct", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "order-002")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("missing idempotency key returns 400", func(t *testing.T) {
		body := `{"amount":100}`
		req := httptest.NewRequest("POST", "/wallets/"+walletID+"/deduct", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var resp ErrorResponse
		json.Unmarshal(w.Body.Bytes(), &resp)
		assert.Equal(t, "missing_idempotency_key", resp.Error)
	})

	t.Run("insufficient balance returns 422", func(t *testing.T) {
		// Create a fresh wallet with ₹50
		w2ID := createWalletHelper(t, router, "LowBal")
		topBody := `{"amount":50}`
		tr := httptest.NewRequest("POST", "/wallets/"+w2ID+"/topup", bytes.NewBufferString(topBody))
		tr.Header.Set("Content-Type", "application/json")
		tw := httptest.NewRecorder()
		router.ServeHTTP(tw, tr)

		body := `{"amount":100}`
		req := httptest.NewRequest("POST", "/wallets/"+w2ID+"/deduct", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "order-insuf")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

		var resp ErrorResponse
		json.Unmarshal(w.Body.Bytes(), &resp)
		assert.Equal(t, "insufficient_balance", resp.Error)
	})

	t.Run("idempotent replay returns 200", func(t *testing.T) {
		// Use the same key as the successful deduct ("order-002")
		body := `{"amount":100}`
		req := httptest.NewRequest("POST", "/wallets/"+walletID+"/deduct", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "order-002")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp DeductResponse
		json.Unmarshal(w.Body.Bytes(), &resp)
		assert.Contains(t, resp.Message, "idempotent")
	})

	t.Run("sub-paisa decimal precision returns 400", func(t *testing.T) {
		body := `{"amount":0.005}`
		req := httptest.NewRequest("POST", "/wallets/"+walletID+"/deduct", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "order-subpaisa")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		var resp ErrorResponse
		json.Unmarshal(w.Body.Bytes(), &resp)
		assert.Equal(t, "invalid_amount", resp.Error)
		assert.Equal(t, "Amount cannot have more than 2 decimal places", resp.Message)
	})

	t.Run("wallet not found returns 404", func(t *testing.T) {
		body := `{"amount":100}`
		req := httptest.NewRequest("POST", "/wallets/nonexistent/deduct", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "order-nf")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

func TestHandler_GetBalance(t *testing.T) {
	router := *newTestRouter()
	walletID := createWalletHelper(t, router, "Alice")

	t.Run("zero balance", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/wallets/"+walletID+"/balance", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp BalanceResponse
		json.Unmarshal(w.Body.Bytes(), &resp)
		assert.Equal(t, walletID, resp.WalletID)
		assert.Equal(t, 0.0, resp.Balance)
	})

	t.Run("after topup", func(t *testing.T) {
		body := `{"amount":250.50}`
		topReq := httptest.NewRequest("POST", "/wallets/"+walletID+"/topup", bytes.NewBufferString(body))
		topReq.Header.Set("Content-Type", "application/json")
		tw := httptest.NewRecorder()
		router.ServeHTTP(tw, topReq)

		req := httptest.NewRequest("GET", "/wallets/"+walletID+"/balance", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp BalanceResponse
		json.Unmarshal(w.Body.Bytes(), &resp)
		assert.Equal(t, 250.50, resp.Balance)
	})

	t.Run("wallet not found returns 404", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/wallets/nonexistent/balance", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

func TestHandler_GetTransactions(t *testing.T) {
	router := *newTestRouter()
	walletID := createWalletHelper(t, router, "Alice")

	t.Run("empty history", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/wallets/"+walletID+"/transactions", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp []TransactionResponse
		json.Unmarshal(w.Body.Bytes(), &resp)
		assert.Len(t, resp, 0)
	})

	t.Run("after topup and deduct", func(t *testing.T) {
		// TopUp
		topBody := `{"amount":500}`
		topReq := httptest.NewRequest("POST", "/wallets/"+walletID+"/topup", bytes.NewBufferString(topBody))
		topReq.Header.Set("Content-Type", "application/json")
		tw := httptest.NewRecorder()
		router.ServeHTTP(tw, topReq)

		// Deduct
		dedBody := `{"amount":100}`
		dedReq := httptest.NewRequest("POST", "/wallets/"+walletID+"/deduct", bytes.NewBufferString(dedBody))
		dedReq.Header.Set("Content-Type", "application/json")
		dedReq.Header.Set("Idempotency-Key", "order-txn-test")
		dw := httptest.NewRecorder()
		router.ServeHTTP(dw, dedReq)

		req := httptest.NewRequest("GET", "/wallets/"+walletID+"/transactions", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp []TransactionResponse
		json.Unmarshal(w.Body.Bytes(), &resp)
		assert.Len(t, resp, 2)
		assert.Equal(t, "CREDIT", resp[0].Type)
		assert.Equal(t, 500.0, resp[0].Amount)
		assert.Equal(t, "DEBIT", resp[1].Type)
		assert.Equal(t, 100.0, resp[1].Amount)
	})

	t.Run("wallet not found returns 404", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/wallets/nonexistent/transactions", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

func TestHandler_HealthCheck(t *testing.T) {
	router := *newTestRouter()

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "ok", resp["status"])
}

// --- Mock WalletService for testing error mapping ---

type mockWalletService struct {
	createWalletErr     error
	topUpErr            error
	deductErr           error
	getBalanceErr       error
	getTransactionsErr  error
}

func (m *mockWalletService) CreateWallet(_ context.Context, _ string) (*domain.Wallet, error) {
	if m.createWalletErr != nil {
		return nil, m.createWalletErr
	}
	return &domain.Wallet{ID: "mock-id", CustomerName: "Mock"}, nil
}

func (m *mockWalletService) TopUp(_ context.Context, _ string, _ int64) (*domain.Transaction, error) {
	if m.topUpErr != nil {
		return nil, m.topUpErr
	}
	return &domain.Transaction{ID: "t-mock", WalletID: "mock-id", Amount: 10000, Type: domain.TransactionTypeCredit}, nil
}

func (m *mockWalletService) Deduct(_ context.Context, _ string, _ int64, _ string) (*domain.Transaction, bool, error) {
	if m.deductErr != nil {
		return nil, false, m.deductErr
	}
	return &domain.Transaction{ID: "t-mock", WalletID: "mock-id", Amount: 10000, Type: domain.TransactionTypeDebit}, false, nil
}

func (m *mockWalletService) GetBalance(_ context.Context, _ string) (int64, error) {
	if m.getBalanceErr != nil {
		return 0, m.getBalanceErr
	}
	return 50000, nil
}

func (m *mockWalletService) GetTransactions(_ context.Context, _ string) ([]domain.Transaction, error) {
	if m.getTransactionsErr != nil {
		return nil, m.getTransactionsErr
	}
	return []domain.Transaction{}, nil
}

func TestHandler_ErrorMapping_DuplicateWallet(t *testing.T) {
	mock := &mockWalletService{createWalletErr: domain.ErrDuplicateWallet}
	h := NewWalletHandler(mock)
	router := NewRouter(h)

	body := `{"customer_name":"Alice"}`
	req := httptest.NewRequest("POST", "/wallets", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	var resp ErrorResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "duplicate_wallet", resp.Error)
}

func TestHandler_ErrorMapping_InternalError(t *testing.T) {
	mock := &mockWalletService{getBalanceErr: fmt.Errorf("unexpected db failure")}
	h := NewWalletHandler(mock)
	router := NewRouter(h)

	req := httptest.NewRequest("GET", "/wallets/some-id/balance", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var resp ErrorResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "internal_error", resp.Error)
}

func TestHandler_ErrorMapping_InvalidAmount_TopUp(t *testing.T) {
	mock := &mockWalletService{topUpErr: domain.ErrInvalidAmount}
	h := NewWalletHandler(mock)
	router := NewRouter(h)

	body := `{"amount":100}`
	req := httptest.NewRequest("POST", "/wallets/some-id/topup", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp ErrorResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "invalid_amount", resp.Error)
}

func TestHandler_ErrorMapping_MissingIdempotencyKey_Service(t *testing.T) {
	// Test the service-level idempotency key error (handler also validates,
	// but this tests the handleServiceError path).
	mock := &mockWalletService{deductErr: domain.ErrMissingIdempotencyKey}
	h := NewWalletHandler(mock)
	router := NewRouter(h)

	body := `{"amount":100}`
	req := httptest.NewRequest("POST", "/wallets/some-id/deduct", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "key-123") // Pass header so handler doesn't reject first
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp ErrorResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "missing_idempotency_key", resp.Error)
}

func TestHandler_ErrorMapping_InvalidCustomerName(t *testing.T) {
	mock := &mockWalletService{createWalletErr: domain.ErrInvalidCustomerName}
	h := NewWalletHandler(mock)
	router := NewRouter(h)

	body := `{"customer_name":"valid"}`
	req := httptest.NewRequest("POST", "/wallets", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp ErrorResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "invalid_customer_name", resp.Error)
}

func TestHandler_ErrorMapping_InternalError_Transactions(t *testing.T) {
	mock := &mockWalletService{getTransactionsErr: fmt.Errorf("some unexpected error")}
	h := NewWalletHandler(mock)
	router := NewRouter(h)

	req := httptest.NewRequest("GET", "/wallets/some-id/transactions", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandler_Deduct_InvalidJSON(t *testing.T) {
	router := *newTestRouter()
	walletID := createWalletHelper(t, router, "TestInvalidJSON")

	req := httptest.NewRequest("POST", "/wallets/"+walletID+"/deduct", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "key-invalid-json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandler_TopUp_InvalidJSON(t *testing.T) {
	router := *newTestRouter()
	walletID := createWalletHelper(t, router, "TestInvalidJSON2")

	req := httptest.NewRequest("POST", "/wallets/"+walletID+"/topup", bytes.NewBufferString("broken"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandler_Deduct_NegativeAmount(t *testing.T) {
	router := *newTestRouter()
	walletID := createWalletHelper(t, router, "TestNegativeDeduct")

	body := `{"amount":-50}`
	req := httptest.NewRequest("POST", "/wallets/"+walletID+"/deduct", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "key-negative")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

