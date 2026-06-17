package sqlite

import (
	"context"
	"sort"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
)

const searchPageMultiplier = 3

type searchRepository struct {
	db sqliteRunner
}

func (r searchRepository) Search(ctx context.Context, query string, page, pageSize int) ([]model.SearchResult, int, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []model.SearchResult{}, 0, nil
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	fallbackQuery := fallbackSearchQuery(query)
	skipFTS := strings.HasPrefix(query, "#")
	ftsQuery := buildFTS5Query(query)
	limit := pageSize * searchPageMultiplier
	results := make([]model.SearchResult, 0)
	total := 0

	var noteResults []model.SearchResult
	var noteTotal int
	var taskResults []model.SearchResult
	var taskTotal int
	var eventResults []model.SearchResult
	var eventTotal int
	var err error
	if !skipFTS {
		noteResults, noteTotal, err = r.searchNotesFTS(ctx, ftsQuery, limit)
		if err != nil {
			return nil, 0, err
		}
		results = append(results, noteResults...)
		total += noteTotal

		taskResults, taskTotal, err = r.searchTasksFTS(ctx, ftsQuery, limit)
		if err != nil {
			return nil, 0, err
		}
		results = append(results, taskResults...)
		total += taskTotal

		eventResults, eventTotal, err = r.searchEventsFTS(ctx, ftsQuery, limit)
		if err != nil {
			return nil, 0, err
		}
		results = append(results, eventResults...)
		total += eventTotal
	}

	if len(results) == 0 {
		likeQuery := "%" + fallbackQuery + "%"
		noteResults, noteTotal, err = r.searchNotesLIKE(ctx, likeQuery, limit)
		if err != nil {
			return nil, 0, err
		}
		taskResults, taskTotal, err = r.searchTasksLIKE(ctx, likeQuery, limit)
		if err != nil {
			return nil, 0, err
		}
		eventResults, eventTotal, err = r.searchEventsLIKE(ctx, likeQuery, limit)
		if err != nil {
			return nil, 0, err
		}
		results = append(results, noteResults...)
		results = append(results, taskResults...)
		results = append(results, eventResults...)
		total = noteTotal + taskTotal + eventTotal
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].UpdatedAt > results[j].UpdatedAt
	})

	start := (page - 1) * pageSize
	if start > len(results) {
		return []model.SearchResult{}, total, nil
	}
	end := start + pageSize
	if end > len(results) {
		end = len(results)
	}
	return results[start:end], total, nil
}

func fallbackSearchQuery(query string) string {
	return strings.TrimSpace(strings.TrimPrefix(query, "#"))
}

func buildFTS5Query(query string) string {
	if strings.Contains(query, "\"") {
		return query
	}
	words := strings.Fields(query)
	for i, word := range words {
		words[i] = word + "*"
	}
	return strings.Join(words, " ")
}

func (r searchRepository) searchNotesFTS(ctx context.Context, query string, limit int) ([]model.SearchResult, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM notes_fts WHERE notes_fts MATCH ?", query).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT n.id, n.title, snippet(notes_fts, 1, '<mark>', '</mark>', '...', 40) as highlight,
		       n.folder_id, n.updated_at
		FROM notes_fts
		JOIN notes n ON n.rowid = notes_fts.rowid
		WHERE notes_fts MATCH ?
		ORDER BY n.updated_at DESC LIMIT ?
	`, query, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	results := make([]model.SearchResult, 0)
	for rows.Next() {
		var result model.SearchResult
		result.Type = "note"
		if err := rows.Scan(&result.ID, &result.Title, &result.Highlight, &result.FolderID, &result.UpdatedAt); err != nil {
			return nil, 0, err
		}
		results = append(results, result)
	}
	return results, total, rows.Err()
}

func (r searchRepository) searchTasksFTS(ctx context.Context, query string, limit int) ([]model.SearchResult, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks_fts WHERE tasks_fts MATCH ?", query).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT t.id, t.title, snippet(tasks_fts, 0, '<mark>', '</mark>', '...', 40) as highlight,
		       t.done, t.updated_at
		FROM tasks_fts
		JOIN tasks t ON t.rowid = tasks_fts.rowid
		WHERE tasks_fts MATCH ?
		ORDER BY t.updated_at DESC LIMIT ?
	`, query, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	results := make([]model.SearchResult, 0)
	for rows.Next() {
		var result model.SearchResult
		result.Type = "task"
		if err := rows.Scan(&result.ID, &result.Title, &result.Highlight, &result.Done, &result.UpdatedAt); err != nil {
			return nil, 0, err
		}
		results = append(results, result)
	}
	return results, total, rows.Err()
}

