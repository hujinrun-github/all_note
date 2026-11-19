package japaneseintegration

import (
	"database/sql"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskdomain"
	"testing"
	"time"
)

type CrossProjectFixture struct {
	Runtime                                          RuntimeSnapshot
	Service                                          *Service
	ControlDB, TenantDB                              *sql.DB
	WorkspaceID, ActorID, TargetTaskID, OccurrenceID string
	Now                                              time.Time
}

func NewCrossProjectFixture(t *testing.T, driver storage.Driver) CrossProjectFixture {
	t.Helper()
	f := fixture(t, driver)
	f.now = time.Now()
	return CrossProjectFixture{Service: f.s, ControlDB: f.control, TenantDB: f.tenant, WorkspaceID: "w1", ActorID: "u1", TargetTaskID: "target", OccurrenceID: taskdomain.DeterministicOccurrenceID("w1", "target", "2026-11-19"), Now: f.now, Runtime: f.runtime.snapshot}
}
