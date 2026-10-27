package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var (
	ErrNoSuchGroup = errors.New("no such group")
	ErrGroupExists = errors.New("group already exists")
)

// Group gathers access keys that share one scope.
type Group struct {
	Name        string
	Description string
	Enabled     bool
	Created     time.Time
	Scope       string   // opaque to storage
	Members     []string // access keys, sorted
}

const groupColumns = `name, description, enabled, created_at, scope`

func scanGroup(scan func(...any) error) (Group, error) {
	var g Group
	var created int64
	err := scan(&g.Name, &g.Description, &g.Enabled, &created, &g.Scope)
	g.Created = fromNanos(created)
	return g, err
}

// CreateGroup stores a new group with its members.
func (s *Store) CreateGroup(ctx context.Context, g Group) error {
	return s.tx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var one int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM groups WHERE name = ?`, g.Name).Scan(&one); err == nil {
			return ErrGroupExists
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO groups (`+groupColumns+`) VALUES (?, ?, ?, ?, ?)`,
			g.Name, g.Description, g.Enabled, s.nowNanos(), g.Scope); err != nil {
			return err
		}
		return setMembers(ctx, tx, g.Name, g.Members)
	})
}

// GetGroup returns one group with its members.
func (s *Store) GetGroup(ctx context.Context, name string) (Group, error) {
	g, err := scanGroup(s.read.QueryRowContext(ctx, `SELECT `+groupColumns+` FROM groups WHERE name = ?`, name).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Group{}, ErrNoSuchGroup
	}
	if err != nil {
		return Group{}, err
	}
	members, err := s.groupMembers(ctx)
	g.Members = members[g.Name]
	return g, err
}

// ListGroups returns every group with its members, by name.
func (s *Store) ListGroups(ctx context.Context) ([]Group, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT `+groupColumns+` FROM groups ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		g, err := scanGroup(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	members, err := s.groupMembers(ctx)
	for i := range out {
		out[i].Members = members[out[i].Name]
	}
	return out, err
}

// groupMembers maps each group to its sorted member keys.
func (s *Store) groupMembers(ctx context.Context) (map[string][]string, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT group_name, access_key FROM group_members ORDER BY group_name, access_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var g, k string
		if err := rows.Scan(&g, &k); err != nil {
			return nil, err
		}
		out[g] = append(out[g], k)
	}
	return out, rows.Err()
}

// UpdateGroup applies fn to a group and saves it, members included. The
// name and creation time cannot change.
func (s *Store) UpdateGroup(ctx context.Context, name string, fn func(*Group) error) (Group, error) {
	var g Group
	err := s.tx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		g, err = scanGroup(tx.QueryRowContext(ctx, `SELECT `+groupColumns+` FROM groups WHERE name = ?`, name).Scan)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoSuchGroup
		} else if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT access_key FROM group_members WHERE group_name = ? ORDER BY access_key`, name)
		if err != nil {
			return err
		}
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err != nil {
				rows.Close()
				return err
			}
			g.Members = append(g.Members, k)
		}
		rows.Close()
		if err := fn(&g); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE groups SET description = ?, enabled = ?, scope = ? WHERE name = ?`,
			g.Description, g.Enabled, g.Scope, name); err != nil {
			return err
		}
		return setMembers(ctx, tx, name, g.Members)
	})
	return g, err
}

// setMembers replaces the members of a group.
func setMembers(ctx context.Context, tx *sql.Tx, group string, keys []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM group_members WHERE group_name = ?`, group); err != nil {
		return err
	}
	for _, k := range keys {
		var one int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM access_keys WHERE access_key = ? AND internal = 0`, k).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNoSuchAccessKey
			}
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO group_members (group_name, access_key) VALUES (?, ?)`, group, k); err != nil {
			return err
		}
	}
	return nil
}

// DeleteGroup removes a group; its members keep their own scope again.
func (s *Store) DeleteGroup(ctx context.Context, name string) error {
	res, err := s.exec(ctx, `DELETE FROM groups WHERE name = ?`, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSuchGroup
	}
	return nil
}

// KeyGroups returns the groups an access key belongs to, enabled or not, by name.
func (s *Store) KeyGroups(ctx context.Context, accessKey string) ([]Group, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT g.name, g.description, g.enabled, g.created_at, g.scope
		FROM groups g JOIN group_members m ON m.group_name = g.name
		WHERE m.access_key = ? ORDER BY g.name`, accessKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		g, err := scanGroup(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SetKeyGroups makes an access key a member of exactly the given groups.
func (s *Store) SetKeyGroups(ctx context.Context, accessKey string, groups []string) error {
	return s.tx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var one int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM access_keys WHERE access_key = ? AND internal = 0`, accessKey).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNoSuchAccessKey
			}
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM group_members WHERE access_key = ?`, accessKey); err != nil {
			return err
		}
		for _, g := range groups {
			if err := tx.QueryRowContext(ctx, `SELECT 1 FROM groups WHERE name = ?`, g).Scan(&one); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return ErrNoSuchGroup
				}
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO group_members (group_name, access_key) VALUES (?, ?)`, g, accessKey); err != nil {
				return err
			}
		}
		return nil
	})
}
