package api

import "testing"

func TestMapIssueStatusClosedHasPriority(t *testing.T) {
	status := mapIssueStatus("closed", []string{"status/triage", "status/in-progress"})
	if status != "closed" {
		t.Fatalf("期望 closed，实际 %s", status)
	}
}

func TestMapIssueStatusFallsBackToInProgress(t *testing.T) {
	status := mapIssueStatus("open", []string{"type/bug"})
	if status != "in_progress" {
		t.Fatalf("期望 in_progress，实际 %s", status)
	}
}
