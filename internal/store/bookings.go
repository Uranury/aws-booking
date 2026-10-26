package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"bookinapp/internal/keys"
	"bookinapp/internal/model"
)

// Condition expressions for the seat and booking state transitions. Each write
// states which state it expects to find, so DynamoDB rejects it atomically if
// another request got there first. "status" is a reserved word, hence #status.
const (
	// Take the seat if it is free. The second clause makes a retried
	// ReserveSeat for the same booking succeed instead of failing on itself.
	condReserveSeat = "attribute_exists(PK) AND (#status = :available OR (#status = :reserved AND bookingId = :bookingId))"

	// Only the booking that holds the seat can release it, and only while it is
	// still RESERVED, so compensation can never free a seat someone else booked.
	condReleaseSeat = "#status = :reserved AND bookingId = :bookingId"

	// Confirm only our own hold. BOOKED is accepted so a retried confirm is a no-op.
	condConfirmSeat = "bookingId = :bookingId AND #status IN (:reserved, :booked)"

	condBookingNotExists = "attribute_not_exists(PK)"
	condConfirmBooking   = "#status IN (:pending, :confirmed)"
	condFailBooking      = "#status IN (:pending, :failed)"
)

var statusName = map[string]string{"#status": "status"}

// CreateBooking stores a new booking. The caller sets status PENDING.
func (s *Store) CreateBooking(ctx context.Context, b model.Booking) error {
	item, err := attributevalue.MarshalMap(bookingItem{
		PK:      keys.UserPK(b.UserID),
		SK:      keys.BookingSK(b.CreatedAt, b.BookingID),
		GSI1PK:  keys.BookingGSI1PK(b.BookingID),
		GSI1SK:  keys.BookingGSI1SK,
		Type:    typeBooking,
		Booking: b,
	})
	if err != nil {
		return fmt.Errorf("encode booking: %w", err)
	}
	_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(s.table),
		Item:                item,
		ConditionExpression: aws.String(condBookingNotExists),
	})
	if isConditionFailed(err) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("put booking %s: %w", b.BookingID, err)
	}
	return nil
}

// GetBooking looks a booking up by id alone.
// Access pattern: Query GSI1 where GSI1PK = BOOKING#<id>. GSI reads are
// eventually consistent, so a booking created a moment ago may briefly be
// missing; the frontend treats that as "still pending".
func (s *Store) GetBooking(ctx context.Context, bookingID string) (model.Booking, error) {
	out, err := s.db.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.table),
		IndexName:              aws.String(keys.GSI1Name),
		KeyConditionExpression: aws.String("GSI1PK = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": &types.AttributeValueMemberS{Value: keys.BookingGSI1PK(bookingID)},
		},
		Limit: aws.Int32(1),
	})
	if err != nil {
		return model.Booking{}, fmt.Errorf("query booking %s: %w", bookingID, err)
	}
	if len(out.Items) == 0 {
		return model.Booking{}, ErrNotFound
	}
	var b model.Booking
	if err := attributevalue.UnmarshalMap(out.Items[0], &b); err != nil {
		return model.Booking{}, fmt.Errorf("decode booking: %w", err)
	}
	return b, nil
}

// ReserveSeat moves a seat from AVAILABLE to RESERVED for bookingID in one
// conditional write. If two requests race for the same seat, DynamoDB applies
// one and rejects the other with ConditionalCheckFailedException, which is
// returned as ErrSeatUnavailable.
func (s *Store) ReserveSeat(ctx context.Context, eventID string, seatNumber int, bookingID string) error {
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                aws.String(s.table),
		Key:                      seatKey(eventID, seatNumber),
		UpdateExpression:         aws.String("SET #status = :reserved, bookingId = :bookingId, updatedAt = :now"),
		ConditionExpression:      aws.String(condReserveSeat),
		ExpressionAttributeNames: statusName,
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":available": str(string(model.SeatAvailable)),
			":reserved":  str(string(model.SeatReserved)),
			":bookingId": str(bookingID),
			":now":       str(s.timestamp()),
		},
	})
	if isConditionFailed(err) {
		return ErrSeatUnavailable
	}
	if err != nil {
		return fmt.Errorf("reserve seat %s/%d: %w", eventID, seatNumber, err)
	}
	return nil
}

