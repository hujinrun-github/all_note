package contracttest

import (
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func RunTaskSuite(t *testing.T, factory StoreFactory) {
	t.Helper()

	t.Run("TasksFilterByProjectAndPlannedDate", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		project, err := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{Name: "Writing", Type: "regular"})
		if err != nil {
			t.Fatalf("create project: %v", err)
		}
		plannedDate := "2026-06-16"
		task := &model.Task{
			Title:       "Read the story book",
			Content:     "Read and take notes",
			ProjectID:   &project.ID,
			PlannedDate: &plannedDate,
			Status:      "open",
			Horizon:     "week",
			Scope:       "daily",
		}
		if err := store.Tasks().Create(ctx, task); err != nil {
			t.Fatalf("create task: %v", err)
		}

		tasks, total, err := store.Tasks().List(ctx, storage.TaskFilter{
			Status:      "active",
			ProjectID:   project.ID,
			PlannedDate: plannedDate,
			Page:        1,
			PageSize:    20,
		})
		if err != nil {
			t.Fatalf("list tasks: %v", err)
		}
		if total != 1 || len(tasks) != 1 || tasks[0].Title != "Read the story book" {
			t.Fatalf("unexpected tasks total=%d items=%+v", total, tasks)
		}
		if tasks[0].Project == nil || *tasks[0].Project != "Writing" {
			t.Fatalf("expected project name on task, got %+v", tasks[0])
		}
	})

	t.Run("TasksFilterByRoadmapNodeID", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		project, err := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{Name: "Roadmap Context", Type: "learning"})
		if err != nil {
			t.Fatalf("create project: %v", err)
		}
		nodeID := "roadmap-node-article-context"
		otherNodeID := "roadmap-node-other"
		if _, err := store.Roadmaps().ReplaceLearningRoadmap(ctx, &model.LearningRoadmap{
			ProjectID: project.ID,
			Title:     "Roadmap context",
			Goal:      "Search with task context",
			Status:    "ready",
			Nodes: []model.RoadmapNode{
				{
					ID:         nodeID,
					Type:       "task",
					Title:      "search index",
					PathType:   "required",
					Status:     "active",
					OrderIndex: 1,
				},
				{
					ID:         otherNodeID,
					Type:       "task",
					Title:      "unrelated search context",
					PathType:   "optional",
					Status:     "active",
					OrderIndex: 2,
				},
			},
		}); err != nil {
			t.Fatalf("create roadmap: %v", err)
		}
		matching := &model.Task{
			Title:         "research vector search index",
			Content:       "compare HNSW IVF and rerank system design tradeoffs",
			Status:        "open",
			Horizon:       "week",
			Scope:         "daily",
			RoadmapNodeID: &nodeID,
		}
		other := &model.Task{
			Title:         " unrelated roadmap task ",
			Content:       "unrelated content",
			Status:        "open",
			Horizon:       "week",
			Scope:         "daily",
			RoadmapNodeID: &otherNodeID,
		}
		if err := store.Tasks().Create(ctx, matching); err != nil {
			t.Fatalf("create matching task: %v", err)
		}
		if err := store.Tasks().Create(ctx, other); err != nil {
			t.Fatalf("create other task: %v", err)
		}

		tasks, total, err := store.Tasks().List(ctx, storage.TaskFilter{
			RoadmapNodeID: nodeID,
			Page:          1,
			PageSize:      20,
		})
		if err != nil {
			t.Fatalf("list roadmap node tasks: %v", err)
		}
		if total != 1 || len(tasks) != 1 {
			t.Fatalf("expected one linked task, total=%d tasks=%+v", total, tasks)
		}
		if tasks[0].Title != matching.Title || tasks[0].Content != matching.Content {
			t.Fatalf("expected linked task content to round-trip, got %+v", tasks[0])
		}
	})

	t.Run("TaskProjectsSortCaseInsensitively", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		if _, err := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{Name: "Alpha", Type: "regular"}); err != nil {
			t.Fatalf("create alpha: %v", err)
		}
		beta, err := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{Name: "beta", Type: "regular"})
		if err != nil {
			t.Fatalf("create beta: %v", err)
		}
		time.Sleep(1100 * time.Millisecond)
		description := "recently touched"
		if _, err := store.Tasks().UpdateProject(ctx, beta.ID, &model.UpdateTaskProjectRequest{Description: &description}); err != nil {
			t.Fatalf("update beta: %v", err)
		}

		projects, err := store.Tasks().ListProjects(ctx)
		if err != nil {
			t.Fatalf("list projects: %v", err)
		}
		if len(projects) < 3 || projects[0].ID != "personal" {
			t.Fatalf("expected personal first, got %+v", projects)
		}
		if projects[1].Name != "beta" || projects[2].Name != "Alpha" {
			t.Fatalf("expected recently updated beta before Alpha, got %+v", projects)
		}
	})

	t.Run("TaskUpdateSearchAndDeleteLifecycle", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		task := &model.Task{
			Title:   "old searchable task",
			Content: "stable task body",
			Status:  "open",
			Horizon: "week",
			Scope:   "daily",
		}
		if err := store.Tasks().Create(ctx, task); err != nil {
			t.Fatalf("create task: %v", err)
		}

		title := "new searchable task"
		done := 1
		status := "done"
		updated, err := store.Tasks().Update(ctx, task.ID, &model.UpdateTaskRequest{Title: &title, Done: &done, Status: &status})
		if err != nil {
			t.Fatalf("update task: %v", err)
		}
		if updated.Title != title || updated.Done != 1 || updated.Status != "done" {
			t.Fatalf("unexpected updated task: %+v", updated)
		}

		oldResults, oldTotal, err := searchStore(ctx, store, "old searchable task", 1, 10)
		if err != nil {
			t.Fatalf("search old task: %v", err)
		}
		if oldTotal != 0 || len(oldResults) != 0 {
			t.Fatalf("expected old task to disappear, total=%d results=%+v", oldTotal, oldResults)
		}

		newResults, newTotal, err := searchStore(ctx, store, "new searchable task", 1, 10)
		if err != nil {
			t.Fatalf("search new task: %v", err)
		}
		if newTotal != 1 || !hasSearchResult(newResults, "task", task.ID) {
			t.Fatalf("expected new task search result, total=%d results=%+v", newTotal, newResults)
		}

		if err := store.Tasks().Delete(ctx, task.ID); err != nil {
			t.Fatalf("delete task: %v", err)
		}
		deletedResults, deletedTotal, err := searchStore(ctx, store, "new searchable task", 1, 10)
		if err != nil {
			t.Fatalf("search deleted task: %v", err)
		}
		if deletedTotal != 0 || len(deletedResults) != 0 {
			t.Fatalf("expected deleted task to disappear, total=%d results=%+v", deletedTotal, deletedResults)
		}
	})

	t.Run("TaskStatusesIncludeMigratedAndCancelled", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		task := &model.Task{Title: "status compatibility task", Content: "history", Status: "open", Horizon: "week", Scope: "daily"}
		if err := store.Tasks().Create(ctx, task); err != nil {
			t.Fatalf("create task: %v", err)
		}
		for _, status := range []string{"migrated", "cancelled"} {
			updated, err := store.Tasks().Update(ctx, task.ID, &model.UpdateTaskRequest{Status: &status})
			if err != nil {
				t.Fatalf("update status %s: %v", status, err)
			}
			if updated.Status != status {
				t.Fatalf("expected status %s, got %+v", status, updated)
			}
		}
	})

	t.Run("TodayTasksUseLocalDateBoundaries", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		localDay := time.Date(2026, 6, 16, 0, 0, 0, 0, time.Local)
		todayStart := localDay.Unix()
		todayEnd := localDay.Add(24 * time.Hour).Unix()
		overdueCutoff := todayStart
		plannedDate := "2026-06-16"
		due0030 := time.Date(2026, 6, 16, 0, 30, 0, 0, time.Local).Unix()
		due2330 := time.Date(2026, 6, 16, 23, 30, 0, 0, time.Local).Unix()

		cases := []model.Task{
			{Title: "due at local 00:30", Due: &due0030, Status: "open", Horizon: "week", Scope: "daily"},
			{Title: "due at local 23:30", Due: &due2330, Status: "open", Horizon: "week", Scope: "daily"},
			{Title: "planned date without due", PlannedDate: &plannedDate, Status: "open", Horizon: "week", Scope: "daily"},
			{Title: "active long task", Status: "active", Horizon: "long", Scope: "monthly"},
		}
		for i := range cases {
			if err := store.Tasks().Create(ctx, &cases[i]); err != nil {
				t.Fatalf("create task %q: %v", cases[i].Title, err)
			}
		}

		todayTasks, overdueTasks, err := store.Tasks().Today(ctx, todayStart, todayEnd, overdueCutoff)
		if err != nil {
			t.Fatalf("today tasks: %v", err)
		}
		if len(overdueTasks) != 0 {
			t.Fatalf("expected no overdue tasks, got %+v", overdueTasks)
		}

		found := map[string]bool{}
		for _, task := range todayTasks {
			found[task.Title] = true
		}
		for _, title := range []string{"due at local 00:30", "due at local 23:30", "planned date without due", "active long task"} {
			if !found[title] {
				t.Fatalf("expected %q in today tasks, got %+v", title, todayTasks)
			}
		}
	})
}
