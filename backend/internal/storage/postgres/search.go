package postgres

import (
	"context"
	"database/sql"
	"strings"
	"time"
	"unicode"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
)

type searchRepository struct {
	db postgresRunner
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

	likeQuery := "%" + query + "%"
	tagQuery := strings.TrimPrefix(query, "#")
	offset := (page - 1) * pageSize
	ftsQuery, useFTS := buildPostgresPrefixTSQuery(query)
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, 0, err
	}

	sqlText := postgresFallbackSearchSQL
	countSQL := postgresFallbackSearchCountSQL
	args := []interface{}{workspaceID, query, likeQuery, tagQuery, pageSize, offset}
	countArgs := []interface{}{workspaceID, query, likeQuery, tagQuery}
	if useFTS {
		sqlText = postgresFTSSearchSQL
		countSQL = postgresFTSSearchCountSQL
		args = []interface{}{workspaceID, query, likeQuery, tagQuery, ftsQuery, pageSize, offset}
		countArgs = []interface{}{workspaceID, query, likeQuery, tagQuery, ftsQuery}
	}

	total := 0
	if err := r.db.QueryRowContext(ctx, countSQL, countArgs...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	results := make([]model.SearchResult, 0)
	for rows.Next() {
		var result model.SearchResult
		var content string
		var updatedAt time.Time
		var folderID sql.NullString
		var done sql.NullBool
		var kind sql.NullString
		if err := rows.Scan(&result.Type, &result.ID, &result.Title, &content, &updatedAt, &folderID, &done, &kind); err != nil {
			return nil, 0, err
		}
		result.Highlight = highlightFallback(result.Title, content)
		result.UpdatedAt = timeToUnix(updatedAt)
		if folderID.Valid {
			result.FolderID = &folderID.String
		}
		if done.Valid {
			doneValue := 0
			if done.Bool {
				doneValue = 1
			}
			result.Done = &doneValue
		}
		if kind.Valid {
			result.Kind = &kind.String
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return results, total, nil
}

const postgresFallbackSearchSQL = `
WITH matched AS (
	SELECT
		s.workspace_id,
		s.entity_type,
		s.entity_id,
		s.title,
		s.content,
		s.updated_at,
		GREATEST(public.similarity(s.title, $2), public.similarity(s.content, $2)) AS rank
	FROM search_index s
	WHERE
		s.workspace_id = $1
		AND (
			s.title ILIKE $3
			OR s.content ILIKE $3
			OR EXISTS (
				SELECT 1 FROM unnest(s.tags) tag
				WHERE lower(tag) = lower($4)
			)
		)
),
visible AS (
	SELECT
		m.entity_type,
		m.entity_id,
		m.title,
		m.content,
		m.updated_at,
		m.rank,
		n.folder_id,
		t.done,
		e.kind
	FROM matched m
	LEFT JOIN notes n ON m.entity_type = 'note' AND n.workspace_id = m.workspace_id AND n.id = m.entity_id AND n.deleted_at IS NULL
	LEFT JOIN tasks t ON m.entity_type = 'task' AND t.workspace_id = m.workspace_id AND t.id = m.entity_id
	LEFT JOIN events e ON m.entity_type = 'event' AND e.workspace_id = m.workspace_id AND e.id = m.entity_id
	WHERE
		(m.entity_type = 'note' AND n.id IS NOT NULL)
		OR (m.entity_type = 'task' AND t.id IS NOT NULL)
		OR (m.entity_type = 'event' AND e.id IS NOT NULL)
),
dedup AS (
	SELECT DISTINCT ON (entity_type, entity_id)
		entity_type, entity_id, title, content, updated_at, rank, folder_id, done, kind
	FROM visible
	ORDER BY entity_type, entity_id, rank DESC, updated_at DESC
),
numbered AS (
		SELECT *, COUNT(*) OVER() AS total
		FROM dedup
		ORDER BY updated_at DESC, rank DESC
		LIMIT $5 OFFSET $6
	)
SELECT entity_type, entity_id, title, content, updated_at, folder_id, done, kind
FROM numbered
`

const postgresFallbackSearchCountSQL = `
WITH matched AS (
	SELECT
		s.workspace_id,
		s.entity_type,
		s.entity_id,
		s.title,
		s.content,
		s.updated_at,
		GREATEST(public.similarity(s.title, $2), public.similarity(s.content, $2)) AS rank
	FROM search_index s
	WHERE
		s.workspace_id = $1
		AND (
			s.title ILIKE $3
			OR s.content ILIKE $3
			OR EXISTS (
				SELECT 1 FROM unnest(s.tags) tag
				WHERE lower(tag) = lower($4)
			)
		)
),
visible AS (
	SELECT
		m.entity_type,
		m.entity_id,
		m.title,
		m.content,
		m.updated_at,
		m.rank,
		n.folder_id,
		t.done,
		e.kind
	FROM matched m
	LEFT JOIN notes n ON m.entity_type = 'note' AND n.workspace_id = m.workspace_id AND n.id = m.entity_id AND n.deleted_at IS NULL
	LEFT JOIN tasks t ON m.entity_type = 'task' AND t.workspace_id = m.workspace_id AND t.id = m.entity_id
	LEFT JOIN events e ON m.entity_type = 'event' AND e.workspace_id = m.workspace_id AND e.id = m.entity_id
	WHERE
		(m.entity_type = 'note' AND n.id IS NOT NULL)
		OR (m.entity_type = 'task' AND t.id IS NOT NULL)
		OR (m.entity_type = 'event' AND e.id IS NOT NULL)
),
dedup AS (
	SELECT DISTINCT ON (entity_type, entity_id)
		entity_type, entity_id
	FROM visible
	ORDER BY entity_type, entity_id, rank DESC, updated_at DESC
)
SELECT COUNT(*) FROM dedup
`

const postgresFTSSearchSQL = `
WITH fts AS (
	SELECT
		s.workspace_id,
		s.entity_type,
		s.entity_id,
		s.title,
		s.content,
		s.updated_at,
		ts_rank(s.search_vector, to_tsquery('simple', $5)) AS rank
	FROM search_index s
	WHERE s.workspace_id = $1
	  AND s.search_vector @@ to_tsquery('simple', $5)
),
fallback AS (
	SELECT
		s.workspace_id,
		s.entity_type,
		s.entity_id,
		s.title,
		s.content,
		s.updated_at,
		GREATEST(public.similarity(s.title, $2), public.similarity(s.content, $2)) AS rank
	FROM search_index s
	WHERE
		s.workspace_id = $1
		AND
		(
			s.title ILIKE $3
			OR s.content ILIKE $3
			OR EXISTS (
				SELECT 1 FROM unnest(s.tags) tag
				WHERE lower(tag) = lower($4)
			)
		)
		AND NOT (s.search_vector @@ to_tsquery('simple', $5))
),
matched AS (
	SELECT * FROM fts
	UNION ALL
	SELECT * FROM fallback
),
visible AS (
	SELECT
		m.entity_type,
		m.entity_id,
		m.title,
		m.content,
		m.updated_at,
		m.rank,
		n.folder_id,
		t.done,
		e.kind
	FROM matched m
	LEFT JOIN notes n ON m.entity_type = 'note' AND n.workspace_id = m.workspace_id AND n.id = m.entity_id AND n.deleted_at IS NULL
	LEFT JOIN tasks t ON m.entity_type = 'task' AND t.workspace_id = m.workspace_id AND t.id = m.entity_id
	LEFT JOIN events e ON m.entity_type = 'event' AND e.workspace_id = m.workspace_id AND e.id = m.entity_id
	WHERE
		(m.entity_type = 'note' AND n.id IS NOT NULL)
		OR (m.entity_type = 'task' AND t.id IS NOT NULL)
		OR (m.entity_type = 'event' AND e.id IS NOT NULL)
),
dedup AS (
	SELECT DISTINCT ON (entity_type, entity_id)
		entity_type, entity_id, title, content, updated_at, rank, folder_id, done, kind
	FROM visible
	ORDER BY entity_type, entity_id, rank DESC, updated_at DESC
),
	numbered AS (
		SELECT *, COUNT(*) OVER() AS total
		FROM dedup
		ORDER BY updated_at DESC, rank DESC
		LIMIT $6 OFFSET $7
	)
SELECT entity_type, entity_id, title, content, updated_at, folder_id, done, kind
FROM numbered
`

const postgresFTSSearchCountSQL = `
WITH fts AS (
	SELECT
		s.workspace_id,
		s.entity_type,
		s.entity_id,
		s.title,
		s.content,
		s.updated_at,
		ts_rank(s.search_vector, to_tsquery('simple', $5)) AS rank
	FROM search_index s
	WHERE s.workspace_id = $1
	  AND s.search_vector @@ to_tsquery('simple', $5)
),
fallback AS (
	SELECT
		s.workspace_id,
		s.entity_type,
		s.entity_id,
		s.title,
		s.content,
		s.updated_at,
		GREATEST(public.similarity(s.title, $2), public.similarity(s.content, $2)) AS rank
	FROM search_index s
	WHERE
		s.workspace_id = $1
		AND
		(
			s.title ILIKE $3
			OR s.content ILIKE $3
			OR EXISTS (
				SELECT 1 FROM unnest(s.tags) tag
				WHERE lower(tag) = lower($4)
			)
		)
		AND NOT (s.search_vector @@ to_tsquery('simple', $5))
),
matched AS (
	SELECT * FROM fts
	UNION ALL
	SELECT * FROM fallback
),
visible AS (
	SELECT
		m.entity_type,
		m.entity_id,
		m.title,
		m.content,
		m.updated_at,
		m.rank,
		n.folder_id,
		t.done,
		e.kind
	FROM matched m
	LEFT JOIN notes n ON m.entity_type = 'note' AND n.workspace_id = m.workspace_id AND n.id = m.entity_id AND n.deleted_at IS NULL
	LEFT JOIN tasks t ON m.entity_type = 'task' AND t.workspace_id = m.workspace_id AND t.id = m.entity_id
	LEFT JOIN events e ON m.entity_type = 'event' AND e.workspace_id = m.workspace_id AND e.id = m.entity_id
	WHERE
		(m.entity_type = 'note' AND n.id IS NOT NULL)
		OR (m.entity_type = 'task' AND t.id IS NOT NULL)
		OR (m.entity_type = 'event' AND e.id IS NOT NULL)
),
dedup AS (
	SELECT DISTINCT ON (entity_type, entity_id)
		entity_type, entity_id
	FROM visible
	ORDER BY entity_type, entity_id, rank DESC, updated_at DESC
)
SELECT COUNT(*) FROM dedup
`

func highlightFallback(title, content string) string {
	if strings.TrimSpace(content) != "" {
		return content
	}
	return title
}

func buildPostgresPrefixTSQuery(query string) (string, bool) {
	tokens := strings.FieldsFunc(query, func(r rune) bool {
		return !(r <= unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)))
	})
	parts := make([]string, 0, len(tokens))
	for _, token := range tokens {
		token = strings.ToLower(strings.TrimSpace(token))
		if token == "" {
			continue
		}
		parts = append(parts, token+":*")
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, " & "), true
}
