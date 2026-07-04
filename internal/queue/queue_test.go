package queue

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

func TestConvertPayloadRoundTrip(t *testing.T) {
	id := uuid.New()
	task, err := NewImageConvertTask(id)
	if err != nil {
		t.Fatalf("NewImageConvertTask: %v", err)
	}
	if task.Type() != TypeImageConvert {
		t.Errorf("task type = %q, want %q", task.Type(), TypeImageConvert)
	}
	p, err := ParseConvertPayload(task.Payload())
	if err != nil {
		t.Fatalf("ParseConvertPayload: %v", err)
	}
	if p.JobID != id {
		t.Errorf("job id = %s, want %s", p.JobID, id)
	}
}

// TestWebhookRetryDelaySchedule asserts WebhookRetryDelay indexes
// webhookRetrySchedule with asynq's 0-based retry count (msg.Retried),
// within the ±25% jitter band, and clamps at the schedule's last entry.
func TestWebhookRetryDelaySchedule(t *testing.T) {
	schedule := []time.Duration{
		30 * time.Second,
		1 * time.Minute,
		2 * time.Minute,
		4 * time.Minute,
		8 * time.Minute,
		15 * time.Minute,
	}
	cases := []struct {
		n    int
		base time.Duration
	}{
		{n: 0, base: schedule[0]},
		{n: 1, base: schedule[1]},
		{n: 5, base: schedule[5]},
		{n: 6, base: schedule[5]},  // clamps past the end of the schedule
		{n: 100, base: schedule[5]},
	}
	for _, tc := range cases {
		delay := WebhookRetryDelay(tc.n, nil, nil)
		lo := time.Duration(float64(tc.base) * 0.75)
		hi := time.Duration(float64(tc.base) * 1.25)
		if delay < lo || delay > hi {
			t.Errorf("WebhookRetryDelay(%d) = %v, want within [%v, %v] (base %v)", tc.n, delay, lo, hi, tc.base)
		}
	}
}

// TestEnqueueImageConvert enqueues a task and confirms it lands in the image
// queue. Requires a live Redis (REDIS_ADDR); skipped otherwise.
func TestEnqueueImageConvert(t *testing.T) {
	if os.Getenv("REDIS_ADDR") == "" {
		t.Skip("REDIS_ADDR not set; skipping integration test")
	}

	cl, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer cl.Close()

	id := uuid.New()
	if err := cl.EnqueueImageConvert(context.Background(), id); err != nil {
		t.Fatalf("EnqueueImageConvert: %v", err)
	}

	opt, _ := RedisOpt()
	insp := asynq.NewInspector(opt)
	defer insp.Close()

	infos, err := insp.ListPendingTasks(QueueImage)
	if err != nil {
		t.Fatalf("ListPendingTasks: %v", err)
	}
	found := false
	for _, ti := range infos {
		p, err := ParseConvertPayload(ti.Payload)
		if err == nil && p.JobID == id {
			found = true
			if ti.Type != TypeImageConvert {
				t.Errorf("enqueued type = %q, want %q", ti.Type, TypeImageConvert)
			}
			// cleanup
			_ = insp.DeleteTask(QueueImage, ti.ID)
			break
		}
	}
	if !found {
		t.Errorf("enqueued task for job %s not found in queue %q", id, QueueImage)
	}
}
