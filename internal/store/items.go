package store

import "bookinapp/internal/model"

// Item types add the table keys and a "type" discriminator to the domain
// models. The model is embedded, so its attributes are stored flat next to
// the keys.

const (
	typeEvent   = "EVENT"
	typeSeat    = "SEAT"
	typeBooking = "BOOKING"
)

type eventItem struct {
	PK     string `dynamodbav:"PK"`
	SK     string `dynamodbav:"SK"`
	GSI1PK string `dynamodbav:"GSI1PK"`
	GSI1SK string `dynamodbav:"GSI1SK"`
	Type   string `dynamodbav:"type"`
	model.Event
}

type seatItem struct {
	PK   string `dynamodbav:"PK"`
	SK   string `dynamodbav:"SK"`
	Type string `dynamodbav:"type"`
	model.Seat
}

type bookingItem struct {
	PK     string `dynamodbav:"PK"`
	SK     string `dynamodbav:"SK"`
	GSI1PK string `dynamodbav:"GSI1PK"`
	GSI1SK string `dynamodbav:"GSI1SK"`
	Type   string `dynamodbav:"type"`
	model.Booking
}
