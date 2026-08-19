# Wallet Service

A production-grade HTTP API for managing customer wallets in a logistics platform. Built with clean architecture principles, interface-driven design, and comprehensive test coverage.

## System Overview

```
Customer places order → Order Service → POST /wallets/:id/deduct → Wallet Service
Customer tops up      → POST /wallets/:id/topup                 → Wallet Service
Customer checks bal.  → GET /wallets/:id/balance                → Wallet Service
```

The Wallet Service owns balances, records every money movement, and enforces the balance constraint (balance must never go negative).

## Architecture

```
┌──────────────────────────────────────────────────────────────────┐
│                          cmd/api/main.go                         │
│                     (Dependency Wiring Only)                     │
└──────────────────────┬───────────────────────────────────────────┘
                       │
┌──────────────────────▼───────────────────────────────────────────┐
│                    internal/handler/                              │
│            HTTP Transport (chi router, DTOs)                     │
│         Maps domain errors → HTTP status codes                   │
│         Depends on: service.WalletService (interface)            │
└──────────────────────┬───────────────────────────────────────────┘
                       │
┌──────────────────────▼───────────────────────────────────────────┐
│                    internal/service/                              │
│              Business Logic & Orchestration                      │
│         Enforces balance constraint, idempotency                 │
│         Depends on: domain.{WalletRepository,                    │
│                     TransactionRepository, IdempotencyStore}     │
└──────────────────────┬───────────────────────────────────────────┘
                       │
┌──────────────────────▼───────────────────────────────────────────┐
│                    internal/domain/                               │
│             Entities, Errors, Repository Interfaces              │
│                   Zero external dependencies                     │
└──────────────────────────────────────────────────────────────────┘
                       │
┌──────────────────────▼───────────────────────────────────────────┐
│               internal/storage/memory/                           │
│         In-Memory Implementations (thread-safe)                  │
│         Swappable to Redis, DynamoDB, PostgreSQL                 │
└──────────────────────────────────────────────────────────────────┘
```


## API Endpoints

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/wallets` | Create a new wallet |
| `POST` | `/wallets/:id/topup` | Add funds (amount in rupees) |
| `POST` | `/wallets/:id/deduct` | Deduct funds (requires `Idempotency-Key` header) |
| `GET`  | `/wallets/:id/balance` | Get current balance |
| `GET`  | `/wallets/:id/transactions` | Get transaction history |
| `GET`  | `/health` | Health check |

### Example Usage

```bash
# Create a wallet
curl -X POST http://localhost:8080/wallets \
  -H "Content-Type: application/json" \
  -d '{"customer_name": "Acme Corp"}'

# Top up ₹500
curl -X POST http://localhost:8080/wallets/{id}/topup \
  -H "Content-Type: application/json" \
  -d '{"amount": 500}'

# Deduct ₹100 (order placement)
curl -X POST http://localhost:8080/wallets/{id}/deduct \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: order-12345" \
  -d '{"amount": 100}'

# Check balance
curl http://localhost:8080/wallets/{id}/balance

