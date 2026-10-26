// Command seed is a Lambda that loads a few sample events with seats.
// It has no API route; invoke it once after deploying (see `make seed`).
// Running it again resets the sample events' seats to AVAILABLE, which is
// handy between demo runs.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-lambda-go/lambda"

	"bookinapp/internal/keys"
	"bookinapp/internal/model"
	"bookinapp/internal/store"
)

type result struct {
	Events int `json:"events"`
	Seats  int `json:"seats"`
}

// sampleEvents returns events dated relative to now so the list always shows
// upcoming events, whenever the seed is run.
func sampleEvents(now time.Time) []model.Event {
	at := func(days, hour int) string {
		d := now.UTC().AddDate(0, 0, days)
		return keys.Timestamp(time.Date(d.Year(), d.Month(), d.Day(), hour, 0, 0, 0, time.UTC))
	}
	return []model.Event{
		{EventID: "jazz-night", Name: "Jazz Night", Venue: "Blue Room", StartsAt: at(7, 20), PriceCents: 2500, SeatCount: 24},
		{EventID: "cloud-meetup", Name: "Serverless Meetup", Venue: "Lecture Hall B", StartsAt: at(14, 18), PriceCents: 0, SeatCount: 32},
		{EventID: "film-classic", Name: "Classic Film Screening", Venue: "Campus Cinema", StartsAt: at(21, 19), PriceCents: 800, SeatCount: 16},
	}
}

func handler(st *store.Store) func(ctx context.Context) (result, error) {
	return func(ctx context.Context) (result, error) {
		var res result
		for _, ev := range sampleEvents(time.Now()) {
			if err := st.PutEventWithSeats(ctx, ev); err != nil {
				return res, fmt.Errorf("seed %s: %w", ev.EventID, err)
			}
			slog.Info("seeded event", "eventId", ev.EventID, "seats", ev.SeatCount)
			res.Events++
			res.Seats += ev.SeatCount
		}
		return res, nil
	}
}

func main() {
	st, err := store.NewFromEnv(context.Background())
	if err != nil {
		panic(err)
	}
	lambda.Start(handler(st))
}
