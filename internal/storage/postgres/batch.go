package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Bound both bind parameters and buffered payloads. One multi-row statement
// replaces hundreds of network round trips without making a whole archive one
// unbounded request. All SQL fragments below are repository-owned constants;
// only values become bind parameters.
const (
	batchRows         = 512
	batchBytes        = 1 << 20
	maxBindParameters = 65535
)

type insertBatch struct {
	ctx                  context.Context
	tx                   *sql.Tx
	insert, suffix       string
	args                 []any
	columns, rows, bytes int
}

func newInsertBatch(ctx context.Context, tx *sql.Tx, insert, suffix string) *insertBatch {
	return &insertBatch{ctx: ctx, tx: tx, insert: insert, suffix: suffix}
}

func (b *insertBatch) add(values ...any) error {
	if err := b.ctx.Err(); err != nil {
		return err
	}
	if len(values) == 0 || len(values) > maxBindParameters || b.columns != 0 && b.columns != len(values) {
		return errors.New("invalid PostgreSQL batch row width")
	}
	size := len(values) * 8 // parameter length/type and placeholder overhead
	for _, v := range values {
		switch v := v.(type) {
		case string:
			size += len(v)
		case []byte:
			size += len(v)
		}
	}
	if b.rows > 0 && (b.rows == batchRows || len(b.args)+len(values) > maxBindParameters || b.bytes+size > batchBytes) {
		if err := b.flush(); err != nil {
			return err
		}
	}
	b.columns = len(values)
	b.rows++
	b.bytes += size
	b.args = append(b.args, values...)
	return nil
}

func (b *insertBatch) flush() error {
	if b.rows == 0 {
		return nil
	}
	var query strings.Builder
	query.WriteString(b.insert)
	query.WriteString(" VALUES ")
	for row := range b.rows {
		if row > 0 {
			query.WriteByte(',')
		}
		query.WriteByte('(')
		for column := range b.columns {
			if column > 0 {
				query.WriteByte(',')
			}
			query.WriteByte('$')
			query.WriteString(strconv.Itoa(row*b.columns + column + 1))
		}
		query.WriteByte(')')
	}
	query.WriteString(b.suffix)
	_, err := b.tx.ExecContext(b.ctx, query.String(), b.args...)
	clear(b.args)
	b.args = b.args[:0]
	b.rows, b.bytes = 0, 0
	if err != nil {
		return fmt.Errorf("write PostgreSQL batch: %w", err)
	}
	return nil
}
