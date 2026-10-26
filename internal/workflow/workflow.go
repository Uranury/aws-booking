// Package workflow defines the state passed between the Step Functions steps
// and the error types the state machine's Retry and Catch rules match on.
package workflow

import (
	"encoding/json"

	"bookinapp/internal/model"
)

// State is the JSON document that flows through the state machine.
//
// POST /bookings fills in the top-level fields. Each step adds its result under
// its own key using ResultPath (validation, payment), and a Catch puts the error
// under "error". Every step Lambda takes the whole State as input.
type State struct {
	BookingID           string `json:"bookingId"`
	UserID              string `json:"userId"`
	EventID             string `json:"eventId"`
	SeatNumber          int    `json:"seatNumber"`
	CreatedAt           string `json:"createdAt"`
	ForcePaymentFailure bool   `json:"forcePaymentFailure"`

	Validation *ValidationResult `json:"validation,omitempty"`
	Payment    *PaymentResult    `json:"payment,omitempty"`
	Error      *CaughtError      `json:"error,omitempty"`
}

// ValidationResult is read by the Choice state after ValidateBooking.
type ValidationResult struct {
	Valid      bool   `json:"valid"`
	Reason     string `json:"reason,omitempty"`
	PriceCents int    `json:"priceCents"`
}

// PaymentResult is the output of ProcessPayment on success.
type PaymentResult struct {
	TransactionID string `json:"transactionId"`
}

// CaughtError is what Step Functions writes at ResultPath when a Catch fires.
// For a Lambda error, Cause is a JSON string containing errorMessage and errorType.
type CaughtError struct {
	Error string `json:"Error"`
	Cause string `json:"Cause"`
}

// BookingKey returns the base-table key of the booking this execution is for.
func (s State) BookingKey() model.BookingKey {
	return model.BookingKey{UserID: s.UserID, CreatedAt: s.CreatedAt, BookingID: s.BookingID}
}

// FailureReason turns whatever stopped the workflow into a short message for the user.
func (s State) FailureReason() string {
	if s.Validation != nil && !s.Validation.Valid {
		return s.Validation.Reason
	}
	if s.Error != nil {
		var cause struct {
			ErrorMessage string `json:"errorMessage"`
		}
		if json.Unmarshal([]byte(s.Error.Cause), &cause) == nil && cause.ErrorMessage != "" {
			return cause.ErrorMessage
		}
		if s.Error.Error != "" {
			return s.Error.Error
		}
	}
	return "booking could not be completed"
}

// Error types. aws-lambda-go reports the Go type name as the errorType, and
// Step Functions matches that name in Retry and Catch, so these names appear
// verbatim in template.yaml. Return them unwrapped from handlers.

// SeatUnavailable means another booking holds the seat. Not retried.
type SeatUnavailable struct{ Msg string }

func (e *SeatUnavailable) Error() string { return e.Msg }

// PaymentDeclined means the (mock) payment provider refused the charge. Not retried;
// it triggers the ReleaseSeat compensation.
type PaymentDeclined struct{ Msg string }

func (e *PaymentDeclined) Error() string { return e.Msg }

// TransientError wraps failures that may succeed on retry (throttling, network,
// unexpected DynamoDB errors). The state machine retries it with backoff.
type TransientError struct{ Msg string }

func (e *TransientError) Error() string { return e.Msg }

// Transient wraps err as a TransientError.
func Transient(err error) error {
	return &TransientError{Msg: err.Error()}
}
