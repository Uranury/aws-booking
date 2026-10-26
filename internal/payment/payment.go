// Package payment is a mock payment provider. It declines a configurable
// fraction of charges at random, and always declines when the request asks it
// to, so a failed payment can be demonstrated on demand.
package payment

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
)

// ErrDeclined is returned when the charge is refused.
var ErrDeclined = errors.New("payment declined")

// Request is one charge.
type Request struct {
	BookingID    string
	AmountCents  int
	ForceFailure bool
}

// Result is a successful charge.
type Result struct {
	TransactionID string
}

// Processor decides whether a charge succeeds.
type Processor struct {
	// FailureRate is the probability of a random decline, from 0 (never) to 1 (always).
	FailureRate float64
	// Rand returns a number in [0, 1). Tests replace it to make outcomes deterministic.
	Rand func() float64
}

// FromEnv reads PAYMENT_FAILURE_RATE (default 0).
func FromEnv() (Processor, error) {
	rate := 0.0
	if v := os.Getenv("PAYMENT_FAILURE_RATE"); v != "" {
		var err error
		if rate, err = strconv.ParseFloat(v, 64); err != nil {
			return Processor{}, fmt.Errorf("PAYMENT_FAILURE_RATE %q: %w", v, err)
		}
	}
	if rate < 0 || rate > 1 {
		return Processor{}, fmt.Errorf("PAYMENT_FAILURE_RATE must be between 0 and 1, got %v", rate)
	}
	return Processor{FailureRate: rate, Rand: rand.Float64}, nil
}

// Charge simulates charging the customer.
//
// The transaction id is derived from the booking id. A real provider would be
// given the booking id as an idempotency key, so a retried step cannot charge
// the customer twice; deriving the id mirrors that.
func (p Processor) Charge(req Request) (Result, error) {
	if req.ForceFailure {
		return Result{}, fmt.Errorf("%w: card declined (forced failure for demo)", ErrDeclined)
	}
	if p.Rand() < p.FailureRate {
		return Result{}, fmt.Errorf("%w: card declined by mock provider", ErrDeclined)
	}
	return Result{TransactionID: "txn-" + req.BookingID}, nil
}
