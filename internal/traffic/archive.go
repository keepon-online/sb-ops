package traffic

import (
	"context"
	"time"

	"github.com/keepon-online/sb-ops/internal/service/common"
)

// retentionTables lists the traffic tables and their time column for the
// automatic retention purge. Raw ingestion batches accumulate one row per
// collection cycle and are only needed for deduplication, so they are aged out
// along with the aggregated history.
var retentionTables = []struct{ table, column string }{
	{"traffic_hourly", "bucket_start"},
	{"traffic_daily", "bucket_start"},
	{"traffic_batches", "collected_at"},
}

// PurgeBefore deletes aggregated history and raw ingestion batches older than
// before. It backs the automatic retention setting (traffic.retention_days).
func (s *Service) PurgeBefore(ctx context.Context, before time.Time) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var removed int64
	for _, item := range retentionTables {
		result, err := tx.ExecContext(ctx, `DELETE FROM `+item.table+` WHERE `+item.column+`<?`, common.UnixTime(before))
		if err != nil {
			return 0, err
		}
		count, _ := result.RowsAffected()
		removed += count
	}
	return removed, tx.Commit()
}
