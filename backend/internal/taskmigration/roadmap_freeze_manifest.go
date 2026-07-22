package taskmigration

import "strings"

// LegacyRoadmapFreezeSpec is the database-enforced substitute for incremental
// roadmap outbox replay. While a workspace migration is live, every legacy
// roadmap graph mutation is rejected; while it is idle/failed, the same
// trigger maintains the durable logical-version ledger.
type LegacyRoadmapFreezeSpec struct {
	EntityKind LegacyEntityKind
	Table      string
	Operation  TriggerOperation
	Name       string
}

// LegacyRoadmapFreezeTriggerManifest is consumed by installers and final
// observers. The names are a stable cross-dialect cutover contract.
func LegacyRoadmapFreezeTriggerManifest() []LegacyRoadmapFreezeSpec {
	tables := []struct {
		kind  LegacyEntityKind
		table string
	}{
		{LegacyEntityRoadmap, "learning_roadmaps"},
		{LegacyEntityRoadmapNode, "roadmap_nodes"},
		{LegacyEntityRoadmapEdge, "roadmap_edges"},
	}
	operations := []TriggerOperation{TriggerInsert, TriggerUpdate, TriggerDelete}
	result := make([]LegacyRoadmapFreezeSpec, 0, len(tables)*len(operations))
	for _, table := range tables {
		for _, operation := range operations {
			result = append(result, LegacyRoadmapFreezeSpec{
				EntityKind: table.kind, Table: table.table, Operation: operation,
				Name: "task_domain_legacy_roadmap_freeze_" + table.table + "_" + strings.ToLower(string(operation)),
			})
		}
	}
	return result
}
