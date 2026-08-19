package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRupeesToPaise(t *testing.T) {
	tests := []struct {
		name    string
		rupees  float64
		want    int64
	}{
		{"zero", 0, 0},
		{"one rupee", 1.0, 100},
		{"hundred rupees", 100.0, 10000},
		{"fractional rupees", 99.50, 9950},
		{"small amount", 0.01, 1},
		{"large amount", 10000.00, 1000000},
		// Floating-point edge cases — these values produce non-exact
		// IEEE 754 representations (e.g., 19.99 * 100 = 1998.9999...).
		// Without math.Round, int64 truncation loses a paisa.
		{"float edge 19.99", 19.99, 1999},
		{"float edge 0.29", 0.29, 29},
		{"float edge 1.01", 1.01, 101},
		{"float edge 999.99", 999.99, 99999},
		{"float edge 49.99", 49.99, 4999},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RupeesToPaise(tt.rupees)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPaiseToRupees(t *testing.T) {
	tests := []struct {
		name  string
		paise int64
		want  float64
	}{
		{"zero", 0, 0.0},
		{"one hundred paise", 100, 1.0},
		{"ten thousand paise", 10000, 100.0},
		{"one paise", 1, 0.01},
		{"large amount", 1000000, 10000.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PaiseToRupees(tt.paise)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTransactionType_Constants(t *testing.T) {
	assert.Equal(t, TransactionType("CREDIT"), TransactionTypeCredit)
	assert.Equal(t, TransactionType("DEBIT"), TransactionTypeDebit)
}
