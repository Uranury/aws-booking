package payment

import (
	"errors"
	"testing"
)

func fixed(v float64) func() float64 { return func() float64 { return v } }

func TestCharge(t *testing.T) {
	tests := []struct {
		name        string
		rate, roll  float64
		force       bool
		wantDecline bool
	}{
		{"rate 0 always succeeds", 0, 0, false, false},
		{"rate 1 always declines", 1, 0.999, false, true},
		{"roll below rate declines", 0.3, 0.29, false, true},
		{"roll at or above rate succeeds", 0.3, 0.3, false, false},
		{"forced failure declines even at rate 0", 0, 0.99, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Processor{FailureRate: tt.rate, Rand: fixed(tt.roll)}
			res, err := p.Charge(Request{BookingID: "b1", AmountCents: 2500, ForceFailure: tt.force})
			if tt.wantDecline {
				if !errors.Is(err, ErrDeclined) {
					t.Fatalf("err = %v, want ErrDeclined", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Same booking, same transaction id: a retried charge is recognisable.
			if res.TransactionID != "txn-b1" {
				t.Fatalf("transaction id = %q, want txn-b1", res.TransactionID)
			}
		})
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("PAYMENT_FAILURE_RATE", "0.25")
	p, err := FromEnv()
	if err != nil || p.FailureRate != 0.25 {
		t.Fatalf("FromEnv() = %v, %v; want rate 0.25", p.FailureRate, err)
	}

	for _, bad := range []string{"abc", "-0.1", "1.5"} {
		t.Setenv("PAYMENT_FAILURE_RATE", bad)
		if _, err := FromEnv(); err == nil {
			t.Errorf("FromEnv() with %q: expected error", bad)
		}
	}
}
