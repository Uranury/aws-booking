// Package booking runs the multi-step booking flow:
//
//	create PENDING booking -> validate -> reserve seat -> take payment -> confirm -> notify
//
// Any step after the seat is reserved can fail, so the flow compensates: it
// releases the seat and marks the booking FAILED. The steps run in order inside
// one request, and dependencies are interfaces so the flow can be unit tested.
package booking

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"bookinapp/internal/keys"
	"bookinapp/internal/model"
	"bookinapp/internal/payment"
	"bookinapp/internal/store"
)

// Store is the part of store.Store the flow needs.
type Store interface {
	GetEvent(ctx context.Context, eventID string) (model.Event, error)
	GetSeat(ctx context.Context, eventID string, seatNumber int) (model.Seat, error)
	CreateBooking(ctx context.Context, b model.Booking) error
	ReserveSeat(ctx context.Context, eventID string, seatNumber int, bookingID string) error
	ReleaseSeat(ctx context.Context, eventID string, seatNumber int, bookingID string) (bool, error)
	ConfirmBooking(ctx context.Context, key model.BookingKey, eventID string, seatNumber int, transactionID string) error
	MarkBookingFailed(ctx context.Context, key model.BookingKey, reason string) error
}

// Payments charges the customer.
type Payments interface {
	Charge(req payment.Request) (payment.Result, error)
}

// Notifier tells the user their booking is confirmed.
type Notifier interface {
	BookingConfirmed(ctx context.Context, b model.Booking) error
}

// Request is a validated booking request.
type Request struct {
	UserID              string
	EventID             string
	SeatNumber          int
	ForcePaymentFailure bool
}

// Service runs the booking flow.
type Service struct {
	Store    Store
	Payments Payments
	Notifier Notifier
	Now      func() time.Time
	NewID    func() string
}

// Book runs the whole flow and returns the booking in its final state,
// CONFIRMED or FAILED. A FAILED booking is a normal outcome, not an error.
// An error is returned only when the booking could not be recorded at all,
// or could not be marked FAILED after something went wrong.
func (s *Service) Book(ctx context.Context, req Request) (model.Booking, error) {
	now := keys.Timestamp(s.Now())
	b := model.Booking{
		BookingID:  s.NewID(),
		UserID:     req.UserID,
		EventID:    req.EventID,
		SeatNumber: req.SeatNumber,
		Status:     model.BookingPending,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	// Step 1: record the booking as PENDING before doing anything else, so
	// every attempt leaves a trace even if a later step fails.
	if err := s.Store.CreateBooking(ctx, b); err != nil {
		return model.Booking{}, fmt.Errorf("create booking: %w", err)
	}
	log := slog.With("bookingId", b.BookingID, "eventId", b.EventID, "seat", b.SeatNumber)

	// Step 2: validate against current data. This gives a clear message for
	// common problems, but it is not the guarantee: two requests can both pass
	// it, and the conditional write in step 3 decides who gets the seat.
	event, reason, err := s.validate(ctx, b)
	if err != nil {
		return s.fail(ctx, b, "internal error during validation", err)
	}
	if reason != "" {
		return s.fail(ctx, b, reason, nil)
	}

	// Step 3: reserve the seat with a conditional write (AVAILABLE -> RESERVED).
	err = s.Store.ReserveSeat(ctx, b.EventID, b.SeatNumber, b.BookingID)
	if errors.Is(err, store.ErrSeatUnavailable) {
		return s.fail(ctx, b, fmt.Sprintf("seat %d was taken by another booking", b.SeatNumber), nil)
	}
	if err != nil {
		// We don't know whether the write landed, so release to be safe.
		// Release only frees a seat this booking holds.
		return s.compensate(ctx, b, "internal error while reserving the seat", err)
	}
	log.Info("seat reserved")

	// Step 4: take payment. From here on, every failure must release the seat.
	res, err := s.Payments.Charge(payment.Request{
		BookingID:    b.BookingID,
		AmountCents:  event.PriceCents,
		ForceFailure: req.ForcePaymentFailure,
	})
	if errors.Is(err, payment.ErrDeclined) {
		return s.compensate(ctx, b, err.Error(), nil)
	}
	if err != nil {
		return s.compensate(ctx, b, "internal error while taking payment", err)
	}
	log.Info("payment succeeded", "transactionId", res.TransactionID)

	// Step 5: seat RESERVED -> BOOKED and booking PENDING -> CONFIRMED in one transaction.
	if err := s.Store.ConfirmBooking(ctx, b.Key(), b.EventID, b.SeatNumber, res.TransactionID); err != nil {
		return s.compensate(ctx, b, "internal error while confirming the booking", err)
	}
	b.Status = model.BookingConfirmed
	b.TransactionID = res.TransactionID
	b.UpdatedAt = keys.Timestamp(s.Now())
	log.Info("booking confirmed")

	// Step 6: notify. The booking is already confirmed, so a failed
	// notification is logged and does not undo it.
	if err := s.Notifier.BookingConfirmed(ctx, b); err != nil {
		log.Warn("notification failed", "err", err)
	}
	return b, nil
}

// validate returns a non-empty reason when the booking cannot go ahead.
func (s *Service) validate(ctx context.Context, b model.Booking) (model.Event, string, error) {
	event, err := s.Store.GetEvent(ctx, b.EventID)
	if errors.Is(err, store.ErrNotFound) {
		return model.Event{}, "event does not exist", nil
	}
	if err != nil {
		return model.Event{}, "", err
	}
	seat, err := s.Store.GetSeat(ctx, b.EventID, b.SeatNumber)
	if errors.Is(err, store.ErrNotFound) {
		return model.Event{}, fmt.Sprintf("seat %d does not exist", b.SeatNumber), nil
	}
	if err != nil {
		return model.Event{}, "", err
	}
	if seat.Status != model.SeatAvailable {
		return model.Event{}, fmt.Sprintf("seat %d is no longer available", b.SeatNumber), nil
	}
	return event, "", nil
}

// compensate undoes the seat reservation, then marks the booking FAILED.
func (s *Service) compensate(ctx context.Context, b model.Booking, reason string, cause error) (model.Booking, error) {
	released, err := s.Store.ReleaseSeat(ctx, b.EventID, b.SeatNumber, b.BookingID)
	if err != nil {
		// The seat stays RESERVED under this booking id until someone frees
		// it by hand. Still mark the booking FAILED so the user gets an answer.
		slog.Error("release seat failed", "bookingId", b.BookingID, "err", err)
	} else {
		slog.Info("seat released", "bookingId", b.BookingID, "released", released)
	}
	return s.fail(ctx, b, reason, cause)
}

// fail marks the booking FAILED with a reason the user can read. cause is the
// underlying error, if any; it is logged but not shown to the user.
func (s *Service) fail(ctx context.Context, b model.Booking, reason string, cause error) (model.Booking, error) {
	if cause != nil {
		slog.Error("booking step failed", "bookingId", b.BookingID, "reason", reason, "err", cause)
	}
	if err := s.Store.MarkBookingFailed(ctx, b.Key(), reason); err != nil {
		return model.Booking{}, fmt.Errorf("mark booking %s failed: %w", b.BookingID, err)
	}
	b.Status = model.BookingFailed
	b.FailureReason = reason
	b.UpdatedAt = keys.Timestamp(s.Now())
	slog.Info("booking failed", "bookingId", b.BookingID, "reason", reason)
	return b, nil
}
