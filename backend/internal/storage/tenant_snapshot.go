package storage

type TenantSnapshot struct {
	WorkspaceID    string
	InstallationID string
	SchemaVersion  string
	Tables         []TenantSnapshotTable
}

type TenantSnapshotTable struct {
	Name   string
	Rows   int64
	SHA256 string
}
