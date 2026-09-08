package traffic

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/keepon-online/sb-ops/internal/service/common"
)

var (
	ErrBatchConflict   = errors.New("流量批次与已有批次或序列冲突")
	ErrCounterRollback = errors.New("累计流量计数回退")
	// ErrInvalidRequest marks query-parameter errors so the HTTP layer can map
	// them to 400 without leaking internal failures as client mistakes.
	ErrInvalidRequest = errors.New("流量查询参数无效")
)

type Service struct {
	db            *sql.DB
	now           func() time.Time
	lastCollected atomic.Int64 // unix nanos of the last successful collection cycle
}

func NewService(db *sql.DB) *Service {
	// now 返回本地时区时刻：Unix 绝对值才是消费方所用，时区语义仅供
	// traffic_daily 的日界对齐（服务器时区即业务时区，见 aggregateDaily）。
	return &Service{db: db, now: time.Now}
}

// RecordCollection records that a collection cycle completed successfully. It
// is called by the local collector and read by the overview and the collection
// stall alert.
func (s *Service) RecordCollection() {
	s.lastCollected.Store(s.now().UnixNano())
}

// LastCollectedAt returns when the collection loop last completed a successful
// cycle, or the zero time when it has never succeeded.
func (s *Service) LastCollectedAt() time.Time {
	value := s.lastCollected.Load()
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value).UTC()
}

// CollectionStalled reports whether the collection loop has not completed a
// successful cycle within the threshold.
func (s *Service) CollectionStalled(now time.Time, threshold time.Duration) bool {
	last := s.LastCollectedAt()
	return last.IsZero() || now.Sub(last) > threshold
}

