package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"bookinapp/internal/keys"
	"bookinapp/internal/model"
)

// ListEvents returns all events ordered by start time.
// Access pattern: Query GSI1 where GSI1PK = "EVENTS". Seats never appear in
// GSI1, so this reads only event items (a Scan would read every seat too).
func (s *Store) ListEvents(ctx context.Context) ([]model.Event, error) {
	items, err := s.queryAll(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.table),
		IndexName:              aws.String(keys.GSI1Name),
		KeyConditionExpression: aws.String("GSI1PK = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": &types.AttributeValueMemberS{Value: keys.EventsGSI1PK},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	events := make([]model.Event, 0, len(items))
	if err := attributevalue.UnmarshalListOfMaps(items, &events); err != nil {
		return nil, fmt.Errorf("decode events: %w", err)
	}
	return events, nil
}

// GetEventWithSeats returns an event and all of its seats in seat order.
// Access pattern: one Query on PK = EVENT#<id>, which returns the META item
// and every SEAT# item together.
func (s *Store) GetEventWithSeats(ctx context.Context, eventID string) (model.Event, []model.Seat, error) {
	items, err := s.queryAll(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.table),
		KeyConditionExpression: aws.String("PK = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": &types.AttributeValueMemberS{Value: keys.EventPK(eventID)},
		},
		// Strongly consistent so the seat grid reflects bookings that just happened.
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return model.Event{}, nil, fmt.Errorf("query event %s: %w", eventID, err)
	}

	var (
		event    model.Event
		hasEvent bool
		seats    []model.Seat
	)
	for _, item := range items {
		sk, _ := item["SK"].(*types.AttributeValueMemberS)
		if sk == nil {
			continue
		}
		switch {
		case sk.Value == keys.EventMetaSK:
			if err := attributevalue.UnmarshalMap(item, &event); err != nil {
				return model.Event{}, nil, fmt.Errorf("decode event: %w", err)
			}
			hasEvent = true
		case strings.HasPrefix(sk.Value, keys.SeatSKPrefix()):
			var seat model.Seat
			if err := attributevalue.UnmarshalMap(item, &seat); err != nil {
				return model.Event{}, nil, fmt.Errorf("decode seat: %w", err)
			}
			seats = append(seats, seat)
		}
	}
	if !hasEvent {
		return model.Event{}, nil, ErrNotFound
	}
	return event, seats, nil
}

// GetEvent returns the META item of one event.
func (s *Store) GetEvent(ctx context.Context, eventID string) (model.Event, error) {
	var event model.Event
	err := s.getItem(ctx, keys.EventPK(eventID), keys.EventMetaSK, &event)
	return event, err
}

// GetSeat returns one seat.
func (s *Store) GetSeat(ctx context.Context, eventID string, seatNumber int) (model.Seat, error) {
	var seat model.Seat
	err := s.getItem(ctx, keys.EventPK(eventID), keys.SeatSK(seatNumber), &seat)
	return seat, err
}

// PutEventWithSeats writes an event and seats 1..SeatCount, all AVAILABLE.
// Used by the seed Lambda. Writing an existing event again resets its seats.
func (s *Store) PutEventWithSeats(ctx context.Context, event model.Event) error {
	requests := make([]types.WriteRequest, 0, event.SeatCount+1)

	meta, err := attributevalue.MarshalMap(eventItem{
		PK:     keys.EventPK(event.EventID),
		SK:     keys.EventMetaSK,
		GSI1PK: keys.EventsGSI1PK,
		GSI1SK: keys.EventGSI1SK(event.StartsAt, event.EventID),
		Type:   typeEvent,
		Event:  event,
	})
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}
	requests = append(requests, types.WriteRequest{PutRequest: &types.PutRequest{Item: meta}})

	for n := 1; n <= event.SeatCount; n++ {
		seat, err := attributevalue.MarshalMap(seatItem{
			PK:   keys.EventPK(event.EventID),
			SK:   keys.SeatSK(n),
			Type: typeSeat,
			Seat: model.Seat{EventID: event.EventID, SeatNumber: n, Status: model.SeatAvailable},
		})
		if err != nil {
			return fmt.Errorf("encode seat: %w", err)
		}
		requests = append(requests, types.WriteRequest{PutRequest: &types.PutRequest{Item: seat}})
	}
	return s.batchWrite(ctx, requests)
}

// batchWrite sends requests in chunks of 25 (the BatchWriteItem limit) and
// resends any items DynamoDB reports as unprocessed.
func (s *Store) batchWrite(ctx context.Context, requests []types.WriteRequest) error {
	const maxBatch = 25
	for start := 0; start < len(requests); start += maxBatch {
		end := min(start+maxBatch, len(requests))
		pending := map[string][]types.WriteRequest{s.table: requests[start:end]}
		for attempt := 0; len(pending) > 0; attempt++ {
			if attempt == 5 {
				return fmt.Errorf("batch write: items still unprocessed after %d attempts", attempt)
			}
			if attempt > 0 {
				time.Sleep(time.Duration(100<<attempt) * time.Millisecond)
			}
			out, err := s.db.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{RequestItems: pending})
			if err != nil {
				return fmt.Errorf("batch write: %w", err)
			}
			pending = out.UnprocessedItems
		}
	}
	return nil
}

func (s *Store) getItem(ctx context.Context, pk, sk string, out any) error {
	res, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.table),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk},
			"SK": &types.AttributeValueMemberS{Value: sk},
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("get %s/%s: %w", pk, sk, err)
	}
	if len(res.Item) == 0 {
		return ErrNotFound
	}
	if err := attributevalue.UnmarshalMap(res.Item, out); err != nil {
		return fmt.Errorf("decode %s/%s: %w", pk, sk, err)
	}
	return nil
}

func (s *Store) queryAll(ctx context.Context, in *dynamodb.QueryInput) ([]map[string]types.AttributeValue, error) {
	var items []map[string]types.AttributeValue
	for {
		out, err := s.db.Query(ctx, in)
		if err != nil {
			return nil, err
		}
		items = append(items, out.Items...)
		if len(out.LastEvaluatedKey) == 0 {
			return items, nil
		}
		in.ExclusiveStartKey = out.LastEvaluatedKey
	}
}
