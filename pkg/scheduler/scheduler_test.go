package scheduler

import (
	"os"
	"testing"
	"time"
)

func TestSchedulerAddAndCancel(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "sched_*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	s, err := NewScheduler(tmpFile.Name(), nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create scheduler: %v", err)
	}

	future := time.Now().Add(10 * time.Minute)
	job, err := s.Add("14155552671", "Reminder text", future)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	if job.Status != StatusPending || job.Recipient != "14155552671" {
		t.Fatalf("Unexpected job: %+v", job)
	}

	list := s.List()
	if len(list) != 1 {
		t.Fatalf("Expected 1 job, got %d", len(list))
	}

	// Cancel job
	if err := s.Cancel(job.ID); err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}

	list = s.List()
	if list[0].Status != StatusCancelled {
		t.Fatalf("Expected job to be cancelled, got %s", list[0].Status)
	}
}
