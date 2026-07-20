package tenantmigration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

var ErrSourceNotFenced = errors.New("source workspace must be fenced before export")

type Snapshot interface {
	Workspace(context.Context) (workspaceID, state string, err error)
	Schema(context.Context) (version string, capabilities map[string]bool, err error)
	ReadTable(context.Context, LogicalTable) ([]LogicalRow, error)
	Close() error
}

func Export(ctx context.Context, snapshot Snapshot) (TransferPackage, error) {
	if snapshot == nil {
		return TransferPackage{}, errors.New("snapshot is required")
	}
	defer snapshot.Close()
	workspaceID, state, err := snapshot.Workspace(ctx)
	if err != nil {
		return TransferPackage{}, err
	}
	if state != "fenced" {
		return TransferPackage{}, ErrSourceNotFenced
	}
	schema, capabilities, err := snapshot.Schema(ctx)
	if err != nil {
		return TransferPackage{}, err
	}
	pack := TransferPackage{Manifest: TransferManifest{WorkspaceID: strings.TrimSpace(workspaceID), Schema: schema, Capabilities: capabilities}}
	for _, table := range BaselineLogicalTables() {
		rows, err := snapshot.ReadTable(ctx, table)
		if err != nil {
			return TransferPackage{}, fmt.Errorf("export logical table %s: %w", table.Name, err)
		}
		normalized, tableManifest, err := normalizeTable(table, rows)
		if err != nil {
			return TransferPackage{}, fmt.Errorf("export logical table %s: %w", table.Name, err)
		}
		pack.Tables = append(pack.Tables, TableData{Table: table, Rows: normalized})
		pack.Manifest.Tables = append(pack.Manifest.Tables, tableManifest)
	}
	pack.Manifest.LogicalHash = manifestHash(pack.Manifest)
	return pack, nil
}

func normalizeTable(table LogicalTable, rows []LogicalRow) ([]LogicalRow, TableManifest, error) {
	normalized := make([]LogicalRow, 0, len(rows))
	for _, row := range rows {
		item := make(LogicalRow, len(table.Columns))
		for _, column := range table.Columns {
			value, exists := row[column]
			if !exists {
				return nil, TableManifest{}, fmt.Errorf("missing column %s", column)
			}
			var err error
			item[column], err = normalizeValue(value, table.JSONColumns[column], table.BooleanColumns[column], table.TimestampColumns[column])
			if err != nil {
				return nil, TableManifest{}, fmt.Errorf("invalid column %s", column)
			}
		}
		normalized = append(normalized, item)
	}
	sort.Slice(normalized, func(i, j int) bool { return rowKey(table, normalized[i]) < rowKey(table, normalized[j]) })
	pkHash, criticalHash := sha256.New(), sha256.New()
	var maxRevision int64
	for _, row := range normalized {
		writeCanonical(pkHash, table.PrimaryKey, row)
		writeCanonical(criticalHash, table.Columns, row)
		if table.RevisionColumn != "" {
			if revision, ok := asInt64(row[table.RevisionColumn]); ok && revision > maxRevision {
				maxRevision = revision
			}
		}
	}
	return normalized, TableManifest{Name: table.Name, Rows: int64(len(normalized)), PrimaryKeyHash: hex.EncodeToString(pkHash.Sum(nil)), CriticalHash: hex.EncodeToString(criticalHash.Sum(nil)), MaxRevision: maxRevision}, nil
}

func normalizeValue(value any, jsonColumn, booleanColumn, timestampColumn bool) (any, error) {
	if value == nil {
		return nil, nil
	}
	if booleanColumn {
		switch typed := value.(type) {
		case bool:
			return typed, nil
		case int64:
			return typed != 0, nil
		case float64:
			return typed != 0, nil
		}
	}
	if jsonColumn {
		var raw []byte
		switch typed := value.(type) {
		case string:
			raw = []byte(typed)
		case []byte:
			raw = typed
		default:
			raw, _ = json.Marshal(typed)
		}
		var decoded any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&decoded); err != nil {
			return nil, err
		}
		canonical, err := json.Marshal(decoded)
		if err != nil {
			return nil, err
		}
		return string(canonical), nil
	}
	if timestampColumn {
		if typed, ok := value.(time.Time); ok {
			return typed.UTC().Format(time.RFC3339Nano), nil
		}
		if typed, ok := value.(string); ok {
			for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
				if parsed, err := time.Parse(layout, typed); err == nil {
					return parsed.UTC().Format(time.RFC3339Nano), nil
				}
			}
			return nil, errors.New("invalid timestamp")
		}
	}
	switch typed := value.(type) {
	case []byte:
		return string(typed), nil
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano), nil
	case float64:
		if typed == math.Trunc(typed) && typed >= math.MinInt64 && typed <= math.MaxInt64 {
			return int64(typed), nil
		}
	}
	return value, nil
}

func rowKey(table LogicalTable, row LogicalRow) string {
	var b strings.Builder
	for _, c := range table.PrimaryKey {
		b.WriteString(fmt.Sprint(row[c]))
		b.WriteByte(0)
	}
	return b.String()
}
func writeCanonical(hash interface{ Write([]byte) (int, error) }, columns []string, row LogicalRow) {
	values := make([]any, 0, len(columns))
	for _, c := range columns {
		values = append(values, row[c])
	}
	encoded, _ := json.Marshal(values)
	_, _ = hash.Write(encoded)
	_, _ = hash.Write([]byte("\n"))
}
func asInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case json.Number:
		n, e := typed.Int64()
		return n, e == nil
	case string:
		n, e := strconv.ParseInt(typed, 10, 64)
		return n, e == nil
	}
	return 0, false
}
