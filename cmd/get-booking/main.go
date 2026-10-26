// Command get-booking handles GET /api/bookings/{id}. The frontend polls it
// until the status is CONFIRMED or FAILED.
package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/aws/aws-lambda-go/lambda"

	"bookinapp/internal/httpx"
	"bookinapp/internal/keys"
	"bookinapp/internal/store"
)

func handler(st *store.Store) func(context.Context, httpx.Request) (httpx.Response, error) {
	return func(ctx context.Context, req httpx.Request) (httpx.Response, error) {
		id := req.PathParameters["id"]
		if !keys.ValidID(id) {
			return httpx.Error(http.StatusBadRequest, "invalid booking id"), nil
		}
		booking, err := st.GetBooking(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return httpx.Error(http.StatusNotFound, "booking not found"), nil
		}
		if err != nil {
			return httpx.ServerError(err), nil
		}
		return httpx.JSON(http.StatusOK, booking), nil
	}
}

func main() {
	st, err := store.NewFromEnv(context.Background())
	if err != nil {
		panic(err)
	}
	lambda.Start(handler(st))
}
