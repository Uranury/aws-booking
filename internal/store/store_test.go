package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"bookinapp/internal/model"
)

// fakeSeatDB stands in for DynamoDB's UpdateItem on seat items. Like DynamoDB,
// it evaluates the condition and applies the write as one atomic step (here,
// under a mutex), and rejects the write with ConditionalCheckFailedException
// when the condition is false. It understands the two seat conditions the
// store sends. Other DynamoAPI methods are not used by these tests.
type fakeSeatDB struct {
	DynamoAPI // nil; calling any other method panics

	mu    sync.Mutex
	seats map[string]model.Seat // key: PK|SK
	calls []*dynamodb.UpdateItemInput
}

func newFakeSeatDB(seat model.Seat, pk, sk string) *fakeSeatDB {
	return &fakeSeatDB{seats: map[string]model.Seat{pk + "|" + sk: seat}}
}

func (f *fakeSeatDB) UpdateItem(_ context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, in)

	key := attr(in.Key, "PK") + "|" + attr(in.Key, "SK")
	seat, exists := f.seats[key]
	bookingID := attr(in.ExpressionAttributeValues, ":bookingId")

	var ok bool
	switch aws.ToString(in.ConditionExpression) {
	case condReserveSeat:
		ok = exists && (seat.Status == model.SeatAvailable ||
			(seat.Status == model.SeatReserved && seat.BookingID == bookingID))
		if ok {
			seat.Status, seat.BookingID = model.SeatReserved, bookingID
		}
	case condReleaseSeat:
		ok = exists && seat.Status == model.SeatReserved && seat.BookingID == bookingID
		if ok {
			seat.Status, seat.BookingID = model.SeatAvailable, ""
		}
	default:
		return nil, fmt.Errorf("fake: unexpected condition %q", aws.ToString(in.ConditionExpression))
	}
	if !ok {
		return nil, &types.ConditionalCheckFailedException{Message: aws.String("The conditional request failed")}
	}
	f.seats[key] = seat
	return &dynamodb.UpdateItemOutput{}, nil
}

func (f *fakeSeatDB) seat(pk, sk string) model.Seat {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seats[pk+"|"+sk]
}

func attr(m map[string]types.AttributeValue, name string) string {
	if s, ok := m[name].(*types.AttributeValueMemberS); ok {
		return s.Value
	}
	return ""
}

const (
	testPK = "EVENT#jazz-night"
	testSK = "SEAT#005"
)

func availableSeat() model.Seat {
	return model.Seat{EventID: "jazz-night", SeatNumber: 5, Status: model.SeatAvailable}
}

func TestReserveSeatSendsConditionalWrite(t *testing.T) {
	db := newFakeSeatDB(availableSeat(), testPK, testSK)
	if err := New(db, "tbl").ReserveSeat(context.Background(), "jazz-night", 5, "b1"); err != nil {
		t.Fatalf("ReserveSeat: %v", err)
	}
	in := db.calls[0]
	if got := attr(in.Key, "SK"); got != testSK {
		t.Errorf("key SK = %q, want %q", got, testSK)
	}
	if aws.ToString(in.ConditionExpression) != condReserveSeat {
		t.Errorf("condition = %q, want the reserve condition", aws.ToString(in.ConditionExpression))
	}
	if s := db.seat(testPK, testSK); s.Status != model.SeatReserved || s.BookingID != "b1" {
		t.Errorf("seat = %+v, want RESERVED by b1", s)
	}
}

func TestReserveSeatTakenByAnotherBooking(t *testing.T) {
	db := newFakeSeatDB(availableSeat(), testPK, testSK)
	st := New(db, "tbl")
	ctx := context.Background()

	if err := st.ReserveSeat(ctx, "jazz-night", 5, "b1"); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if err := st.ReserveSeat(ctx, "jazz-night", 5, "b2"); !errors.Is(err, ErrSeatUnavailable) {
		t.Fatalf("second reserve err = %v, want ErrSeatUnavailable", err)
	}
	// Retrying for the booking that already holds the seat is not an error.
	if err := st.ReserveSeat(ctx, "jazz-night", 5, "b1"); err != nil {
		t.Fatalf("repeat reserve by holder: %v", err)
	}
}

// Many requests race for one seat; exactly one may win.
func TestReserveSeatConcurrentRequests(t *testing.T) {
	db := newFakeSeatDB(availableSeat(), testPK, testSK)
	st := New(db, "tbl")

	const n = 50
	var wins, losses atomic.Int32
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := st.ReserveSeat(context.Background(), "jazz-night", 5, fmt.Sprintf("b%d", i))
			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, ErrSeatUnavailable):
				losses.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 || losses.Load() != n-1 {
		t.Fatalf("wins=%d losses=%d, want 1 and %d", wins.Load(), losses.Load(), n-1)
	}
}

func TestReleaseSeatOnlyByHolder(t *testing.T) {
	db := newFakeSeatDB(availableSeat(), testPK, testSK)
	st := New(db, "tbl")
	ctx := context.Background()
	if err := st.ReserveSeat(ctx, "jazz-night", 5, "b1"); err != nil {
		t.Fatal(err)
	}

	// Another booking's compensation must not free b1's seat.
	released, err := st.ReleaseSeat(ctx, "jazz-night", 5, "b2")
	if err != nil || released {
		t.Fatalf("release by b2 = %v, %v; want false, nil", released, err)
	}
	if s := db.seat(testPK, testSK); s.Status != model.SeatReserved {
		t.Fatalf("seat = %+v, want still RESERVED", s)
	}

	released, err = st.ReleaseSeat(ctx, "jazz-night", 5, "b1")
	if err != nil || !released {
		t.Fatalf("release by b1 = %v, %v; want true, nil", released, err)
	}
	if s := db.seat(testPK, testSK); s.Status != model.SeatAvailable || s.BookingID != "" {
		t.Fatalf("seat = %+v, want AVAILABLE with no booking", s)
	}

	// Releasing again is a harmless no-op.
	if released, err = st.ReleaseSeat(ctx, "jazz-night", 5, "b1"); err != nil || released {
		t.Fatalf("second release = %v, %v; want false, nil", released, err)
	}
}

func TestReserveSeatPassesThroughOtherErrors(t *testing.T) {
	st := New(errDB{}, "tbl")
	err := st.ReserveSeat(context.Background(), "jazz-night", 5, "b1")
	if err == nil || errors.Is(err, ErrSeatUnavailable) {
		t.Fatalf("err = %v, want a non-ErrSeatUnavailable error", err)
	}
}

// errDB fails every UpdateItem, like a throttled or unreachable table.
type errDB struct{ DynamoAPI }

func (errDB) UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	return nil, errors.New("throttled")
}
