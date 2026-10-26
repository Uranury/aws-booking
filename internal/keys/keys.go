// Package keys builds the partition and sort keys for the single DynamoDB table.
//
// Keeping every key format in one place means the access patterns are easy to
// read, and a typo in a prefix cannot silently create a second "kind" of item.
//
//	PK              SK                          item
//	EVENT#<id>      META                        event details
//	EVENT#<id>      SEAT#<nnn>                  one seat
//	USER#<id>       BOOKING#<timestamp>#<id>    one booking
//
// GSI1 (sparse, overloaded):
//
//	GSI1PK          GSI1SK                      used for
//	EVENTS          <startsAt>#<id>             listing events by date
//	BOOKING#<id>    BOOKING                     fetching a booking by id
package keys

import (
	"fmt"
	"time"
)

const (
	EventMetaSK   = "META"
	EventsGSI1PK  = "EVENTS"
	BookingGSI1SK = "BOOKING"
	GSI1Name      = "GSI1"
	MaxSeatNumber = 999

	seatPrefix    = "SEAT#"
	bookingPrefix = "BOOKING#"
	eventPrefix   = "EVENT#"
	userPrefix    = "USER#"
)

// TimestampLayout is a fixed-width UTC timestamp with millisecond precision.
// Fixed width matters: sort keys compare as strings, so "...05.1Z" would sort
// after "...05.10Z" with a variable-width format like RFC3339Nano.
const TimestampLayout = "2006-01-02T15:04:05.000Z"

// Timestamp formats t for use in keys and stored attributes.
func Timestamp(t time.Time) string {
	return t.UTC().Format(TimestampLayout)
}

// EventPK is the partition key shared by an event and all of its seats, so one
// Query on it returns the event page in a single request.
func EventPK(eventID string) string {
	return eventPrefix + eventID
}

// SeatSK zero-pads the seat number so seats sort numerically (SEAT#002 < SEAT#010).
func SeatSK(seatNumber int) string {
	return fmt.Sprintf("%s%03d", seatPrefix, seatNumber)
}

// SeatSKPrefix is used with begins_with to select only the seats of an event.
func SeatSKPrefix() string {
	return seatPrefix
}

// UserPK groups all bookings made by one user.
func UserPK(userID string) string {
	return userPrefix + userID
}

// BookingSK puts the creation time first so a user's bookings come back in
// chronological order; the booking id on the end keeps the key unique.
func BookingSK(createdAt, bookingID string) string {
	return bookingPrefix + createdAt + "#" + bookingID
}

// BookingGSI1PK lets GET /bookings/{id} find a booking without knowing the user
// or the creation time.
func BookingGSI1PK(bookingID string) string {
	return bookingPrefix + bookingID
}

// EventGSI1SK sorts the events list by start time.
func EventGSI1SK(startsAt, eventID string) string {
	return startsAt + "#" + eventID
}

// ValidID reports whether id is safe to embed in a key: 1-64 characters from
// [A-Za-z0-9_-]. This rejects "#", so a caller cannot forge a key such as
// "x#SEAT#001" that would address a different item.
func ValidID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// ValidSeatNumber reports whether n fits the three-digit seat key format.
func ValidSeatNumber(n int) bool {
	return n >= 1 && n <= MaxSeatNumber
}
