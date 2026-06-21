package contracttest

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func RunRecurrenceSuite(t *testing.T, factory StoreFactory) {
	t.Helper()

	t.Run("RecurrenceRuleRoundTripInTransaction", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := context.Background()
		task := &model.Task{
			Title:   "recurring daily review",
			Content: "review PRs",
			Status:  "open",
			Horizon: "week",
			Scope:   "daily",
		}

		var rule *model.RecurrenceRule
		err := store.Transact(ctx, func(txStore storage.Store) error {
			if err := txStore.Tasks().Create(ctx, task); err != nil {
				return err
			}
			return txStore.Recurrence().UpsertRule(ctx, &model.RecurrenceRule{
				TaskID:    task.ID,
				StartDate: "2026-06-21",
				Frequency: "daily",
				Interval:  1,
				Weekdays:  []int{1, 2, 3, 4, 5},
				Timezone:  "Asia/Shanghai",
				Enabled:   true,
			})
		})
		if err != nil {
			t.Fatalf("transact create task + rule: %v", err)
		}
		if task.ID == "" {
			t.Fatal("expected task ID to be populated after create")
		}

		rule, err = store.Recurrence().GetRule(ctx, task.ID)
		if err != nil {
			t.Fatalf("get rule: %v", err)
		}
		if rule.TaskID != task.ID {
			t.Fatalf("expected rule task_id %q, got %q", task.ID, rule.TaskID)
		}
		if rule.Frequency != "daily" {
			t.Fatalf("expected frequency daily, got %q", rule.Frequency)
		}
		if rule.StartDate != "2026-06-21" {
			t.Fatalf("expected start_date 2026-06-21, got %q", rule.StartDate)
		}
		if rule.Interval != 1 {
			t.Fatalf("expected interval 1, got %d", rule.Interval)
		}
		if len(rule.Weekdays) != 5 || rule.Weekdays[0] != 1 {
			t.Fatalf("expected weekdays [1,2,3,4,5], got %v", rule.Weekdays)
		}
		if rule.Timezone != "Asia/Shanghai" {
			t.Fatalf("expected timezone Asia/Shanghai, got %q", rule.Timezone)
		}
		if !rule.Enabled {
			t.Fatal("expected rule to be enabled")
		}
		if rule.CreatedAt == 0 || rule.UpdatedAt == 0 {
			t.Fatal("expected created_at and updated_at to be set")
		}

		// Verify the rule is visible in ListActiveRules
		rules, err := store.Recurrence().ListActiveRules(ctx, "2026-06-20", "2026-06-22")
		if err != nil {
			t.Fatalf("list active rules: %v", err)
		}
		found := false
		for _, r := range rules {
			if r.TaskID == task.ID {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("expected rule to appear in ListActiveRules")
		}

		// EndDate filter: rules with end_date before range start should not appear
		endDate := "2026-06-19"
		rule.EndDate = &endDate
		if err := store.Recurrence().UpsertRule(ctx, rule); err != nil {
			t.Fatalf("update rule end_date: %v", err)
		}
		rulesAfterEnd, err := store.Recurrence().ListActiveRules(ctx, "2026-06-20", "2026-06-22")
		if err != nil {
			t.Fatalf("list active rules after end: %v", err)
		}
		for _, r := range rulesAfterEnd {
			if r.TaskID == task.ID {
				t.Fatal("expected rule with end_date before range to not appear")
			}
		}
	})

	t.Run("OccurrenceCompleteReopenSkipUpsert", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := context.Background()
		task := &model.Task{
			Title:   "upsert occurrence test",
			Content: "test complete/reopen/skip",
			Status:  "open",
			Horizon: "week",
			Scope:   "daily",
		}
		if err := store.Tasks().Create(ctx, task); err != nil {
			t.Fatalf("create task: %v", err)
		}

		date := "2026-06-21"
		completedAt := time.Date(2026, 6, 21, 10, 0, 0, 0, time.Local).Unix()

		// Complete the occurrence
		complete, err := store.Recurrence().CompleteOccurrence(ctx, task.ID, date, completedAt)
		if err != nil {
			t.Fatalf("complete occurrence: %v", err)
		}
		if complete.Status != "done" {
			t.Fatalf("expected status done, got %q", complete.Status)
		}
		if complete.CompletedAt == nil || *complete.CompletedAt != completedAt {
			t.Fatalf("expected completed_at %d, got %v", completedAt, complete.CompletedAt)
		}

		// Reopen the occurrence (upsert open over done)
		reopen, err := store.Recurrence().ReopenOccurrence(ctx, task.ID, date)
		if err != nil {
			t.Fatalf("reopen occurrence: %v", err)
		}
		if reopen.Status != "open" {
			t.Fatalf("expected status open after reopen, got %q", reopen.Status)
		}
		if reopen.CompletedAt != nil {
			t.Fatalf("expected completed_at nil after reopen, got %v", reopen.CompletedAt)
		}

		// Skip the occurrence (upsert skipped over open)
		skip, err := store.Recurrence().SkipOccurrence(ctx, task.ID, date)
		if err != nil {
			t.Fatalf("skip occurrence: %v", err)
		}
		if skip.Status != "skipped" {
			t.Fatalf("expected status skipped, got %q", skip.Status)
		}
		if skip.CompletedAt != nil {
			t.Fatalf("expected completed_at nil for skipped, got %v", skip.CompletedAt)
		}

		// Complete again (upsert done over skipped) — verify we can go back to done
		completedAt2 := time.Date(2026, 6, 21, 14, 0, 0, 0, time.Local).Unix()
		completeAgain, err := store.Recurrence().CompleteOccurrence(ctx, task.ID, date, completedAt2)
		if err != nil {
			t.Fatalf("complete occurrence again: %v", err)
		}
		if completeAgain.Status != "done" {
			t.Fatalf("expected status done after re-complete, got %q", completeAgain.Status)
		}
		if completeAgain.CompletedAt == nil || *completeAgain.CompletedAt != completedAt2 {
			t.Fatalf("expected completed_at %d, got %v", completedAt2, completeAgain.CompletedAt)
		}
	})

	t.Run("ListOccurrencesMergesTaskProjectMetadata", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := context.Background()
		project, err := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{
			Name: "Health Project",
			Type: "regular",
		})
		if err != nil {
			t.Fatalf("create project: %v", err)
		}

		task := &model.Task{
			Title:     "morning exercise",
			Content:   "30 min workout",
			ProjectID: &project.ID,
			Status:    "open",
			Horizon:   "week",
			Scope:     "daily",
		}
		if err := store.Tasks().Create(ctx, task); err != nil {
			t.Fatalf("create task: %v", err)
		}

		date := "2026-06-21"
		completedAt := time.Date(2026, 6, 21, 8, 0, 0, 0, time.Local).Unix()
		if _, err := store.Recurrence().CompleteOccurrence(ctx, task.ID, date, completedAt); err != nil {
			t.Fatalf("complete occurrence: %v", err)
		}

		occurrences, err := store.Recurrence().ListOccurrences(ctx, "2026-06-20", "2026-06-22")
		if err != nil {
			t.Fatalf("list occurrences: %v", err)
		}

		var found *model.TaskOccurrence
		for i := range occurrences {
			if occurrences[i].TaskID == task.ID {
				found = &occurrences[i]
				break
			}
		}
		if found == nil {
			t.Fatalf("expected occurrence for task %q in list", task.ID)
		}

		// Verify task-level metadata is merged
		if found.Title != "morning exercise" {
			t.Fatalf("expected title 'morning exercise', got %q", found.Title)
		}
		if found.Content != "30 min workout" {
			t.Fatalf("expected content '30 min workout', got %q", found.Content)
		}
		if found.ProjectID == nil || *found.ProjectID != project.ID {
			t.Fatalf("expected project_id %q, got %v", project.ID, found.ProjectID)
		}
		if found.Project != "Health Project" {
			t.Fatalf("expected project name 'Health Project', got %q", found.Project)
		}

		// Verify occurrence-level data
		if found.OccurrenceDate != date {
			t.Fatalf("expected occurrence_date %q, got %q", date, found.OccurrenceDate)
		}
		if found.Status != "done" {
			t.Fatalf("expected status done, got %q", found.Status)
		}
	})

	t.Run("DeleteTaskCascadesToRuleAndOccurrences", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := context.Background()
		task := &model.Task{
			Title:   "task to delete",
			Content: "will be cascaded",
			Status:  "open",
			Horizon: "week",
			Scope:   "daily",
		}
		if err := store.Tasks().Create(ctx, task); err != nil {
			t.Fatalf("create task: %v", err)
		}

		// Create a rule
		rule := &model.RecurrenceRule{
			TaskID:    task.ID,
			StartDate: "2026-06-21",
			Frequency: "weekly",
			Interval:  1,
			Weekdays:  []int{1},
			Timezone:  "UTC",
			Enabled:   true,
		}
		if err := store.Recurrence().UpsertRule(ctx, rule); err != nil {
			t.Fatalf("upsert rule: %v", err)
		}

		// Create an occurrence
		date := "2026-06-21"
		completedAt := time.Now().Unix()
		if _, err := store.Recurrence().CompleteOccurrence(ctx, task.ID, date, completedAt); err != nil {
			t.Fatalf("complete occurrence: %v", err)
		}

		// Verify they exist before delete
		if _, err := store.Recurrence().GetRule(ctx, task.ID); err != nil {
			t.Fatalf("get rule before delete: %v", err)
		}
		count, err := store.Recurrence().CountOccurrencesByTask(ctx, task.ID)
		if err != nil {
			t.Fatalf("count occurrences before delete: %v", err)
		}
		if count != 1 {
			t.Fatalf("expected 1 occurrence before delete, got %d", count)
		}

		// Delete the task
		if err := store.Tasks().Delete(ctx, task.ID); err != nil {
			t.Fatalf("delete task: %v", err)
		}

		// Rule should be gone
		if _, err := store.Recurrence().GetRule(ctx, task.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected sql.ErrNoRows for deleted rule, got %v", err)
		}

		// Occurrences should be gone
		countAfter, err := store.Recurrence().CountOccurrencesByTask(ctx, task.ID)
		if err != nil {
			t.Fatalf("count occurrences after delete: %v", err)
		}
		if countAfter != 0 {
			t.Fatalf("expected 0 occurrences after cascade delete, got %d", countAfter)
		}
	})

	t.Run("GetCompletedOccurrencesByRangeReturnsCorrectSummaries", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := context.Background()
		project, err := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{
			Name: "Summary Project",
			Type: "learning",
		})
		if err != nil {
			t.Fatalf("create project: %v", err)
		}

		task := &model.Task{
			Title:     "write daily summary",
			Content:   "summarize today's learning",
			ProjectID: &project.ID,
			Status:    "open",
			Horizon:   "week",
			Scope:     "daily",
		}
		if err := store.Tasks().Create(ctx, task); err != nil {
			t.Fatalf("create task: %v", err)
		}

		// Create completed occurrences at two different timestamps
		completed1 := time.Date(2026, 6, 21, 9, 0, 0, 0, time.Local).Unix()
		completed2 := time.Date(2026, 6, 22, 15, 0, 0, 0, time.Local).Unix()
		completed3 := time.Date(2026, 6, 20, 12, 0, 0, 0, time.Local).Unix() // out of range

		if _, err := store.Recurrence().CompleteOccurrence(ctx, task.ID, "2026-06-21", completed1); err != nil {
			t.Fatalf("complete occurrence 1: %v", err)
		}
		if _, err := store.Recurrence().CompleteOccurrence(ctx, task.ID, "2026-06-22", completed2); err != nil {
			t.Fatalf("complete occurrence 2: %v", err)
		}
		if _, err := store.Recurrence().CompleteOccurrence(ctx, task.ID, "2026-06-20", completed3); err != nil {
			t.Fatalf("complete occurrence 3: %v", err)
		}

		// Query for range that includes only completed1 and completed2
		from := time.Date(2026, 6, 21, 0, 0, 0, 0, time.Local).Unix()
		to := time.Date(2026, 6, 23, 0, 0, 0, 0, time.Local).Unix()

		summaries, err := store.Recurrence().GetCompletedOccurrencesByRange(ctx, from, to)
		if err != nil {
			t.Fatalf("get completed occurrences: %v", err)
		}

		// Should have exactly 2 in-range completed occurrences
		if len(summaries) != 2 {
			t.Fatalf("expected 2 summaries, got %d: %+v", len(summaries), summaries)
		}

		// Verify summary structure
		for _, s := range summaries {
			if s.ID != task.ID {
				t.Fatalf("expected summary task_id %q, got %q", task.ID, s.ID)
			}
			if s.Title != "write daily summary" {
				t.Fatalf("expected title 'write daily summary', got %q", s.Title)
			}
			if s.Done != 1 {
				t.Fatalf("expected Done=1 for completed occurrence, got %d", s.Done)
			}
			if s.ExecutionType != "recurring" {
				t.Fatalf("expected ExecutionType 'recurring', got %q", s.ExecutionType)
			}
			if s.CompletedAt == nil {
				t.Fatal("expected CompletedAt to be set")
			}
			if s.Project == nil || s.Project.Name != "Summary Project" {
				t.Fatalf("expected project 'Summary Project', got %+v", s.Project)
			}
			if s.OccurrenceDate == "" {
				t.Fatal("expected OccurrenceDate to be set")
			}
		}

		// Verify ordering: most recent completed_at first (DESC)
		if summaries[0].OccurrenceDate == "2026-06-22" && summaries[1].OccurrenceDate == "2026-06-21" {
			// correct order (descending by completed_at)
		} else if summaries[0].OccurrenceDate == "2026-06-21" && summaries[1].OccurrenceDate == "2026-06-22" {
			// Also acceptable — just verify the timestamps are decreasing
			if summaries[0].CompletedAt != nil && summaries[1].CompletedAt != nil {
				if *summaries[0].CompletedAt < *summaries[1].CompletedAt {
					t.Fatalf("expected summaries ordered by completed_at DESC, got %+v", summaries)
				}
			}
		}
	})

	t.Run("CountOccurrencesByTaskReturnsCorrectCount", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := context.Background()
		task := &model.Task{
			Title:   "count test task",
			Content: "testing count",
			Status:  "open",
			Horizon: "week",
			Scope:   "daily",
		}
		if err := store.Tasks().Create(ctx, task); err != nil {
			t.Fatalf("create task: %v", err)
		}

		// Initially zero
		count, err := store.Recurrence().CountOccurrencesByTask(ctx, task.ID)
		if err != nil {
			t.Fatalf("count occurrences: %v", err)
		}
		if count != 0 {
			t.Fatalf("expected 0 occurrences initially, got %d", count)
		}

		completedAt := time.Now().Unix()
		dates := []string{"2026-06-21", "2026-06-22", "2026-06-23"}
		for _, date := range dates {
			if _, err := store.Recurrence().CompleteOccurrence(ctx, task.ID, date, completedAt); err != nil {
				t.Fatalf("complete occurrence %s: %v", date, err)
			}
		}

		count, err = store.Recurrence().CountOccurrencesByTask(ctx, task.ID)
		if err != nil {
			t.Fatalf("count occurrences after inserts: %v", err)
		}
		if count != 3 {
			t.Fatalf("expected 3 occurrences, got %d", count)
		}

		// Skip one — count should remain the same (upsert, not insert)
		if _, err := store.Recurrence().SkipOccurrence(ctx, task.ID, dates[0]); err != nil {
			t.Fatalf("skip occurrence: %v", err)
		}
		count, err = store.Recurrence().CountOccurrencesByTask(ctx, task.ID)
		if err != nil {
			t.Fatalf("count after skip: %v", err)
		}
		if count != 3 {
			t.Fatalf("expected 3 occurrences after skip (upsert, not new), got %d", count)
		}

		// Reopen one — count still the same
		if _, err := store.Recurrence().ReopenOccurrence(ctx, task.ID, dates[1]); err != nil {
			t.Fatalf("reopen occurrence: %v", err)
		}
		count, err = store.Recurrence().CountOccurrencesByTask(ctx, task.ID)
		if err != nil {
			t.Fatalf("count after reopen: %v", err)
		}
		if count != 3 {
			t.Fatalf("expected 3 occurrences after reopen (upsert, not new), got %d", count)
		}

		// Unrelated task should have count 0
		other := &model.Task{
			Title:   "other count test",
			Content: "unrelated",
			Status:  "open",
			Horizon: "week",
			Scope:   "daily",
		}
		if err := store.Tasks().Create(ctx, other); err != nil {
			t.Fatalf("create other task: %v", err)
		}
		otherCount, err := store.Recurrence().CountOccurrencesByTask(ctx, other.ID)
		if err != nil {
			t.Fatalf("count occurrences for other: %v", err)
		}
		if otherCount != 0 {
			t.Fatalf("expected 0 for unrelated task, got %d", otherCount)
		}
	})

	t.Run("UpsertRuleUpdatesExistingRule", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := context.Background()
		task := &model.Task{
			Title:   "upsert rule test",
			Content: "rule upsert",
			Status:  "open",
			Horizon: "week",
			Scope:   "daily",
		}
		if err := store.Tasks().Create(ctx, task); err != nil {
			t.Fatalf("create task: %v", err)
		}

		// First upsert creates
		rule := &model.RecurrenceRule{
			TaskID:    task.ID,
			StartDate: "2026-06-21",
			Frequency: "daily",
			Interval:  1,
			Weekdays:  []int{1, 3, 5},
			Timezone:  "UTC",
			Enabled:   true,
		}
		if err := store.Recurrence().UpsertRule(ctx, rule); err != nil {
			t.Fatalf("upsert rule: %v", err)
		}

		// Second upsert updates
		rule.Frequency = "weekly"
		rule.Interval = 2
		rule.Weekdays = []int{1}
		rule.Enabled = false
		if err := store.Recurrence().UpsertRule(ctx, rule); err != nil {
			t.Fatalf("upsert rule again: %v", err)
		}

		loaded, err := store.Recurrence().GetRule(ctx, task.ID)
		if err != nil {
			t.Fatalf("get updated rule: %v", err)
		}
		if loaded.Frequency != "weekly" {
			t.Fatalf("expected frequency weekly after update, got %q", loaded.Frequency)
		}
		if loaded.Interval != 2 {
			t.Fatalf("expected interval 2 after update, got %d", loaded.Interval)
		}
		if len(loaded.Weekdays) != 1 || loaded.Weekdays[0] != 1 {
			t.Fatalf("expected weekdays [1], got %v", loaded.Weekdays)
		}
		if loaded.Enabled {
			t.Fatal("expected enabled false after update")
		}

		// Disabled rules should not appear in ListActiveRules
		rules, err := store.Recurrence().ListActiveRules(ctx, "2026-06-20", "2026-06-22")
		if err != nil {
			t.Fatalf("list active rules: %v", err)
		}
		for _, r := range rules {
			if r.TaskID == task.ID {
				t.Fatal("disabled rule should not appear in ListActiveRules")
			}
		}
	})
}
