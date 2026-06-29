package queue

import (
	"context"
	"os"
	"testing"

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
