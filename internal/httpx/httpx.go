// Package httpx has small helpers for API Gateway HTTP API (payload v2) responses.
package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/aws/aws-lambda-go/events"
)

// Request and Response are the HTTP API v2 Lambda event types.
type (
	Request  = events.APIGatewayV2HTTPRequest
	Response = events.APIGatewayV2HTTPResponse
)

// JSON returns body encoded as JSON with the given status code.
func JSON(status int, body any) Response {
	b, err := json.Marshal(body)
	if err != nil {
		slog.Error("encode response", "err", err)
		return Response{StatusCode: http.StatusInternalServerError, Body: `{"error":"internal error"}`, Headers: headers()}
	}
	return Response{StatusCode: status, Body: string(b), Headers: headers()}
}

// Error returns {"error": msg} with the given status code.
func Error(status int, msg string) Response {
	return JSON(status, map[string]string{"error": msg})
}

// ServerError logs err and returns a generic 500, so internal details never
// reach the client.
func ServerError(err error) Response {
	slog.Error("request failed", "err", err)
	return Error(http.StatusInternalServerError, "internal error")
}

func headers() map[string]string {
	return map[string]string{
		"Content-Type": "application/json",
		// Responses change as seats are booked; never let a browser or proxy cache them.
		"Cache-Control": "no-store",
	}
}
