package policy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/keepon-online/sb-ops/internal/service/common"
)

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) CreatePolicy(ctx context.Context, value Policy) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO policies(id,name,description,enabled,default_action,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		value.ID, value.Name, value.Description, value.Enabled, value.DefaultAction, value.Revision, common.UnixTime(value.CreatedAt), common.UnixTime(value.UpdatedAt))
	return mapError(err)
}

func scanPolicy(row common.Scanner) (Policy, error) {
	var value Policy
	var enabled int
	var createdAt, updatedAt common.Time
	if err := row.Scan(&value.ID, &value.Name, &value.Description, &enabled, &value.DefaultAction, &value.Revision, &createdAt, &updatedAt); err != nil {
		return Policy{}, err
	}
	value.Enabled = enabled != 0
	value.CreatedAt, value.UpdatedAt = createdAt.Time, updatedAt.Time
	return value, nil
}

func (s *Store) GetPolicy(ctx context.Context, id string) (Policy, error) {
	value, err := scanPolicy(s.db.QueryRowContext(ctx, `SELECT id,name,description,enabled,default_action,revision,created_at,updated_at FROM policies WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Policy{}, common.ErrNotFound
	}
	return value, err
}

func (s *Store) ListPolicies(ctx context.Context) ([]Policy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,description,enabled,default_action,revision,created_at,updated_at FROM policies ORDER BY name COLLATE NOCASE,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []Policy{}
	for rows.Next() {
		value, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) UpdatePolicy(ctx context.Context, value Policy, expectedRevision int64) error {
	result, err := s.db.ExecContext(ctx, `UPDATE policies SET name=?,description=?,enabled=?,default_action=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?`,
		value.Name, value.Description, value.Enabled, value.DefaultAction, common.UnixTime(value.UpdatedAt), value.ID, expectedRevision)
	if err != nil {
		return mapError(err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return common.ErrRevisionConflict
	}
	return nil
}

func (s *Store) DeletePolicy(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM policies WHERE id=?`, id)
	if err != nil {
		return common.MapSQLiteError(err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return common.ErrNotFound
	}
	return nil
}

func (s *Store) AssignPolicyToUsersWithoutPolicies(ctx context.Context, policyID string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT OR IGNORE INTO user_policies(user_id,policy_id)
SELECT u.id,? FROM users u
WHERE NOT EXISTS (SELECT 1 FROM user_policies up WHERE up.user_id=u.id)`, policyID)
	return err
}

func (s *Store) CreateRule(ctx context.Context, value Rule) error {
	conditions, err := json.Marshal(value.Conditions)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO rules(id,policy_id,name,enabled,priority,conditions_json,action,no_resolve,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		value.ID, value.PolicyID, value.Name, value.Enabled, value.Priority, conditions, value.Action, value.NoResolve, value.Revision, common.UnixTime(value.CreatedAt), common.UnixTime(value.UpdatedAt))
	return mapError(err)
}

func scanRule(row common.Scanner) (Rule, error) {
	var value Rule
	var enabled, noResolve int
	var conditions string
	var createdAt, updatedAt common.Time
	if err := row.Scan(&value.ID, &value.PolicyID, &value.Name, &enabled, &value.Priority, &conditions, &value.Action, &noResolve, &value.Revision, &createdAt, &updatedAt); err != nil {
		return Rule{}, err
	}
	value.Enabled, value.NoResolve = enabled != 0, noResolve != 0
	if err := json.Unmarshal([]byte(conditions), &value.Conditions); err != nil {
		return Rule{}, err
	}
	value.CreatedAt, value.UpdatedAt = createdAt.Time, updatedAt.Time
	return value, nil
}

func (s *Store) GetRule(ctx context.Context, policyID, id string) (Rule, error) {
	value, err := scanRule(s.db.QueryRowContext(ctx, `SELECT id,policy_id,name,enabled,priority,conditions_json,action,no_resolve,revision,created_at,updated_at FROM rules WHERE policy_id=? AND id=?`, policyID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Rule{}, common.ErrNotFound
	}
	return value, err
}

func (s *Store) ListRules(ctx context.Context, policyID string) ([]Rule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,policy_id,name,enabled,priority,conditions_json,action,no_resolve,revision,created_at,updated_at FROM rules WHERE policy_id=? ORDER BY priority DESC,created_at,id`, policyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []Rule{}
	for rows.Next() {
		value, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) UpdateRule(ctx context.Context, value Rule, expectedRevision int64) error {
	conditions, err := json.Marshal(value.Conditions)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE rules SET name=?,enabled=?,priority=?,conditions_json=?,action=?,no_resolve=?,revision=revision+1,updated_at=? WHERE policy_id=? AND id=? AND revision=?`,
		value.Name, value.Enabled, value.Priority, conditions, value.Action, value.NoResolve, common.UnixTime(value.UpdatedAt), value.PolicyID, value.ID, expectedRevision)
	if err != nil {
		return mapError(err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return common.ErrRevisionConflict
	}
	return nil
}

func (s *Store) DeleteRule(ctx context.Context, policyID, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM rules WHERE policy_id=? AND id=?`, policyID, id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return common.ErrNotFound
	}
	return nil
}

func (s *Store) OrderRules(ctx context.Context, policyID string, ids []string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM rules WHERE policy_id=?`, policyID).Scan(&count); err != nil {
		return err
	}
	if count != len(ids) {
		return &common.ValidationError{Fields: map[string]string{"ids": "must contain every policy rule exactly once"}}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE rules SET priority=-priority-1 WHERE policy_id=?`, policyID); err != nil {
		return err
	}
	seen := map[string]bool{}
	for index, id := range ids {
		if seen[id] {
			return &common.ValidationError{Fields: map[string]string{"ids": "contains duplicate rule IDs"}}
		}
		seen[id] = true
		priority := (len(ids) - index) * 10
		result, err := tx.ExecContext(ctx, `UPDATE rules SET priority=?,revision=revision+1,updated_at=? WHERE policy_id=? AND id=?`, priority, common.UnixTime(now), policyID, id)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed == 0 {
			return common.ErrNotFound
		}
	}
	return tx.Commit()
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "rules.policy_id, rules.priority") {
		return &common.ValidationError{Fields: map[string]string{"priority": "is already used in this policy"}}
	}
	return common.MapSQLiteError(err)
}