# Transaction history
curl http://localhost:8080/wallets/{id}/transactions
```

## Key Design Decisions

### 1. Balance in Paise (Integer, Not Float)

All internal balance and amount calculations use **int64 paise** (1 rupee = 100 paise). The API accepts/returns rupee amounts as floats but converts immediately at the boundary. This eliminates floating-point rounding errors that could cause incorrect balance calculations in financial systems.

### 2. Idempotency via `Idempotency-Key` Header

The `/deduct` endpoint requires an `Idempotency-Key` header (typically the Order ID). If the same key is sent twice:
- First request: processes the deduction, stores the key
- Subsequent requests: returns `200 OK` with a "replay" message, balance unchanged

This prevents double-deductions when the Order Service retries failed network calls.

### 3. Per-Wallet Mutex for Atomicity

Balance operations use a per-wallet mutex (`sync.Map` of `*sync.Mutex`) to ensure atomic read-check-write. This prevents race conditions where two concurrent deductions could both see sufficient balance and both deduct, causing a negative balance.

In a database-backed implementation, this would be replaced by `SELECT ... FOR UPDATE` or optimistic concurrency control (version column).

### 4. Append-Only Transaction Ledger

Every money movement (top-up or deduction) creates an immutable `Transaction` record. Transactions are never modified or deleted. This provides a full audit trail and enables balance reconciliation.

### 5. Storage Pluggability

Swapping to Redis/DynamoDB requires:
1. Create a new package `internal/storage/redis/` implementing the three interfaces
2. Change 3 constructor lines in `cmd/api/main.go`

**Zero changes** to service logic, handler logic, or tests.

## Data Model

```
Wallet
├── ID           (UUID, primary key)
├── CustomerName (string, required)
├── Balance      (int64, paise — never negative)
├── CreatedAt    (timestamp)
└── UpdatedAt    (timestamp)

Transaction (append-only ledger)
├── ID           (UUID, primary key)
├── WalletID     (UUID, foreign key → Wallet)
├── Amount       (int64, paise — always positive)
├── Type         (enum: CREDIT | DEBIT)
├── Description  (string)
├── ReferenceID  (string — order ID, topup ID, etc.)
└── CreatedAt    (timestamp)
```

## Running

### Start the Wallet Service

```bash
go run cmd/api/main.go
# Server starts on :8080 (override with PORT env var)
```

### Run the Order Service Stub

```bash
# In a separate terminal (Wallet Service must be running):
go run cmd/orderstub/main.go
```

The stub demonstrates: wallet creation → top-up → 5 orders → 6th order rejection → idempotency retry → transaction history.

### Run Tests

```bash
# All tests
go test ./... -v

# With coverage
go test ./... -coverprofile=coverage.out -covermode=atomic
go tool cover -func=coverage.out

# With race detector
go test ./... -race

# Integration tests only
go test ./tests/... -v
```

## Test Coverage

| Package | Coverage |
|---|---|
| `internal/domain` | 100.0% |
| `internal/service` | 100.0% |
| `internal/storage/memory` | 100.0% |
| `internal/handler` | 99.0% |

### Test Methodology

**Three levels of testing:**

1. **Unit tests (storage layer)** — Verify each in-memory store's CRUD operations, error handling, deep-copy semantics (external mutation doesn't corrupt internal state), and thread-safety.

2. **Unit tests with mocks (service & handler layers)** — Test business logic in isolation by injecting mock repositories that return specific errors. This covers every error-wrapping path, every domain error → HTTP status code mapping, and idempotency logic.

3. **Integration tests** — Spin up a real `httptest.Server` with fully wired dependencies and make actual HTTP requests. Test scenarios include:
   - Full order lifecycle (create → topup → 5 deductions → 6th rejected)
   - Idempotency (same key → single deduction)
   - Concurrent deductions (10 goroutines, only 5 succeed, balance = 0)
   - Wallet isolation (operations on wallet A don't affect wallet B)
   - Content-Type headers on all endpoints
   - 404 for all endpoints with nonexistent wallet ID

**Concurrency testing** — Uses Go's `-race` flag to verify no data races under concurrent deductions. The concurrent deduction test launches 10 goroutines against a ₹500 wallet, asserting exactly 5 succeed and the balance is exactly 0 (never negative).

## Trade-offs

| Decision | Trade-off |
|---|---|
| In-memory storage | Fast for dev/test but loses data on restart; pluggable to real DB |
| Per-wallet mutex | Simpler than MVCC but doesn't scale to multiple instances; DB would use row-level locks |
| Idempotency key in service layer | Tightly coupled to deduct; in production, could be middleware for any POST endpoint |
| No pagination on transactions | Fine for demo; production would need cursor-based pagination |
| No authentication/authorization | Out of scope; production would use JWT/API keys |
