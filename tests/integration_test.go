// Package tests contains full integration tests that spin up a real HTTP server
// and make actual HTTP requests to test the complete request lifecycle:
// HTTP client → router → handler → service → storage → response.
//
// These tests verify the system works end-to-end, including JSON serialisation,
// HTTP status codes, header handling, and cross-endpoint interactions.
package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/walletservice/internal/handler"
	"github.com/walletservice/internal/service"
	"github.com/walletservice/internal/storage/memory"
)

// --- Test Helpers ---

// setupServer creates a fresh test HTTP server for each test.
// Returns the server and a cleanup function.
func setupServer(t *testing.T) *httptest.Server {
	t.Helper()

	walletStore := memory.NewWalletStore()
	txnStore := memory.NewTransactionStore()
	idempStore := memory.NewIdempotencyStore()
	svc := service.NewWalletService(walletStore, txnStore, idempStore)
	h := handler.NewWalletHandler(svc)
	router := handler.NewRouter(h)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server
}

// walletResponse mirrors the handler's WalletResponse for test deserialization.
type walletResponse struct {
	ID           string  `json:"id"`
	CustomerName string  `json:"customer_name"`
	Balance      float64 `json:"balance"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

type balanceResponse struct {
	WalletID string  `json:"wallet_id"`
	Balance  float64 `json:"balance"`
}

type transactionResponse struct {
	ID          string  `json:"id"`
	WalletID    string  `json:"wallet_id"`
	Amount      float64 `json:"amount"`
	Type        string  `json:"type"`
	Description string  `json:"description"`
	ReferenceID string  `json:"reference_id"`
	CreatedAt   string  `json:"created_at"`
}

type deductResponse struct {
	Transaction *transactionResponse `json:"transaction,omitempty"`
	Message     string               `json:"message"`
}

type topUpResponse struct {
	Transaction *transactionResponse `json:"transaction"`
	Message     string               `json:"message"`
}

type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// createWallet is a helper that creates a wallet and returns its ID.
func createWallet(t *testing.T, baseURL, name string) string {
	t.Helper()
	body := fmt.Sprintf(`{"customer_name":"%s"}`, name)
	resp, err := http.Post(baseURL+"/wallets", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var wr walletResponse
	json.NewDecoder(resp.Body).Decode(&wr)
	require.NotEmpty(t, wr.ID)
	return wr.ID
}

// topUp is a helper that tops up a wallet.
func topUp(t *testing.T, baseURL, walletID string, amount float64) {
	t.Helper()
	body := fmt.Sprintf(`{"amount":%v}`, amount)
	resp, err := http.Post(baseURL+"/wallets/"+walletID+"/topup", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// getBalance is a helper that returns the wallet balance.
func getBalance(t *testing.T, baseURL, walletID string) float64 {
	t.Helper()
	resp, err := http.Get(baseURL + "/wallets/" + walletID + "/balance")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var br balanceResponse
	json.NewDecoder(resp.Body).Decode(&br)
	return br.Balance
}

// deduct is a helper that deducts from a wallet with the given idempotency key.
func deduct(t *testing.T, baseURL, walletID, idempotencyKey string, amount float64) (*http.Response, []byte) {
	t.Helper()
	body := fmt.Sprintf(`{"amount":%v}`, amount)
	req, _ := http.NewRequest("POST", baseURL+"/wallets/"+walletID+"/deduct", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idempotencyKey)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, respBody
}

// --- Integration Tests ---

func TestIntegration_CreateWallet_CheckBalance(t *testing.T) {
	server := setupServer(t)

	// Create a wallet → balance should be 0.
	walletID := createWallet(t, server.URL, "Alice")
	balance := getBalance(t, server.URL, walletID)
	assert.Equal(t, 0.0, balance)
}

func TestIntegration_TopUp_CheckBalance(t *testing.T) {
	server := setupServer(t)

	walletID := createWallet(t, server.URL, "Bob")
	topUp(t, server.URL, walletID, 500)

	balance := getBalance(t, server.URL, walletID)
	assert.Equal(t, 500.0, balance)
}

func TestIntegration_TopUp_Deduct_CheckBalance(t *testing.T) {
	server := setupServer(t)

	walletID := createWallet(t, server.URL, "Carol")
	topUp(t, server.URL, walletID, 200)

	resp, _ := deduct(t, server.URL, walletID, "order-1", 100)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	balance := getBalance(t, server.URL, walletID)
	assert.Equal(t, 100.0, balance)
}

func TestIntegration_InsufficientBalance_Rejected(t *testing.T) {
	server := setupServer(t)

	walletID := createWallet(t, server.URL, "Dave")
	topUp(t, server.URL, walletID, 50) // Only ₹50

	resp, body := deduct(t, server.URL, walletID, "order-insuf", 100)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	var errResp errorResponse
	json.Unmarshal(body, &errResp)
	assert.Equal(t, "insufficient_balance", errResp.Error)

	// Balance should remain unchanged.
	balance := getBalance(t, server.URL, walletID)
	assert.Equal(t, 50.0, balance)
}

func TestIntegration_Idempotency_SameKey_DeductsOnce(t *testing.T) {
	server := setupServer(t)

	walletID := createWallet(t, server.URL, "Eve")
	topUp(t, server.URL, walletID, 300)

	// First deduct with key "order-abc"
	resp1, body1 := deduct(t, server.URL, walletID, "order-abc", 100)
	assert.Equal(t, http.StatusOK, resp1.StatusCode)

	var dr1 deductResponse
	json.Unmarshal(body1, &dr1)
	assert.Equal(t, "Deduction successful", dr1.Message)

	// Second deduct with SAME key "order-abc" — should be idempotent replay
	resp2, body2 := deduct(t, server.URL, walletID, "order-abc", 100)
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var dr2 deductResponse
	json.Unmarshal(body2, &dr2)
	assert.Contains(t, dr2.Message, "idempotent")

	// Balance should only reflect ONE deduction (₹300 - ₹100 = ₹200)
	balance := getBalance(t, server.URL, walletID)
	assert.Equal(t, 200.0, balance)
}

func TestIntegration_Idempotency_DifferentKeys_DeductsTwice(t *testing.T) {
	server := setupServer(t)

	walletID := createWallet(t, server.URL, "Frank")
	topUp(t, server.URL, walletID, 300)

	deduct(t, server.URL, walletID, "order-x", 100)
	deduct(t, server.URL, walletID, "order-y", 100)

	balance := getBalance(t, server.URL, walletID)
	assert.Equal(t, 100.0, balance) // ₹300 - ₹100 - ₹100 = ₹100
}

func TestIntegration_MultipleTopups_TransactionHistory(t *testing.T) {
	server := setupServer(t)

	walletID := createWallet(t, server.URL, "Grace")
	topUp(t, server.URL, walletID, 100)
	topUp(t, server.URL, walletID, 200)
	topUp(t, server.URL, walletID, 300)

	// Fetch transaction history
	resp, err := http.Get(server.URL + "/wallets/" + walletID + "/transactions")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var txns []transactionResponse
	json.NewDecoder(resp.Body).Decode(&txns)
	assert.Len(t, txns, 3)

	// All should be CREDIT
	for _, txn := range txns {
		assert.Equal(t, "CREDIT", txn.Type)
	}
	assert.Equal(t, 100.0, txns[0].Amount)
	assert.Equal(t, 200.0, txns[1].Amount)
	assert.Equal(t, 300.0, txns[2].Amount)
}

func TestIntegration_TopupAndDeduct_TransactionHistory(t *testing.T) {
	server := setupServer(t)

	walletID := createWallet(t, server.URL, "Heidi")
	topUp(t, server.URL, walletID, 500)
	deduct(t, server.URL, walletID, "order-hist", 100)

	resp, err := http.Get(server.URL + "/wallets/" + walletID + "/transactions")
	require.NoError(t, err)
	defer resp.Body.Close()

	var txns []transactionResponse
	json.NewDecoder(resp.Body).Decode(&txns)
	assert.Len(t, txns, 2)
	assert.Equal(t, "CREDIT", txns[0].Type)
	assert.Equal(t, 500.0, txns[0].Amount)
	assert.Equal(t, "DEBIT", txns[1].Type)
	assert.Equal(t, 100.0, txns[1].Amount)
}

func TestIntegration_WalletNotFound_AllEndpoints(t *testing.T) {
	server := setupServer(t)
	fakeID := "nonexistent-wallet-id"

	t.Run("balance", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/wallets/" + fakeID + "/balance")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("topup", func(t *testing.T) {
		body := `{"amount":100}`
		resp, err := http.Post(server.URL+"/wallets/"+fakeID+"/topup", "application/json", bytes.NewBufferString(body))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("deduct", func(t *testing.T) {
		resp, _ := deduct(t, server.URL, fakeID, "order-nf", 100)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("transactions", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/wallets/" + fakeID + "/transactions")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestIntegration_ConcurrentDeductions_NeverNegative(t *testing.T) {
	server := setupServer(t)

	walletID := createWallet(t, server.URL, "ConcurrentUser")
	topUp(t, server.URL, walletID, 500) // ₹500

	// Launch 10 goroutines, each trying to deduct ₹100.
	// Only 5 should succeed.
	var wg sync.WaitGroup
	successCount := 0
	failCount := 0
	var mu sync.Mutex

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := fmt.Sprintf("concurrent-order-%d", idx)
			resp, _ := deduct(t, server.URL, walletID, key, 100)

			mu.Lock()
			defer mu.Unlock()
			if resp.StatusCode == http.StatusOK {
				successCount++
			} else {
				failCount++
			}
		}(i)
	}

	wg.Wait()

	assert.Equal(t, 5, successCount, "exactly 5 deductions should succeed")
	assert.Equal(t, 5, failCount, "exactly 5 deductions should fail")

	balance := getBalance(t, server.URL, walletID)
	assert.Equal(t, 0.0, balance)
	assert.True(t, balance >= 0, "balance must NEVER go negative")
}

func TestIntegration_ExactBalance_ThenReject(t *testing.T) {
	server := setupServer(t)

	walletID := createWallet(t, server.URL, "ExactUser")
	topUp(t, server.URL, walletID, 100) // Exactly ₹100

	// First deduct should succeed
	resp1, _ := deduct(t, server.URL, walletID, "order-exact-1", 100)
	assert.Equal(t, http.StatusOK, resp1.StatusCode)

	balance := getBalance(t, server.URL, walletID)
	assert.Equal(t, 0.0, balance)

	// Second deduct should fail
	resp2, body2 := deduct(t, server.URL, walletID, "order-exact-2", 100)
	assert.Equal(t, http.StatusUnprocessableEntity, resp2.StatusCode)

	var errResp errorResponse
	json.Unmarshal(body2, &errResp)
	assert.Equal(t, "insufficient_balance", errResp.Error)
}

func TestIntegration_InvalidTopupAmount(t *testing.T) {
	server := setupServer(t)
	walletID := createWallet(t, server.URL, "InvalidUser")

	t.Run("negative amount", func(t *testing.T) {
		body := `{"amount":-100}`
		resp, err := http.Post(server.URL+"/wallets/"+walletID+"/topup", "application/json", bytes.NewBufferString(body))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("zero amount", func(t *testing.T) {
		body := `{"amount":0}`
		resp, err := http.Post(server.URL+"/wallets/"+walletID+"/topup", "application/json", bytes.NewBufferString(body))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func TestIntegration_DeductZeroAmount_Rejected(t *testing.T) {
	server := setupServer(t)

	walletID := createWallet(t, server.URL, "ZeroAmountUser")
	topUp(t, server.URL, walletID, 500)

	// Send empty body — should return 400 (zero amount is not allowed).
	// Previously this silently defaulted to ₹100, which was a dangerous
	// behavior for a financial API.
	req, _ := http.NewRequest("POST", server.URL+"/wallets/"+walletID+"/deduct", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "order-zero")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// Balance should be unchanged.
	balance := getBalance(t, server.URL, walletID)
	assert.Equal(t, 500.0, balance)
}

func TestIntegration_ConcurrentSameIdempotencyKey_DeductsOnce(t *testing.T) {
	// This test verifies the fix for the idempotency race condition.
	// 10 goroutines send the SAME idempotency key concurrently.
	// Only 1 deduction should occur — the rest should be replays.
	server := setupServer(t)

	walletID := createWallet(t, server.URL, "IdempotencyRaceUser")
	topUp(t, server.URL, walletID, 500) // ₹500

	var wg sync.WaitGroup
	successCount := 0
	replayCount := 0
	var mu sync.Mutex

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// ALL goroutines use the same idempotency key
			resp, body := deduct(t, server.URL, walletID, "same-order-key", 100)

			mu.Lock()
			defer mu.Unlock()
			if resp.StatusCode == http.StatusOK {
				var dr deductResponse
				json.Unmarshal(body, &dr)
				if dr.Transaction != nil {
					successCount++ // First processing
				} else {
					replayCount++ // Idempotent replay
				}
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, 1, successCount, "exactly 1 deduction should succeed")
	assert.Equal(t, 9, replayCount, "exactly 9 should be idempotent replays")

	// Balance should reflect exactly ONE deduction: ₹500 - ₹100 = ₹400
	balance := getBalance(t, server.URL, walletID)
	assert.Equal(t, 400.0, balance, "balance should reflect exactly one deduction")
}


func TestIntegration_FullOrderLifecycle(t *testing.T) {
	// This test simulates the full Order Service interaction pattern:
	// 1. Customer creates wallet
	// 2. Customer tops up ₹500
	// 3. 5 orders placed (each ₹100)
	// 4. 6th order rejected
	// 5. Check final balance = 0
	// 6. Check transaction history = 6 entries (1 topup + 5 deducts)

	server := setupServer(t)

	// Step 1: Create wallet
	walletID := createWallet(t, server.URL, "OrderLifecycleUser")

	// Step 2: Top up ₹500
	topUp(t, server.URL, walletID, 500)

	// Step 3: Place 5 orders
	for i := 1; i <= 5; i++ {
		key := fmt.Sprintf("order-%d", i)
		resp, _ := deduct(t, server.URL, walletID, key, 100)
		assert.Equal(t, http.StatusOK, resp.StatusCode, "Order %d should succeed", i)
	}

	// Step 4: 6th order should fail
	resp, body := deduct(t, server.URL, walletID, "order-6", 100)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	var errResp errorResponse
	json.Unmarshal(body, &errResp)
	assert.Equal(t, "insufficient_balance", errResp.Error)

	// Step 5: Final balance = 0
	balance := getBalance(t, server.URL, walletID)
	assert.Equal(t, 0.0, balance)

	// Step 6: Transaction history
	txnResp, err := http.Get(server.URL + "/wallets/" + walletID + "/transactions")
	require.NoError(t, err)
	defer txnResp.Body.Close()

	var txns []transactionResponse
	json.NewDecoder(txnResp.Body).Decode(&txns)
	assert.Len(t, txns, 6) // 1 topup + 5 deducts

	// First entry is CREDIT, rest are DEBIT
	assert.Equal(t, "CREDIT", txns[0].Type)
	assert.Equal(t, 500.0, txns[0].Amount)
	for i := 1; i <= 5; i++ {
		assert.Equal(t, "DEBIT", txns[i].Type)
		assert.Equal(t, 100.0, txns[i].Amount)
	}
}

func TestIntegration_HealthCheck(t *testing.T) {
	server := setupServer(t)

	resp, err := http.Get(server.URL + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	assert.Equal(t, "ok", body["status"])
}

func TestIntegration_ResponseContentType(t *testing.T) {
	server := setupServer(t)
	walletID := createWallet(t, server.URL, "ContentTypeUser")

	endpoints := []struct {
		method string
		path   string
		body   string
		header map[string]string
	}{
		{"GET", "/wallets/" + walletID + "/balance", "", nil},
		{"GET", "/wallets/" + walletID + "/transactions", "", nil},
		{"POST", "/wallets/" + walletID + "/topup", `{"amount":100}`, nil},
		{"POST", "/wallets/" + walletID + "/deduct", `{"amount":100}`, map[string]string{"Idempotency-Key": "ct-test"}},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			var bodyReader io.Reader
			if ep.body != "" {
				bodyReader = bytes.NewBufferString(ep.body)
			}
			req, _ := http.NewRequest(ep.method, server.URL+ep.path, bodyReader)
			req.Header.Set("Content-Type", "application/json")
			for k, v := range ep.header {
				req.Header.Set(k, v)
			}
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")
		})
	}
}

func TestIntegration_FractionalTopup(t *testing.T) {
	server := setupServer(t)
	walletID := createWallet(t, server.URL, "FractionalUser")

	topUp(t, server.URL, walletID, 99.50)

	balance := getBalance(t, server.URL, walletID)
	assert.Equal(t, 99.50, balance)
}

func TestIntegration_MultipleWallets_Isolated(t *testing.T) {
	server := setupServer(t)

	wallet1 := createWallet(t, server.URL, "User1")
	wallet2 := createWallet(t, server.URL, "User2")

	topUp(t, server.URL, wallet1, 1000)
	topUp(t, server.URL, wallet2, 200)

	deduct(t, server.URL, wallet1, "w1-order", 100)

	balance1 := getBalance(t, server.URL, wallet1)
	balance2 := getBalance(t, server.URL, wallet2)

	assert.Equal(t, 900.0, balance1)
	assert.Equal(t, 200.0, balance2, "wallet2 should be unaffected by wallet1 operations")
}
