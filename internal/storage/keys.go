package storage

import (
	"fmt"
	"path"

	"github.com/google/uuid"
)

// InputKey builds the object key for an uploaded input of a job:
// uploads/{job_id}/{ordinal}-{filename}.
func InputKey(jobID uuid.UUID, ordinal int, filename string) string {
	return fmt.Sprintf("uploads/%s/%d-%s", jobID, ordinal, path.Base(filename))
}

// OutputKey builds the object key for a converted output of a job:
// results/{job_id}/{ordinal}-{filename}.
func OutputKey(jobID uuid.UUID, ordinal int, filename string) string {
	return fmt.Sprintf("results/%s/%d-%s", jobID, ordinal, path.Base(filename))
}
