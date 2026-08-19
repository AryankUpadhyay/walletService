package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// NewRouter creates and configures the chi router with all wallet endpoints.
// It takes a WalletHandler and returns a fully configured http.Handler.
func NewRouter(h *WalletHandler) *chi.Mux {
	r := chi.NewRouter()

	// Middleware stack
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(JSONContentType)

	// Health check endpoint
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Wallet routes
	r.Route("/wallets", func(r chi.Router) {
		r.Post("/create-id", h.CreateWallet)

		r.Route("/{id}", func(r chi.Router) {
			r.Post("/topup", h.TopUp)
			r.Post("/deduct", h.Deduct)
			r.Get("/balance", h.GetBalance)
			r.Get("/transactions", h.GetTransactions)
		})
	})

	return r
}
