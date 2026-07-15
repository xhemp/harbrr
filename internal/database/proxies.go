package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
)

// Proxies is the SQLite repository for global proxy resources. Like the other
// resource repos it is stateless (every method takes an Execer, so it runs
// standalone or inside a transaction) and stores the opaque (already-encrypted)
// password; encryption is the service's concern.
type Proxies struct{}

// proxyColumns is the full select list, in scan order. url_encrypted is
// deliberately excluded — it is legacy, pre-#71 storage the backfill alone reads
// (via ProxiesPendingSplit), not part of the current proxy shape.
const proxyColumns = `id, name, type, host, port, username, password_encrypted, key_id, created_at, updated_at`

// InsertProxy writes a proxy row and returns its new id. The row is inserted with
// an empty password_encrypted so its id can bind the encryption AAD; the service
// writes the sealed password back via SetProxySecret in the same tx.
func (Proxies) InsertProxy(ctx context.Context, q dbinterface.Execer, p domain.Proxy) (int64, error) {
	res, err := q.ExecContext(ctx,
		q.Rebind(`INSERT INTO proxies (name, type, host, port, username, password_encrypted, key_id, url_encrypted, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, '', ?, ?)`),
		p.Name, p.Type, p.Host, p.Port, p.Username, p.PasswordEncrypted, p.KeyID,
		p.CreatedAt.UTC().Format(timeLayout), p.UpdatedAt.UTC().Format(timeLayout))
	if err != nil {
		return 0, fmt.Errorf("database: insert proxy: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("database: proxy last insert id: %w", err)
	}
	return id, nil
}

// GetProxy returns the proxy with the given id, or ErrNotFound.
func (Proxies) GetProxy(ctx context.Context, q dbinterface.Execer, id int64) (domain.Proxy, error) {
	row := q.QueryRowContext(ctx, q.Rebind(`SELECT `+proxyColumns+` FROM proxies WHERE id = ?`), id)
	p, err := scanProxy(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Proxy{}, fmt.Errorf("proxy %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return domain.Proxy{}, fmt.Errorf("database: scan proxy %d: %w", id, err)
	}
	return p, nil
}

// ListProxies returns all proxies ordered by id.
func (Proxies) ListProxies(ctx context.Context, q dbinterface.Execer) ([]domain.Proxy, error) {
	rows, err := q.QueryContext(ctx, q.Rebind(`SELECT `+proxyColumns+` FROM proxies ORDER BY id`))
	if err != nil {
		return nil, fmt.Errorf("database: list proxies: %w", err)
	}
	defer rows.Close()

	var out []domain.Proxy
	for rows.Next() {
		p, err := scanProxy(rows)
		if err != nil {
			return nil, fmt.Errorf("database: scan proxy row: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate proxies: %w", err)
	}
	return out, nil
}

// UpdateProxy writes a proxy's mutable fields (name, type, host, port, username,
// the re-encrypted password, key_id) by id, returning ErrNotFound when no row
// matches.
func (Proxies) UpdateProxy(ctx context.Context, q dbinterface.Execer, p domain.Proxy) error {
	res, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE proxies SET name = ?, type = ?, host = ?, port = ?, username = ?, password_encrypted = ?, key_id = ?, updated_at = ?
			WHERE id = ?`),
		p.Name, p.Type, p.Host, p.Port, p.Username, p.PasswordEncrypted, p.KeyID, p.UpdatedAt.UTC().Format(timeLayout), p.ID)
	if err != nil {
		return fmt.Errorf("database: update proxy: %w", err)
	}
	return affectedOrNotFoundID(res, p.ID)
}

// SetProxySecret writes the encrypted password column + key_id by id (phase two
// of the insert-then-seal write, so the credential binds to the freshly-minted
// row id).
func (Proxies) SetProxySecret(ctx context.Context, q dbinterface.Execer, id int64, passwordEncrypted, keyID string) error {
	res, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE proxies SET password_encrypted = ?, key_id = ? WHERE id = ?`),
		passwordEncrypted, keyID, id)
	if err != nil {
		return fmt.Errorf("database: set proxy secret: %w", err)
	}
	return affectedOrNotFoundID(res, id)
}

// SetProxyLegacyURL writes the encrypted composite URL into the legacy
// url_encrypted column (phase two of the insert-then-seal write, pre-#71 shape).
// Used only by resourcemigrate's fold of inline instance settings into a shared
// proxy row; the boot backfill (SplitProxyURL, run right after on the same boot)
// converts it into structured fields.
func (Proxies) SetProxyLegacyURL(ctx context.Context, q dbinterface.Execer, id int64, urlEncrypted string) error {
	res, err := q.ExecContext(ctx, q.Rebind(`UPDATE proxies SET url_encrypted = ? WHERE id = ?`), urlEncrypted, id)
	if err != nil {
		return fmt.Errorf("database: set proxy legacy url: %w", err)
	}
	return affectedOrNotFoundID(res, id)
}

// DeleteProxy removes a proxy by id, returning ErrNotFound when absent. Referencing
// instances' proxy_id is nulled by the ON DELETE SET NULL foreign key.
func (Proxies) DeleteProxy(ctx context.Context, q dbinterface.Execer, id int64) error {
	res, err := q.ExecContext(ctx, q.Rebind(`DELETE FROM proxies WHERE id = ?`), id)
	if err != nil {
		return fmt.Errorf("database: delete proxy: %w", err)
	}
	return affectedOrNotFoundID(res, id)
}

// LegacyProxyURL is one row pending the #71 URL→structured-fields backfill: it
// still holds a legacy encrypted composite URL and no host yet.
type LegacyProxyURL struct {
	ID           int64
	URLEncrypted string
}

// ProxiesPendingSplit returns proxies with a legacy url_encrypted still set and no
// host yet — the backfill's work list. Naturally idempotent: SplitProxyURL sets
// host and clears url_encrypted back to ” in the same write, so a split row never
// matches this query again.
func (Proxies) ProxiesPendingSplit(ctx context.Context, q dbinterface.Execer) ([]LegacyProxyURL, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, url_encrypted FROM proxies WHERE host = '' AND url_encrypted != ''`)
	if err != nil {
		return nil, fmt.Errorf("database: list proxies pending split: %w", err)
	}
	defer rows.Close()

	var out []LegacyProxyURL
	for rows.Next() {
		var l LegacyProxyURL
		if err := rows.Scan(&l.ID, &l.URLEncrypted); err != nil {
			return nil, fmt.Errorf("database: scan legacy proxy row: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate legacy proxies: %w", err)
	}
	return out, nil
}

// SplitProxyURL writes one proxy's backfilled structured fields and clears its
// legacy url_encrypted, in a single update (so a crash between the two never
// leaves a row with both a stored URL and a stored host).
func (Proxies) SplitProxyURL(ctx context.Context, q dbinterface.Execer, id int64, host string, port int, username, passwordEncrypted, keyID string) error {
	res, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE proxies SET host = ?, port = ?, username = ?, password_encrypted = ?, key_id = ?, url_encrypted = '' WHERE id = ?`),
		host, port, username, passwordEncrypted, keyID, id)
	if err != nil {
		return fmt.Errorf("database: split proxy url: %w", err)
	}
	return affectedOrNotFoundID(res, id)
}

// scanProxy reads one proxies row from a *sql.Row or *sql.Rows.
func scanProxy(s interface{ Scan(...any) error }) (domain.Proxy, error) {
	var (
		p                    domain.Proxy
		createdAt, updatedAt string
	)
	if err := s.Scan(&p.ID, &p.Name, &p.Type, &p.Host, &p.Port, &p.Username, &p.PasswordEncrypted, &p.KeyID, &createdAt, &updatedAt); err != nil {
		return domain.Proxy{}, err //nolint:wrapcheck // sql.ErrNoRows matched by caller; others wrapped there.
	}
	p.CreatedAt, p.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
	return p, nil
}
