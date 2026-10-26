// Command get-event handles GET /api/events/{id}: one event with every seat's status.
package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/aws/aws-lambda-go/lambda"

	"bookinapp/internal/httpx"
	"bookinapp/internal/keys"
	"bookinapp/internal/model"
	"bookinapp/internal/store"
)

type response struct {
	model.Event
	Available int          `json:"available"`
	Seats     []model.Seat `json:"seats"`
}

func handler(st *store.Store) func(context.Context, httpx.Request) (httpx.Response, error) {
	return func(ctx context.Context, req httpx.Request) (httpx.Response, error) {
		id := req.PathParameters["id"]
		if !keys.ValidID(id) {
			return httpx.Error(http.StatusBadRequest, "invalid event id"), nil
		}

		event, seats, err := st.GetEventWithSeats(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return httpx.Error(http.StatusNotFound, "event not found"), nil
		}
		if err != nil {
			return httpx.ServerError(err), nil
		}

		available := 0
		for _, s := range seats {
			if s.Status == model.SeatAvailable {
				available++
			}
		}
		return httpx.JSON(http.StatusOK, response{Event: event, Available: available, Seats: seats}), nil
	}
}

func main() {
	st, err := store.NewFromEnv(context.Background())
	if err != nil {
		panic(err)
	}
	lambda.Start(handler(st))
}
