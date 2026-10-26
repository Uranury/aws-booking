// Package model holds the domain types shared by the API and workflow Lambdas.
//
// The json tags define the API responses; the dynamodbav tags define the stored
// attributes. Table keys (PK, SK, GSI1PK, GSI1SK) are added by the store package
// and never leak into API responses.
package model

// SeatStatus is the lifecycle of a seat:
// AVAILABLE -> RESERVED (held during payment) -> BOOKED, or back to AVAILABLE on failure.
type SeatStatus string

const (
	SeatAvailable SeatStatus = "AVAILABLE"
	SeatReserved  SeatStatus = "RESERVED"
	SeatBooked    SeatStatus = "BOOKED"
)

// BookingStatus is the lifecycle of a booking: PENDING -> CONFIRMED or FAILED.
type BookingStatus string

const (
	BookingPending   BookingStatus = "PENDING"
	BookingConfirmed BookingStatus = "CONFIRMED"
	BookingFailed    BookingStatus = "FAILED"
)

// Event is the META item of an event partition.
type Event struct {
	EventID    string `json:"eventId" dynamodbav:"eventId"`
	Name       string `json:"name" dynamodbav:"name"`
	Venue      string `json:"venue" dynamodbav:"venue"`
	StartsAt   string `json:"startsAt" dynamodbav:"startsAt"`
	PriceCents int    `json:"priceCents" dynamodbav:"priceCents"`
	SeatCount  int    `json:"seatCount" dynamodbav:"seatCount"`
}

// Seat is one SEAT# item. BookingID is set while the seat is RESERVED or BOOKED;
// it is not exposed through the API.
type Seat struct {
	EventID    string     `json:"-" dynamodbav:"eventId"`
	SeatNumber int        `json:"seatNumber" dynamodbav:"seatNumber"`
	Status     SeatStatus `json:"status" dynamodbav:"status"`
	BookingID  string     `json:"-" dynamodbav:"bookingId,omitempty"`
}

// Booking is one BOOKING# item in a user's partition.
type Booking struct {
	BookingID     string        `json:"bookingId" dynamodbav:"bookingId"`
	UserID        string        `json:"userId" dynamodbav:"userId"`
	EventID       string        `json:"eventId" dynamodbav:"eventId"`
	SeatNumber    int           `json:"seatNumber" dynamodbav:"seatNumber"`
	Status        BookingStatus `json:"status" dynamodbav:"status"`
	CreatedAt     string        `json:"createdAt" dynamodbav:"createdAt"`
	UpdatedAt     string        `json:"updatedAt" dynamodbav:"updatedAt"`
	FailureReason string        `json:"failureReason,omitempty" dynamodbav:"failureReason,omitempty"`
	TransactionID string        `json:"transactionId,omitempty" dynamodbav:"transactionId,omitempty"`
}

// BookingKey is everything needed to address a booking item in the base table.
// The booking flow writes through this direct key, never through the
// (eventually consistent) GSI.
type BookingKey struct {
	UserID    string
	CreatedAt string
	BookingID string
}

// Key returns the base-table key of b.
func (b Booking) Key() BookingKey {
	return BookingKey{UserID: b.UserID, CreatedAt: b.CreatedAt, BookingID: b.BookingID}
}