// ReleaseSeat is the compensation for ReserveSeat: it moves the seat back to
// AVAILABLE if, and only if, bookingID still holds it. It returns false when
// there was nothing to release, which is not an error. That makes it safe to
// retry and safe to run when the reservation never happened.
func (s *Store) ReleaseSeat(ctx context.Context, eventID string, seatNumber int, bookingID string) (bool, error) {
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                aws.String(s.table),
		Key:                      seatKey(eventID, seatNumber),
		UpdateExpression:         aws.String("SET #status = :available, updatedAt = :now REMOVE bookingId"),
		ConditionExpression:      aws.String(condReleaseSeat),
		ExpressionAttributeNames: statusName,
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":available": str(string(model.SeatAvailable)),
			":reserved":  str(string(model.SeatReserved)),
			":bookingId": str(bookingID),
			":now":       str(s.timestamp()),
		},
	})
	if isConditionFailed(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("release seat %s/%d: %w", eventID, seatNumber, err)
	}
	return true, nil
}

// ConfirmBooking marks the seat BOOKED and the booking CONFIRMED in a single
// transaction, so there is never a confirmed booking without a booked seat
// (or the other way round).
func (s *Store) ConfirmBooking(ctx context.Context, key model.BookingKey, eventID string, seatNumber int, transactionID string) error {
	now := str(s.timestamp())
	_, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{Update: &types.Update{
				TableName:                aws.String(s.table),
				Key:                      seatKey(eventID, seatNumber),
				UpdateExpression:         aws.String("SET #status = :booked, updatedAt = :now"),
				ConditionExpression:      aws.String(condConfirmSeat),
				ExpressionAttributeNames: statusName,
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":reserved":  str(string(model.SeatReserved)),
					":booked":    str(string(model.SeatBooked)),
					":bookingId": str(key.BookingID),
					":now":       now,
				},
			}},
			{Update: &types.Update{
				TableName:                aws.String(s.table),
				Key:                      bookingKey(key),
				UpdateExpression:         aws.String("SET #status = :confirmed, transactionId = :txn, updatedAt = :now"),
				ConditionExpression:      aws.String(condConfirmBooking),
				ExpressionAttributeNames: statusName,
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":pending":   str(string(model.BookingPending)),
					":confirmed": str(string(model.BookingConfirmed)),
					":txn":       str(transactionID),
					":now":       now,
				},
			}},
		},
	})
	if isTransactionConditionFailed(err) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("confirm booking %s: %w", key.BookingID, err)
	}
	return nil
}

// MarkBookingFailed sets the booking to FAILED with a reason. It never
// overwrites a CONFIRMED booking.
func (s *Store) MarkBookingFailed(ctx context.Context, key model.BookingKey, reason string) error {
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                aws.String(s.table),
		Key:                      bookingKey(key),
		UpdateExpression:         aws.String("SET #status = :failed, failureReason = :reason, updatedAt = :now"),
		ConditionExpression:      aws.String(condFailBooking),
		ExpressionAttributeNames: statusName,
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pending": str(string(model.BookingPending)),
			":failed":  str(string(model.BookingFailed)),
			":reason":  str(reason),
			":now":     str(s.timestamp()),
		},
	})
	if isConditionFailed(err) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("mark booking %s failed: %w", key.BookingID, err)
	}
	return nil
}

func seatKey(eventID string, seatNumber int) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"PK": str(keys.EventPK(eventID)),
		"SK": str(keys.SeatSK(seatNumber)),
	}
}

func bookingKey(k model.BookingKey) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"PK": str(keys.UserPK(k.UserID)),
		"SK": str(keys.BookingSK(k.CreatedAt, k.BookingID)),
	}
}

func str(v string) types.AttributeValue {
	return &types.AttributeValueMemberS{Value: v}
}

func isConditionFailed(err error) bool {
	var ccf *types.ConditionalCheckFailedException
	return errors.As(err, &ccf)
}

func isTransactionConditionFailed(err error) bool {
	var tce *types.TransactionCanceledException
	if !errors.As(err, &tce) {
		return false
	}
	for _, r := range tce.CancellationReasons {
		if aws.ToString(r.Code) == "ConditionalCheckFailed" {
			return true
		}
	}
	return false
}
