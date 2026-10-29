package admin

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Shop domain: one read for the listings and the single write the settings
// endpoint performs. Domain files keep reads and writes together so the whole
// surface of a table lives in one place.

// ShopRow is one row of the shop listing.
type ShopRow struct {
	ID                  string
	Name                string
	Status              string
	PublicSlug          string
	WelcomeMessage      string
	CustomerChatEnabled bool
}

// Shops lists the shops inside the caller's scope.
func (s *Store) Shops(ctx context.Context, db *pgxpool.Pool, tenantID int64, shops []string) ([]ShopRow, error) {
	rows, err := db.Query(ctx, `SELECT id,name,status,COALESCE(public_slug,''),welcome_message,customer_chat_enabled FROM shop WHERE tenant_id=$1 AND id=ANY($2::text[]) ORDER BY id`,
		tenantID, shops)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ShopRow, 0)
	for rows.Next() {
		var row ShopRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Status, &row.PublicSlug, &row.WelcomeMessage,
			&row.CustomerChatEnabled); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// UpdateShopSettings rewrites the merchant-facing channel settings of one shop.
// It returns the affected row count so the caller keeps its conflict check.
func (s *Store) UpdateShopSettings(ctx context.Context, tx pgx.Tx, tenantID int64, shopID, name, slug, welcome string, enabled bool) (int64, error) {
	if tx == nil {
		return 0, ErrStoreNeedsTransaction
	}
	result, err := tx.Exec(ctx, `UPDATE shop SET name=$3,public_slug=$4,welcome_message=$5,customer_chat_enabled=$6 WHERE tenant_id=$1 AND id=$2`,
		tenantID, shopID, name, slug, welcome, enabled)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}
