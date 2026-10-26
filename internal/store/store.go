// Package store is the only code that talks to DynamoDB. Handlers call these
// methods and never build keys or expressions themselves.
package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"bookinapp/internal/keys"
)

// DynamoAPI is the subset of the DynamoDB client the store uses. Depending on
// this interface instead of *dynamodb.Client lets tests substitute a fake.
type DynamoAPI interface {
	GetItem(ctx context.Context, in *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, in *dynamodb.PutItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	Query(ctx context.Context, in *dynamodb.QueryInput, opts ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	TransactWriteItems(ctx context.Context, in *dynamodb.TransactWriteItemsInput, opts ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
	BatchWriteItem(ctx context.Context, in *dynamodb.BatchWriteItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error)
}

var (
	// ErrNotFound means the requested item does not exist.
	ErrNotFound = errors.New("not found")
	// ErrSeatUnavailable means the conditional reservation failed: the seat is
	// held or booked by another booking (or does not exist).
	ErrSeatUnavailable = errors.New("seat is not available")
	// ErrConflict means a state transition was rejected because the item was not
	// in the expected state (for example confirming a booking that already FAILED).
	ErrConflict = errors.New("item is not in the expected state")
)

// Store reads and writes the application table.
type Store struct {
	db    DynamoAPI
	table string
	now   func() time.Time
}

// New returns a Store for the given client and table name.
func New(db DynamoAPI, table string) *Store {
	return &Store{db: db, table: table, now: time.Now}
}

// NewFromEnv builds a Store from the Lambda environment (AWS credentials and
// region from the runtime, table name from TABLE_NAME).
func NewFromEnv(ctx context.Context) (*Store, error) {
	table := os.Getenv("TABLE_NAME")
	if table == "" {
		return nil, errors.New("TABLE_NAME is not set")
	}
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	return New(dynamodb.NewFromConfig(cfg), table), nil
}

func (s *Store) timestamp() string {
	return keys.Timestamp(s.now())
}
