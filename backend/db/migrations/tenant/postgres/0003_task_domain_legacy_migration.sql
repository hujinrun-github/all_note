CREATE TABLE legacy_task_domain_entity_versions (
    workspace_id TEXT NOT NULL,
    entity_kind TEXT NOT NULL CHECK (entity_kind IN ('project','task','rule','occurrence','event','roadmap','roadmap_node','roadmap_edge')),
    entity_id TEXT NOT NULL CHECK (btrim(entity_id) <> ''),
    logical_version BIGINT NOT NULL CHECK (logical_version > 0),
    deleted BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, entity_kind, entity_id),
    FOREIGN KEY (workspace_id) REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE
);

CREATE TABLE task_domain_legacy_outbox (
    sequence BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    entity_kind TEXT NOT NULL CHECK (entity_kind IN ('project','task','rule','occurrence','event','roadmap','roadmap_node','roadmap_edge')),
    entity_id TEXT NOT NULL CHECK (btrim(entity_id) <> ''),
    operation TEXT NOT NULL CHECK (operation IN ('upsert','delete')),
    source_logical_version BIGINT NOT NULL CHECK (source_logical_version > 0),
    row_image JSONB CHECK (row_image IS NULL OR jsonb_typeof(row_image) = 'object'),
    tombstone_image JSONB CHECK (tombstone_image IS NULL OR jsonb_typeof(tombstone_image) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id) REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE,
    CHECK (
        (operation = 'upsert' AND row_image IS NOT NULL AND tombstone_image IS NULL)
        OR
        (operation = 'delete' AND row_image IS NULL AND tombstone_image IS NOT NULL)
    )
);

CREATE INDEX task_domain_legacy_outbox_workspace_sequence_idx
    ON task_domain_legacy_outbox(workspace_id, sequence);

CREATE TABLE task_domain_legacy_id_map (
    workspace_id TEXT NOT NULL,
    entity_kind TEXT NOT NULL CHECK (entity_kind IN ('project','task','rule','occurrence','event','roadmap','roadmap_node','roadmap_edge')),
    legacy_id TEXT NOT NULL CHECK (btrim(legacy_id) <> ''),
    target_kind TEXT NOT NULL CHECK (target_kind IN ('project','task','schedule','occurrence','roadmap','roadmap_node','roadmap_edge')),
    v2_id TEXT NOT NULL CHECK (btrim(v2_id) <> ''),
    source_logical_version BIGINT NOT NULL CHECK (source_logical_version > 0),
    deleted BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, entity_kind, legacy_id, target_kind),
    UNIQUE (workspace_id, target_kind, v2_id),
    FOREIGN KEY (workspace_id) REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE
);