// IngestLocal accepts a batch collected by the in-process LocalCollector and
// writes it to the local traffic partition.
func (s *Service) IngestLocal(ctx context.Context, batch Batch) (IngestResult, error) {
	batch.ID, batch.BootID = strings.TrimSpace(batch.ID), strings.TrimSpace(batch.BootID)
	if batch.ID == "" || batch.BootID == "" || batch.Sequence < 1 || batch.CollectedAt.IsZero() || len(batch.Samples) == 0 || len(batch.Samples) > 5000 {
		return IngestResult{}, errors.New("需要提供 batch id、bootId、正数 sequence、collectedAt 和 1..5000 个样本")
	}
	for _, sample := range batch.Samples {
		if sample.UpBytes < 0 || sample.DownBytes < 0 {
			return IngestResult{}, errors.New("流量计数不能为负数")
		}
	}
	payload, err := json.Marshal(batch)
	if err != nil {
		return IngestResult{}, err
	}
	digest := sha256.Sum256(payload)
	hash := hex.EncodeToString(digest[:])
	now := s.now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return IngestResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var existingHash string
	err = tx.QueryRowContext(ctx, `SELECT payload_hash FROM traffic_batches WHERE id=?`, batch.ID).Scan(&existingHash)
	if err == nil {
		if existingHash != hash {
			return IngestResult{}, ErrBatchConflict
		}
		return IngestResult{AcceptedBatchID: batch.ID, LastSequence: batch.Sequence, Duplicate: true, ChangedUsers: []string{}}, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return IngestResult{}, err
	}
	var lastSequence int64
	err = tx.QueryRowContext(ctx, `SELECT last_sequence FROM traffic_boot_sequences WHERE node_id=? AND boot_id=?`, LocalNodeID, batch.BootID).Scan(&lastSequence)
	if err == nil && batch.Sequence <= lastSequence {
		return IngestResult{}, ErrBatchConflict
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return IngestResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO traffic_batches(id,node_id,boot_id,sequence,payload_hash,collected_at,received_at) VALUES(?,?,?,?,?,?,?)`, batch.ID, LocalNodeID, batch.BootID, batch.Sequence, hash, common.UnixTime(batch.CollectedAt), common.UnixTime(now)); err != nil {
		return IngestResult{}, ErrBatchConflict
	}
	changedUsers := map[string]bool{}
	bucket := batch.CollectedAt.UTC().Truncate(time.Hour)
	for _, sample := range batch.Samples {
		if err := validateDimensions(ctx, tx, sample); err != nil {
			return IngestResult{}, err
		}
		// 用户×设备样本的 stat key 以设备为维度；入站样本的 user/device 均空。
		key := sample.DeviceID + "\x00" + sample.ProtocolID
		var previousUp, previousDown int64
		err := tx.QueryRowContext(ctx, `SELECT up_bytes,down_bytes FROM traffic_cursors WHERE node_id=? AND boot_id=? AND stat_key=?`, LocalNodeID, batch.BootID, key).Scan(&previousUp, &previousDown)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return IngestResult{}, err
		}
		if err == nil && (sample.UpBytes < previousUp || sample.DownBytes < previousDown) {
			return IngestResult{}, ErrCounterRollback
		}
		deltaUp, deltaDown := sample.UpBytes-previousUp, sample.DownBytes-previousDown
		_, err = tx.ExecContext(ctx, `INSERT INTO traffic_cursors(node_id,boot_id,stat_key,user_id,protocol_id,device_id,up_bytes,down_bytes,sequence,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(node_id,boot_id,stat_key) DO UPDATE SET up_bytes=excluded.up_bytes,down_bytes=excluded.down_bytes,sequence=excluded.sequence,updated_at=excluded.updated_at`, LocalNodeID, batch.BootID, key, sample.UserID, sample.ProtocolID, sample.DeviceID, sample.UpBytes, sample.DownBytes, batch.Sequence, common.UnixTime(now))
		if err != nil {
			return IngestResult{}, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO traffic_hourly(bucket_start,node_id,user_id,protocol_id,device_id,up_bytes,down_bytes,sample_count,updated_at) VALUES(?,?,?,?,?,?,?,1,?) ON CONFLICT(bucket_start,node_id,user_id,protocol_id,device_id) DO UPDATE SET up_bytes=up_bytes+excluded.up_bytes,down_bytes=down_bytes+excluded.down_bytes,sample_count=sample_count+1,updated_at=excluded.updated_at`, common.UnixTime(bucket), LocalNodeID, sample.UserID, sample.ProtocolID, sample.DeviceID, deltaUp, deltaDown, common.UnixTime(now))
		if err != nil {
			return IngestResult{}, err
		}
		if sample.DeviceID != "" && sample.UserID != "" && deltaUp+deltaDown > 0 {
			changedUsers[sample.UserID] = true
			// 设备累计与用户汇总同事务累加，保持「设备之和 = 用户总量」。
			if _, err = tx.ExecContext(ctx, `UPDATE devices SET used_bytes=used_bytes+? WHERE id=?`, deltaUp+deltaDown, sample.DeviceID); err != nil {
				return IngestResult{}, err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE users SET used_bytes=used_bytes+?,revision=revision+1,updated_at=? WHERE id=?`, deltaUp+deltaDown, common.UnixTime(now), sample.UserID); err != nil {
				return IngestResult{}, err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO traffic_boot_sequences(node_id,boot_id,last_sequence,updated_at) VALUES(?,?,?,?) ON CONFLICT(node_id,boot_id) DO UPDATE SET last_sequence=excluded.last_sequence,updated_at=excluded.updated_at`, LocalNodeID, batch.BootID, batch.Sequence, common.UnixTime(now))
	if err != nil {
		return IngestResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return IngestResult{}, err
	}
	users := make([]string, 0, len(changedUsers))
	for id := range changedUsers {
		users = append(users, id)
	}
	sort.Strings(users)
	return IngestResult{AcceptedBatchID: batch.ID, LastSequence: batch.Sequence, ChangedUsers: users}, nil
}

// ForgetStats drops the cursors of dimensions that no longer exist in the
// kernel (deleted devices/protocols). Without this, re-creating the same ID
// would restart its counters near zero and trip the counter-rollback guard on
// the next ingest.
func (s *Service) ForgetStats(ctx context.Context, deviceIDs, protocolIDs []string) error {
	if len(deviceIDs) == 0 && len(protocolIDs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, deviceID := range deviceIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM traffic_cursors WHERE node_id=? AND device_id=?`, LocalNodeID, deviceID); err != nil {
			return err
		}
	}
	for _, protocolID := range protocolIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM traffic_cursors WHERE node_id=? AND protocol_id=? AND user_id=''`, LocalNodeID, protocolID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func validateDimensions(ctx context.Context, tx *sql.Tx, sample Sample) error {
	for table, id := range map[string]string{"users": sample.UserID, "devices": sample.DeviceID, "protocols": sample.ProtocolID} {
		if id == "" {
			continue
		}
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM `+table+` WHERE id=?`, id).Scan(&exists); err != nil {
			return err
		}
		if exists != 1 {
			return fmt.Errorf("未知的 %s 维度 %q", table, id)
		}
	}
	return nil
}
