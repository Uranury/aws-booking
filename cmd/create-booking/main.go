// Command create-booking handles POST /api/bookings. It validates the request
// body, then runs the booking flow in internal/booking and returns the booking
// in its final state (CONFIRMED or FAILED).
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/google/uuid"

	"bookinapp/internal/booking"
	"bookinapp/internal/httpx"
	"bookinapp/internal/keys"
	"bookinapp/internal/model"
	"bookinapp/internal/payment"
	"bookinapp/internal/store"
)

const maxBodyBytes = 4 << 10

type request struct {
	UserID              string `json:"userId"`
	EventID             string `json:"eventId"`
	SeatNumber          int    `json:"seatNumber"`
	ForcePaymentFailure bool   `json:"forcePaymentFailure"`
}

func (r request) validate() error {
	switch {
	case !keys.ValidID(r.UserID):
		return errors.New("userId must be 1-64 characters of letters, digits, '-' or '_'")
	case !keys.ValidID(r.EventID):
		return errors.New("eventId must be 1-64 characters of letters, digits, '-' or '_'")
	case !keys.ValidSeatNumber(r.SeatNumber):
		return fmt.Errorf("seatNumber must be between 1 and %d", keys.MaxSeatNumber)
	}
	return nil
}

func handler(svc *booking.Service) func(context.Context, httpx.Request) (httpx.Response, error) {
	return func(ctx context.Context, req httpx.Request) (httpx.Response, error) {
		body := req.Body
		if req.IsBase64Encoded {
			raw, err := base64.StdEncoding.DecodeString(body)
			if err != nil {
				return httpx.Error(http.StatusBadRequest, "invalid body encoding"), nil
			}
			body = string(raw)
		}
		if len(body) > maxBodyBytes {
			return httpx.Error(http.StatusRequestEntityTooLarge, "request body too large"), nil
		}

		var in request
		dec := json.NewDecoder(strings.NewReader(body))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			return httpx.Error(http.StatusBadRequest, "body must be JSON: {userId, eventId, seatNumber, forcePaymentFailure?}"), nil
		}
		if err := in.validate(); err != nil {
			return httpx.Error(http.StatusBadRequest, err.Error()), nil
		}

		b, err := svc.Book(ctx, booking.Request{
			UserID:              in.UserID,
			EventID:             in.EventID,
			SeatNumber:          in.SeatNumber,
			ForcePaymentFailure: in.ForcePaymentFailure,
		})
		if err != nil {
			return httpx.ServerError(err), nil
		}
		// 201 for both outcomes: a booking record was created either way, and
		// its status says whether it succeeded.
		return httpx.JSON(http.StatusCreated, b), nil
	}
}

// snsNotifier publishes "booking confirmed" messages to an SNS topic.
type snsNotifier struct {
	client   *sns.Client
	topicArn string
}

func (n snsNotifier) BookingConfirmed(ctx context.Context, b model.Booking) error {
	_, err := n.client.Publish(ctx, &sns.PublishInput{
		TopicArn: aws.String(n.topicArn),
		Subject:  aws.String("Your booking is confirmed"),
		Message:  aws.String(fmt.Sprintf("Booking %s is confirmed: event %s, seat %d.", b.BookingID, b.EventID, b.SeatNumber)),
	})
	return err
}

func main() {
	ctx := context.Background()
	st, err := store.NewFromEnv(ctx)
	if err != nil {
		panic(err)
	}
	payments, err := payment.FromEnv()
	if err != nil {
		panic(err)
	}
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		panic(err)
	}
	topicArn := os.Getenv("TOPIC_ARN")
	if topicArn == "" {
		panic("TOPIC_ARN is not set")
	}

	svc := &booking.Service{
		Store:    st,
		Payments: payments,
		Notifier: snsNotifier{client: sns.NewFromConfig(cfg), topicArn: topicArn},
		Now:      time.Now,
		NewID:    uuid.NewString,
	}
	lambda.Start(handler(svc))
}