func (r searchRepository) searchEventsFTS(ctx context.Context, query string, limit int) ([]model.SearchResult, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM events_fts WHERE events_fts MATCH ?", query).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT e.id, e.title, snippet(events_fts, 0, '<mark>', '</mark>', '...', 40) as highlight,
		       e.kind, e.updated_at
		FROM events_fts
		JOIN events e ON e.rowid = events_fts.rowid
		WHERE events_fts MATCH ?
		ORDER BY e.updated_at DESC LIMIT ?
	`, query, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	results := make([]model.SearchResult, 0)
	for rows.Next() {
		var result model.SearchResult
		result.Type = "event"
		if err := rows.Scan(&result.ID, &result.Title, &result.Highlight, &result.Kind, &result.UpdatedAt); err != nil {
			return nil, 0, err
		}
		results = append(results, result)
	}
	return results, total, rows.Err()
}

func (r searchRepository) searchNotesLIKE(ctx context.Context, query string, limit int) ([]model.SearchResult, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM notes WHERE title LIKE ? OR body LIKE ? OR tags LIKE ?", query, query, query).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id, title, folder_id, updated_at
		FROM notes
		WHERE title LIKE ? OR body LIKE ? OR tags LIKE ?
		ORDER BY updated_at DESC LIMIT ?
	`, query, query, query, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	results := make([]model.SearchResult, 0)
	for rows.Next() {
		var result model.SearchResult
		result.Type = "note"
		if err := rows.Scan(&result.ID, &result.Title, &result.FolderID, &result.UpdatedAt); err != nil {
			return nil, 0, err
		}
		result.Highlight = result.Title
		results = append(results, result)
	}
	return results, total, rows.Err()
}

func (r searchRepository) searchTasksLIKE(ctx context.Context, query string, limit int) ([]model.SearchResult, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks WHERE title LIKE ?", query).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id, title, done, updated_at
		FROM tasks
		WHERE title LIKE ?
		ORDER BY updated_at DESC LIMIT ?
	`, query, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	results := make([]model.SearchResult, 0)
	for rows.Next() {
		var result model.SearchResult
		result.Type = "task"
		if err := rows.Scan(&result.ID, &result.Title, &result.Done, &result.UpdatedAt); err != nil {
			return nil, 0, err
		}
		result.Highlight = result.Title
		results = append(results, result)
	}
	return results, total, rows.Err()
}

func (r searchRepository) searchEventsLIKE(ctx context.Context, query string, limit int) ([]model.SearchResult, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM events WHERE title LIKE ? OR location LIKE ?", query, query).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id, title, kind, updated_at
		FROM events
		WHERE title LIKE ? OR location LIKE ?
		ORDER BY updated_at DESC LIMIT ?
	`, query, query, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	results := make([]model.SearchResult, 0)
	for rows.Next() {
		var result model.SearchResult
		result.Type = "event"
		if err := rows.Scan(&result.ID, &result.Title, &result.Kind, &result.UpdatedAt); err != nil {
			return nil, 0, err
		}
		result.Highlight = result.Title
		results = append(results, result)
	}
	return results, total, rows.Err()
}
