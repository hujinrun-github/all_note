package tenantmigration

import "testing"

func TestCompareNamespaceRecognizesAliasesAndClones(t *testing.T) {
	original := NamespaceSnapshot{Provider: "postgres", InstallationID: "install-1", DatabaseIdentity: "db-42", SchemaIdentity: "tenant_a"}
	tests := []struct {
		name  string
		other NamespaceSnapshot
		want  NamespaceRelation
	}{
		{"alias or credential change", original, NamespaceSame},
		{"different schema", NamespaceSnapshot{Provider: "postgres", InstallationID: "install-2", DatabaseIdentity: "db-42", SchemaIdentity: "tenant_b"}, NamespaceDifferent},
		{"cloned installation", NamespaceSnapshot{Provider: "postgres", InstallationID: "install-1", DatabaseIdentity: "db-99", SchemaIdentity: "tenant_a"}, NamespaceCollision},
		{"other provider", NamespaceSnapshot{Provider: "sqlite", InstallationID: "install-1", DatabaseIdentity: "file-1", SchemaIdentity: "main"}, NamespaceDifferent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := CompareNamespace(original, test.other); got != test.want {
				t.Fatalf("CompareNamespace() = %q, want %q", got, test.want)
			}
		})
	}
}
