package booking

import (
	"context"
	"errors"
	"testing"
	"time"

	"bookinapp/internal/model"
	"bookinapp/internal/payment"
	"bookinapp/internal/store"
)

// memStore is an in-memory Store with the same state rules as the DynamoDB
// conditions in the store package.
type memStore struct {
	event    model.Event
	seats    map[int]model.Seat
	bookings map[string]model.Booking
}

func newMemStore() *memStore {
	return &memStore{
		event: model.Event{EventID: "jazz-night", PriceCents: 2500, SeatCount: 2},
		seats: map[int]model.Seat{
			1: {EventID: "jazz-night", SeatNumber: 1, Status: model.SeatAvailable},
			2: {EventID: "jazz-night", SeatNumber: 2, Status: model.SeatAvailable},
		},
		bookings: map[string]model.Booking{},
	}
}

func (m *memStore) GetEvent(_ context.Context, id string) (model.Event, error) {
	if id != m.event.EventID {
		return model.Event{}, store.ErrNotFound
	}
	return m.event, nil
}

func (m *memStore) GetSeat(_ context.Context, _ string, n int) (model.Seat, error) {
	s, ok := m.seats[n]
	if !ok {
		return model.Seat{}, store.ErrNotFound
	}
	return s, nil
}

func (m *memStore) CreateBooking(_ context.Context, b model.Booking) error {
	m.bookings[b.BookingID] = b
	return nil
}

func (m *memStore) ReserveSeat(_ context.Context, _ string, n int, id string) error {
	s := m.seats[n]
	if s.Status != model.SeatAvailable {
		return store.ErrSeatUnavailable
	}
	s.Status, s.BookingID = model.SeatReserved, id
	m.seats[n] = s
	return nil
}

func (m *memStore) ReleaseSeat(_ context.Context, _ string, n int, id string) (bool, error) {
	s := m.seats[n]
	if s.Status != model.SeatReserved || s.BookingID != id {
		return false, nil
	}
	s.Status, s.BookingID = model.SeatAvailable, ""
	m.seats[n] = s
	return true, nil
}

func (m *memStore) ConfirmBooking(_ context.Context, k model.BookingKey, _ string, n int, txn string) error {
	s, b := m.seats[n], m.bookings[k.BookingID]
	if s.BookingID != k.BookingID || b.Status != model.BookingPending {
		return store.ErrConflict
	}
	s.Status = model.SeatBooked
	b.Status, b.TransactionID = model.BookingConfirmed, txn
	m.seats[n], m.bookings[k.BookingID] = s, b
	return nil
}

func (m *memStore) MarkBookingFailed(_ context.Context, k model.BookingKey, reason string) error {
	b := m.bookings[k.BookingID]
	b.Status, b.FailureReason = model.BookingFailed, reason
	m.bookings[k.BookingID] = b
	return nil
}

type fakeNotifier struct {
	err  error
	sent []string
}

func (n *fakeNotifier) BookingConfirmed(_ context.Context, b model.Booking) error {
	n.sent = append(n.sent, b.BookingID)
	return n.err
}

func newService(st *memStore, failureRate float64, n *fakeNotifier) *Service {
	ids := []string{"b1", "b2", "b3"}
	return &Service{
		Store:    st,
		Payments: payment.Processor{FailureRate: failureRate, Rand: func() float64 { return 0.5 }},
		Notifier: n,
		Now:      func() time.Time { return time.Date(2026, 10, 27, 18, 0, 0, 0, time.UTC) },
		NewID: func() string {
			id := ids[0]
			ids = ids[1:]
			return id
		},
	}
}

func TestBookSuccess(t *testing.T) {
	st, n := newMemStore(), &fakeNotifier{}
	b, err := newService(st, 0, n).Book(context.Background(), Request{UserID: "u1", EventID: "jazz-night", SeatNumber: 1})
	if err != nil {
		t.Fatal(err)
	}
	if b.Status != model.BookingConfirmed || st.bookings["b1"].Status != model.BookingConfirmed {
		t.Errorf("booking status = %s (stored %s), want CONFIRMED", b.Status, st.bookings["b1"].Status)
	}
	if st.seats[1].Status != model.SeatBooked {
		t.Errorf("seat status = %s, want BOOKED", st.seats[1].Status)
	}
	if len(n.sent) != 1 {
		t.Errorf("notifications sent = %d, want 1", len(n.sent))
	}
}

// The compensation path: a declined payment must free the seat again.
func TestBookPaymentDeclinedReleasesSeat(t *testing.T) {
	st, n := newMemStore(), &fakeNotifier{}
	b, err := newService(st, 0, n).Book(context.Background(),
		Request{UserID: "u1", EventID: "jazz-night", SeatNumber: 1, ForcePaymentFailure: true})
	if err != nil {
		t.Fatal(err)
	}
	if b.Status != model.BookingFailed || st.bookings["b1"].Status != model.BookingFailed {
		t.Errorf("booking status = %s (stored %s), want FAILED", b.Status, st.bookings["b1"].Status)
	}
	if b.FailureReason == "" {
		t.Error("expected a failure reason")
	}
	if s := st.seats[1]; s.Status != model.SeatAvailable || s.BookingID != "" {
		t.Errorf("seat = %+v, want AVAILABLE again", s)
	}
	if len(n.sent) != 0 {
		t.Error("no notification expected for a failed booking")
	}
}

func TestBookSeatAlreadyTaken(t *testing.T) {
	st := newMemStore()
	svc := newService(st, 0, &fakeNotifier{})
	ctx := context.Background()
	req := Request{UserID: "u1", EventID: "jazz-night", SeatNumber: 2}

	if first, _ := svc.Book(ctx, req); first.Status != model.BookingConfirmed {
		t.Fatalf("first booking = %s, want CONFIRMED", first.Status)
	}
	second, err := svc.Book(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != model.BookingFailed {
		t.Errorf("second booking = %s, want FAILED", second.Status)
	}
	if s := st.seats[2]; s.Status != model.SeatBooked || s.BookingID != "b1" {
		t.Errorf("seat = %+v, want still BOOKED by b1", s)
	}
}

func TestBookUnknownEventOrSeat(t *testing.T) {
	for _, req := range []Request{
		{UserID: "u1", EventID: "nope", SeatNumber: 1},
		{UserID: "u1", EventID: "jazz-night", SeatNumber: 99},
	} {
		st := newMemStore()
		b, err := newService(st, 0, &fakeNotifier{}).Book(context.Background(), req)
		if err != nil || b.Status != model.BookingFailed {
			t.Errorf("Book(%+v) = %s, %v; want FAILED", req, b.Status, err)
		}
	}
}

// The booking is already confirmed when notifying, so a failed notification
// must not undo it.
func TestBookNotificationFailureKeepsBooking(t *testing.T) {
	st := newMemStore()
	b, err := newService(st, 0, &fakeNotifier{err: errors.New("sns down")}).
		Book(context.Background(), Request{UserID: "u1", EventID: "jazz-night", SeatNumber: 1})
	if err != nil || b.Status != model.BookingConfirmed {
		t.Fatalf("Book = %s, %v; want CONFIRMED", b.Status, err)
	}
}
