// Command get-events handles GET /api/events: the list of upcoming events.
package main

import (
	"context"
	"net/http"

	"github.com/aws/aws-lambda-go/lambda"

	"bookinapp/internal/httpx"
	"bookinapp/internal/store"
)

func handler(st *store.Store) func(context.Context, httpx.Request) (httpx.Response, error) {
	return func(ctx context.Context, _ httpx.Request) (httpx.Response, error) {
		events, err := st.ListEvents(ctx)
		if err != nil {
			return httpx.ServerError(err), nil
		}
		return httpx.JSON(http.StatusOK, map[string]any{"events": events}), nil
	}
}

func main() {
	st, err := store.NewFromEnv(context.Background())
	if err != nil {
		panic(err)
	}
	lambda.Start(handler(st))
}
