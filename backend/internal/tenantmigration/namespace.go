package tenantmigration

// NamespaceSnapshot is the persisted physical identity used by transition jobs.
// Endpoint IDs and hostnames are deliberately excluded because aliases and
// credential rotations can still point at the same namespace.
type NamespaceSnapshot struct {
	Provider         string
	InstallationID   string
	DatabaseIdentity string
	SchemaIdentity   string
}

type NamespaceRelation string

const (
	NamespaceSame      NamespaceRelation = "same"
	NamespaceDifferent NamespaceRelation = "different"
	NamespaceCollision NamespaceRelation = "installation_collision"
)

func CompareNamespace(left, right NamespaceSnapshot) NamespaceRelation {
	if left.Provider != right.Provider {
		return NamespaceDifferent
	}
	if left.InstallationID == right.InstallationID &&
		left.DatabaseIdentity == right.DatabaseIdentity &&
		left.SchemaIdentity == right.SchemaIdentity {
		return NamespaceSame
	}
	if left.InstallationID != "" && left.InstallationID == right.InstallationID {
		return NamespaceCollision
	}
	return NamespaceDifferent
}
