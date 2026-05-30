package repository

import (
	"sort"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
)

const searchPageMultiplier = 3

func Search(q string, page, pageSize int) ([]model.SearchResult, int, error) {
	if strings.TrimSpace(q) == "" {
		return []model.SearchResult{}, 0, nil
	}

	ftsQuery := buildFTS5Query(q)
	limit := pageSize * searchPageMultiplier
	var allResults []model.SearchResult
	totalCount := 0

	noteResults, noteCount := searchNotes(ftsQuery, limit)
	totalCount += noteCount
	allResults = append(allResults, noteResults...)

	taskResults, taskCount := searchTasks(ftsQuery, limit)
	totalCount += taskCount
	allResults = append(allResults, taskResults...)

	eventResults, eventCount := searchEvents(ftsQuery, limit)
	totalCount += eventCount
	allResults = append(allResults, eventResults...)

	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].UpdatedAt > allResults[j].UpdatedAt
	})

	start := (page - 1) * pageSize
	if start > len(allResults) {
		return []model.SearchResult{}, totalCount, nil
	}
	end := start + pageSize
	if end > len(allResults) {
		end = len(allResults)
	}

	return allResults[start:end], totalCount, nil
}

func buildFTS5Query(q string) string {
	q = strings.TrimSpace(q)
	if strings.Contains(q, "\"") {
		return q
	}
	words := strings.Fields(q)
	for i, w := range words {
		words[i] = "\"" + w + "\""
	}
	return strings.Join(words, " AND ")
}

func searchNotes(q string, limit int) ([]model.SearchResult, int) {
	var total int
	DB.QueryRow("SELECT COUNT(*) FROM notes_fts WHERE notes_fts MATCH ?", q).Scan(&total)

	rows, err := DB.Query(`
		SELECT n.id, n.title, snippet(notes_fts, 1, '<mark>', '</mark>', '...', 40) as highlight,
		       n.folder_id, n.updated_at
		FROM notes_fts
		JOIN notes n ON n.rowid = notes_fts.rowid
		WHERE notes_fts MATCH ?
		ORDER BY n.updated_at DESC LIMIT ?
	`, q, limit)
	if err != nil {
		return nil, 0
	}
	defer rows.Close()

	var results []model.SearchResult
	for rows.Next() {
		var r model.SearchResult
		r.Type = "note"
		rows.Scan(&r.ID, &r.Title, &r.Highlight, &r.FolderID, &r.UpdatedAt)
		results = append(results, r)
	}
	return results, total
}

func searchTasks(q string, limit int) ([]model.SearchResult, int) {
	var total int
	DB.QueryRow("SELECT COUNT(*) FROM tasks_fts WHERE tasks_fts MATCH ?", q).Scan(&total)

	rows, err := DB.Query(`
		SELECT t.id, t.title, snippet(tasks_fts, 0, '<mark>', '</mark>', '...', 40) as highlight,
		       t.done, t.updated_at
		FROM tasks_fts
		JOIN tasks t ON t.rowid = tasks_fts.rowid
		WHERE tasks_fts MATCH ?
		ORDER BY t.updated_at DESC LIMIT ?
	`, q, limit)
	if err != nil {
		return nil, 0
	}
	defer rows.Close()

	var results []model.SearchResult
	for rows.Next() {
		var r model.SearchResult
		r.Type = "task"
		rows.Scan(&r.ID, &r.Title, &r.Highlight, &r.Done, &r.UpdatedAt)
		results = append(results, r)
	}
	return results, total
}

func searchEvents(q string, limit int) ([]model.SearchResult, int) {
	var total int
	DB.QueryRow("SELECT COUNT(*) FROM events_fts WHERE events_fts MATCH ?", q).Scan(&total)

	rows, err := DB.Query(`
		SELECT e.id, e.title, snippet(events_fts, 0, '<mark>', '</mark>', '...', 40) as highlight,
		       e.kind, e.updated_at
		FROM events_fts
		JOIN events e ON e.rowid = events_fts.rowid
		WHERE events_fts MATCH ?
		ORDER BY e.updated_at DESC LIMIT ?
	`, q, limit)
	if err != nil {
		return nil, 0
	}
	defer rows.Close()

	var results []model.SearchResult
	for rows.Next() {
		var r model.SearchResult
		r.Type = "event"
		rows.Scan(&r.ID, &r.Title, &r.Highlight, &r.Kind, &r.UpdatedAt)
		results = append(results, r)
	}
	return results, total
}
