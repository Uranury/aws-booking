package keys

import (
	"sort"
	"testing"
	"time"
)

func TestKeyFormats(t *testing.T) {
	tests := []struct{ got, want string }{
		{EventPK("jazz-night"), "EVENT#jazz-night"},
		{SeatSK(7), "SEAT#007"},
		{SeatSK(999), "SEAT#999"},
		{UserPK("u1"), "USER#u1"},
		{BookingSK("2026-10-27T18:00:00.000Z", "b1"), "BOOKING#2026-10-27T18:00:00.000Z#b1"},
		{BookingGSI1PK("b1"), "BOOKING#b1"},
		{EventGSI1SK("2026-11-03T20:00:00.000Z", "jazz-night"), "2026-11-03T20:00:00.000Z#jazz-night"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("got %q, want %q", tt.got, tt.want)
		}
	}
}

// DynamoDB compares sort keys as strings, so seat 2 must sort before seat 10.
func TestSeatKeysSortNumerically(t *testing.T) {
	got := []string{SeatSK(10), SeatSK(2), SeatSK(100), SeatSK(1)}
	sort.Strings(got)
	want := []string{"SEAT#001", "SEAT#002", "SEAT#010", "SEAT#100"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sorted seat keys = %v, want %v", got, want)
		}
	}
}

// Timestamps must be fixed width so that string order equals time order.
// RFC3339Nano would drop trailing zeros and break this.
func TestTimestampIsFixedWidthAndSortable(t *testing.T) {
	base := time.Date(2026, 10, 27, 18, 0, 5, 0, time.UTC)
	a := Timestamp(base.Add(100 * time.Millisecond)) // .100
	b := Timestamp(base.Add(120 * time.Millisecond)) // .120
	if len(a) != len(TimestampLayout) || len(b) != len(TimestampLayout) {
		t.Fatalf("timestamps not fixed width: %q %q", a, b)
	}
	if !(a < b) {
		t.Fatalf("expected %q < %q", a, b)
	}
	if got := Timestamp(base.In(time.FixedZone("CET", 3600))); got != "2026-10-27T18:00:05.000Z" {
		t.Fatalf("timestamp not converted to UTC: %q", got)
	}
}

func TestValidID(t *testing.T) {
	for _, id := range []string{"jazz-night", "user_42", "550e8400-e29b-41d4-a716-446655440000"} {
		if !ValidID(id) {
			t.Errorf("ValidID(%q) = false, want true", id)
		}
	}
	// "#" would let a caller forge a key for a different item.
	for _, id := range []string{"", "a#SEAT#001", "has space", "ünïcode", string(make([]byte, 65))} {
		if ValidID(id) {
			t.Errorf("ValidID(%q) = true, want false", id)
		}
	}
}

func TestValidSeatNumber(t *testing.T) {
	for n, want := range map[int]bool{0: false, 1: true, 999: true, 1000: false, -1: false} {
		if got := ValidSeatNumber(n); got != want {
			t.Errorf("ValidSeatNumber(%d) = %v, want %v", n, got, want)
		}
	}
}
