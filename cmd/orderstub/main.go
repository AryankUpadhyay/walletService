// Package main implements an Order Service stub that demonstrates the
// integration between the Order Service and the Wallet Service.
//
// This is NOT the real Order Service — it's a simple script that
// simulates the order placement lifecycle by calling the Wallet Service API.
//
// Usage:
//
//	# First, start the Wallet Service:
//	go run cmd/api/main.go
//
//	# Then, in another terminal:
//	go run cmd/orderstub/main.go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
)

const (
	defaultBaseURL    = "http://localhost:8080"
	orderDeductAmount = 100.0 // ₹100 per order
)

func main() {
	baseURL := os.Getenv("WALLET_SERVICE_URL")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	fmt.Println("=== Order Service Stub ===")
	fmt.Printf("Wallet Service URL: %s\n\n", baseURL)

	// Step 1: Create a customer wallet
	fmt.Println("--- Step 1: Creating customer wallet ---")
	walletID := createWallet(baseURL, "Acme Corp")
	fmt.Printf("✅ Wallet created: %s\n\n", walletID)

	// Step 2: Customer tops up ₹500
	fmt.Println("--- Step 2: Customer tops up ₹500 ---")
	topUp(baseURL, walletID, 500)
	fmt.Printf("✅ Top-up successful\n\n")

	// Step 3: Check balance
	fmt.Println("--- Step 3: Checking balance ---")
	balance := getBalance(baseURL, walletID)
	fmt.Printf("✅ Balance: ₹%.2f\n\n", balance)

	// Step 4: Place 5 orders (each deducts ₹100)
	fmt.Println("--- Step 4: Placing 5 orders ---")
	for i := 1; i <= 5; i++ {
		orderID := fmt.Sprintf("ORD-%04d", i)
		fmt.Printf("  Placing order %s... ", orderID)
		success := placeOrder(baseURL, walletID, orderID)
		if success {
			fmt.Println("✅ Order confirmed")
		} else {
			fmt.Println("❌ Order rejected")
		}
	}
	fmt.Println()

	// Step 5: Check balance after orders
	fmt.Println("--- Step 5: Checking balance after orders ---")
	balance = getBalance(baseURL, walletID)
	fmt.Printf("✅ Balance: ₹%.2f\n\n", balance)

	// Step 6: Try a 6th order — should fail (insufficient balance)
	fmt.Println("--- Step 6: Placing 6th order (should fail) ---")
	success := placeOrder(baseURL, walletID, "ORD-0006")
	if success {
		fmt.Println("❌ BUG: Order should have been rejected!")
	} else {
		fmt.Println("✅ Order correctly rejected (insufficient balance)")
	}
	fmt.Println()

	// Step 7: Demonstrate idempotency — retry order 3
	fmt.Println("--- Step 7: Retrying order ORD-0003 (idempotency test) ---")
	balanceBefore := getBalance(baseURL, walletID)
	fmt.Printf("  Balance before retry: ₹%.2f\n", balanceBefore)

	placeOrder(baseURL, walletID, "ORD-0003") // Same order ID as step 4
	balanceAfter := getBalance(baseURL, walletID)
	fmt.Printf("  Balance after retry:  ₹%.2f\n", balanceAfter)

	if balanceBefore == balanceAfter {
		fmt.Println("✅ Idempotency works! Balance unchanged on retry")
	} else {
		fmt.Println("❌ BUG: Balance changed on retry — idempotency broken!")
	}
	fmt.Println()

	// Step 8: Print transaction history
	fmt.Println("--- Step 8: Transaction History ---")
	printTransactions(baseURL, walletID)

	fmt.Println("\n=== Order Service Stub Complete ===")
}

// --- Wallet Service API Calls ---

func createWallet(baseURL, name string) string {
	body := fmt.Sprintf(`{"customer_name":"%s"}`, name)
	resp, err := http.Post(baseURL+"/wallets", "application/json", bytes.NewBufferString(body))
	if err != nil {
		log.Fatalf("Failed to create wallet: %v", err)
	}
	defer resp.Body.Close()

	// Read body once into bytes to avoid double-read bug.
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Failed to read response body: %v", err)
	}

	if resp.StatusCode != http.StatusCreated {
		log.Fatalf("Create wallet failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		log.Fatalf("Failed to parse create wallet response: %v", err)
	}

	id, ok := result["id"].(string)
	if !ok {
		log.Fatalf("Unexpected response format: 'id' field missing or not a string")
	}
	return id
}

func topUp(baseURL, walletID string, amount float64) {
	body := fmt.Sprintf(`{"amount":%v}`, amount)
	resp, err := http.Post(baseURL+"/wallets/"+walletID+"/topup", "application/json", bytes.NewBufferString(body))
	if err != nil {
		log.Fatalf("Failed to top up: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		log.Fatalf("Top-up failed (status %d): %s", resp.StatusCode, string(respBody))
	}
}

func getBalance(baseURL, walletID string) float64 {
	resp, err := http.Get(baseURL + "/wallets/" + walletID + "/balance")
	if err != nil {
		log.Fatalf("Failed to get balance: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Fatalf("Failed to parse balance response: %v", err)
	}

	balance, ok := result["balance"].(float64)
	if !ok {
		log.Fatalf("Unexpected response format: 'balance' field missing or not a number")
	}
	return balance
}

func placeOrder(baseURL, walletID, orderID string) bool {
	// Always send an explicit amount — never rely on server defaults for financial operations.
	body := fmt.Sprintf(`{"amount":%v}`, orderDeductAmount)
	req, err := http.NewRequest("POST", baseURL+"/wallets/"+walletID+"/deduct", bytes.NewBufferString(body))
	if err != nil {
		log.Fatalf("Failed to create deduct request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", orderID) // Order ID serves as the idempotency key

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatalf("Failed to deduct: %v", err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body) // drain body

	return resp.StatusCode == http.StatusOK
}

func printTransactions(baseURL, walletID string) {
	resp, err := http.Get(baseURL + "/wallets/" + walletID + "/transactions")
	if err != nil {
		log.Fatalf("Failed to get transactions: %v", err)
	}
	defer resp.Body.Close()

	var txns []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&txns); err != nil {
		log.Fatalf("Failed to parse transactions response: %v", err)
	}

	fmt.Printf("  %-6s  %-8s  %-10s  %s\n", "Type", "Amount", "Ref ID", "Description")
	fmt.Printf("  %-6s  %-8s  %-10s  %s\n", "------", "--------", "----------", "-----------")
	for _, txn := range txns {
		txnType, _ := txn["type"].(string)
		amount, _ := txn["amount"].(float64)
		refID, _ := txn["reference_id"].(string)
		desc, _ := txn["description"].(string)

		symbol := "+"
		if txnType == "DEBIT" {
			symbol = "-"
		}
		fmt.Printf("  %-6s  %s₹%-6.2f  %-10s  %s\n", txnType, symbol, amount, refID, desc)
	}
}
