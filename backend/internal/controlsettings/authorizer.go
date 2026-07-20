package controlsettings

import (
	"context"
	"database/sql"
	"errors"
)

type SQLAuthorizer struct {
	db       *sql.DB
	postgres bool
}

func NewSQLAuthorizer(db *sql.DB, postgres bool) (*SQLAuthorizer, error) {
	if db == nil {
		return nil, errors.New("settings authorizer database is required")
	}
	return &SQLAuthorizer{db: db, postgres: postgres}, nil
}

func (a *SQLAuthorizer) CanManageWorkspace(ctx context.Context, userID, workspaceID string) (bool, error) {
	query := `SELECT COUNT(*) FROM workspace_members WHERE workspace_id=? AND user_id=? AND role='owner'`
	if a.postgres {
		query = `SELECT COUNT(*) FROM workspace_members WHERE workspace_id=$1 AND user_id=$2 AND role='owner'`
	}
	var count int
	if err := a.db.QueryRowContext(ctx, query, workspaceID, userID).Scan(&count); err != nil {
		return false, err
	}
	return count == 1, nil
}

var _ Authorizer = (*SQLAuthorizer)(nil)
