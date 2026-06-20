package postgres

import (
	"fmt"
	"strings"
)

func pgPlaceholder(index int) string {
	return fmt.Sprintf("$%d", index)
}

func pgPlaceholders(start, count int) string {
	parts := make([]string, count)
	for i := 0; i < count; i++ {
		parts[i] = pgPlaceholder(start + i)
	}
	return strings.Join(parts, ",")
}

func pgInClause(column string, start, count int) (string, error) {
	if count <= 0 {
		return "", fmt.Errorf("%s IN clause requires at least one value", column)
	}
	return fmt.Sprintf("%s IN (%s)", column, pgPlaceholders(start, count)), nil
}

type pgSetBuilder struct {
	next int
	sets []string
	args []interface{}
}

func newPgSetBuilder(start int) *pgSetBuilder {
	return &pgSetBuilder{next: start}
}

func (b *pgSetBuilder) Add(column string, value interface{}) {
	b.sets = append(b.sets, fmt.Sprintf("%s = %s", column, pgPlaceholder(b.next)))
	b.args = append(b.args, value)
	b.next++
}

func (b *pgSetBuilder) ClauseAndArgs() (string, []interface{}) {
	return strings.Join(b.sets, ", "), b.args
}
