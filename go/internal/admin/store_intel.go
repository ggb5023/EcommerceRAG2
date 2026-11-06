package admin

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	errIntelWatchConfigNotFound = errors.New("intel watch config not found")
	errIntelWatchConfigExists   = errors.New("intel watch config exists")
)

type IntelWatchConfigRow struct {
	ID        int64
	Market    string
	Category  string
	Platform  string
	Enabled   bool
	CreatedAt time.Time
}

const intelWatchConfigColumns = `id,market,category,platform,enabled,created_at`

func scanIntelWatchConfig(scanner interface{ Scan(...any) error }) (IntelWatchConfigRow, error) {
	var row IntelWatchConfigRow
	err := scanner.Scan(&row.ID, &row.Market, &row.Category, &row.Platform, &row.Enabled, &row.CreatedAt)
	return row, err
}

// IntelWatchConfigs lists the merchant's own watch targets. The tenant comes
// from the admin session; the shared observation table is never touched here.
func (s *Store) IntelWatchConfigs(ctx context.Context, db *pgxpool.Pool, tenantID int64) ([]IntelWatchConfigRow, error) {
	rows, err := db.Query(ctx, `SELECT `+intelWatchConfigColumns+`
		FROM intel_watch_config WHERE tenant_id=$1 ORDER BY id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]IntelWatchConfigRow, 0)
	for rows.Next() {
		row, err := scanIntelWatchConfig(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// CreateIntelWatchConfig adds one target for the tenant. A duplicate target is
// reported as a conflict, not silently merged.
func (s *Store) CreateIntelWatchConfig(ctx context.Context, db *pgxpool.Pool, tenantID int64,
	market, category, platform string, enabled bool) (IntelWatchConfigRow, error) {
	row, err := scanIntelWatchConfig(db.QueryRow(ctx, `INSERT INTO intel_watch_config(
			tenant_id,market,category,platform,enabled)
		VALUES($1,$2,$3,$4,$5)
		RETURNING `+intelWatchConfigColumns,
		tenantID, market, category, platform, enabled))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return row, errIntelWatchConfigExists
	}
	return row, err
}

// UpdateIntelWatchConfig changes only the supplied fields. Market/category/
// platform move together in practice; the COALESCE keeps PATCH semantics.
func (s *Store) UpdateIntelWatchConfig(ctx context.Context, db *pgxpool.Pool, tenantID, id int64,
	market, category, platform *string, enabled *bool) (IntelWatchConfigRow, error) {
	row, err := scanIntelWatchConfig(db.QueryRow(ctx, `UPDATE intel_watch_config
		SET market=COALESCE($3,market),category=COALESCE($4,category),
			platform=COALESCE($5,platform),enabled=COALESCE($6,enabled)
		WHERE tenant_id=$1 AND id=$2
		RETURNING `+intelWatchConfigColumns,
		tenantID, id, market, category, platform, enabled))
	if errors.Is(err, pgx.ErrNoRows) {
		return row, errIntelWatchConfigNotFound
	}
	return row, err
}
