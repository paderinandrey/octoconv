package queue

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// Client enqueues conversion tasks. Wraps an asynq.Client.
type Client struct {
	c *asynq.Client
}

// NewClient builds a queue client from REDIS_ADDR.
func NewClient() (*Client, error) {
	opt, err := RedisOpt()
	if err != nil {
		return nil, err
	}
	return &Client{c: asynq.NewClient(opt)}, nil
}

// Close releases the underlying Redis connections.
func (c *Client) Close() error { return c.c.Close() }

// EnqueueImageConvert puts an image conversion job onto the image queue.
func (c *Client) EnqueueImageConvert(ctx context.Context, jobID uuid.UUID) error {
	task, err := NewImageConvertTask(jobID)
	if err != nil {
		return err
	}
	if _, err := c.c.EnqueueContext(ctx, task); err != nil {
		return fmt.Errorf("enqueue image convert %s: %w", jobID, err)
	}
	return nil
}
