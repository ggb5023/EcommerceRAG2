package gateway

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

func insertConversation(ctx context.Context, db *pgxpool.Pool, tenantID, userID int64, shop, title string) (int64, error) {
	var id int64
	err := db.QueryRow(ctx, `INSERT INTO conversation(tenant_id,user_id,shop_id,title)
		VALUES($1,$2,$3,NULLIF($4,'')) RETURNING id`, tenantID, userID, shop, title).Scan(&id)
	return id, err
}
