package customer

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func emit(ctx context.Context, tx pgx.Tx, c conversation, kind string, data map[string]any) error {
	var seq int64
	if err := tx.QueryRow(ctx, `UPDATE customer_conversation SET last_sequence=last_sequence+1,updated_at=now() WHERE tenant_id=$1 AND id=$2 RETURNING last_sequence`, c.Tenant, c.ID).Scan(&seq); err != nil {
		return err
	}
	data["conversation_id"], data["epoch"], data["state"] = c.ID, c.Epoch, c.State
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO customer_event(tenant_id,conversation_id,sequence,event_type,data_json) VALUES($1,$2,$3,$4,$5)`, c.Tenant, c.ID, seq, kind, encoded)
	return err
}
func addMessage(ctx context.Context, tx pgx.Tx, c conversation, source, text, execution string, actor *int64, citations any) (string, error) {
	id := ID()
	if citations == nil {
		citations = []any{}
	}
	encoded, err := json.Marshal(citations)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO customer_message(id,tenant_id,conversation_id,source,content,execution_id,actor_user_id,citations_json) VALUES($1,$2,$3,$4,$5,NULLIF($6,''),$7,$8)`, id, c.Tenant, c.ID, source, text, execution, actor, encoded)
	if err == nil {
		err = emit(ctx, tx, c, "message", map[string]any{"message_id": id})
	}
	return id, err
}
func replay(ctx context.Context, tx pgx.Tx, c conversation, actor, key, payload string) (map[string]any, bool, error) {
	var hash string
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT payload_sha256,result_json FROM customer_command WHERE tenant_id=$1 AND conversation_id=$2 AND actor=$3 AND idempotency_key=$4`, c.Tenant, c.ID, actor, key).Scan(&hash, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if hash != Hash(payload) {
		return nil, false, errors.New("idempotency_conflict")
	}
	var result map[string]any
	err = json.Unmarshal(raw, &result)
	return result, true, err
}
func saveCommand(ctx context.Context, tx pgx.Tx, c conversation, actor, key, payload string, result map[string]any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO customer_command(tenant_id,conversation_id,actor,idempotency_key,payload_sha256,result_json) VALUES($1,$2,$3,$4,$5,$6)`, c.Tenant, c.ID, actor, key, Hash(payload), raw)
	return err
}
func (s *Service) submit(ctx context.Context, v visitor, conversationID, key, text string) (string, string, bool, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return "", "", false, err
	}
	defer tx.Rollback(ctx)
	c, err := scanConversation(tx.QueryRow(ctx, conversationSQL+" FOR UPDATE", conversationID))
	if err != nil {
		return "", "", false, err
	}
	if c.Tenant != v.Tenant || c.Shop != v.Shop || c.Customer != v.Customer {
		return "", "", false, errors.New("not_found")
	}
	actor, payload := "customer:"+v.Customer, "message:"+text
	result, found, err := replay(ctx, tx, c, actor, key, payload)
	if err != nil {
		return "", "", false, err
	}
	if found {
		return result["message_id"].(string), result["execution_id"].(string), true, nil
	}
	if c.State == "CLOSED" || (c.State == "AI" && c.Active != nil) {
		return "", "", false, errors.New("conversation_busy")
	}
	id, err := addMessage(ctx, tx, c, "customer", text, "", nil, nil)
	if err != nil {
		return "", "", false, err
	}
	execution := ""
	if c.State == "AI" {
		execution = ID()
		_, err = tx.Exec(ctx, `INSERT INTO customer_execution(id,tenant_id,conversation_id,epoch,session_id,is_mock) VALUES($1,$2,$3,$4,$5,$6)`, execution, c.Tenant, c.ID, c.Epoch, v.Session, s.Profile != "v2_langchain_bailian")
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE customer_conversation SET active_execution=$1 WHERE id=$2`, execution, c.ID)
		}
		if err != nil {
			return "", "", false, err
		}
	}
	result = map[string]any{"message_id": id, "execution_id": execution}
	if err = saveCommand(ctx, tx, c, actor, key, payload, result); err != nil {
		return "", "", false, err
	}
	return id, execution, false, tx.Commit(ctx)
}

func queueTx(ctx context.Context, tx pgx.Tx, c conversation, reason string) error {
	if c.State == "WAITING_HUMAN" || c.State == "CLOSED" {
		return nil
	}
	if c.Active != nil {
		if _, err := tx.Exec(ctx, `UPDATE customer_execution SET state='CANCELLED',reason_code=$2,finished_at=now() WHERE id=$1 AND state IN ('PENDING','EXECUTING')`, *c.Active, reason); err != nil {
			return err
		}
	}
	c.State = "WAITING_HUMAN"
	c.Epoch++
	_, err := tx.Exec(ctx, `UPDATE customer_conversation SET state=$2,epoch=$3,owner_user_id=NULL,owner_session_hash=NULL,lease_until=NULL,active_execution=NULL WHERE id=$1`, c.ID, c.State, c.Epoch)
	if err != nil {
		return err
	}
	if err = emit(ctx, tx, c, "state", map[string]any{"reason_code": reason}); err != nil {
		return err
	}
	_, err = addMessage(ctx, tx, c, "system", handoffText(reason), "", nil, nil)
	return err
}
func handoffText(reason string) string {
	prefix := "这个问题需要人工客服处理。"
	if strings.HasPrefix(reason, "provider_failed_") {
		reason = "provider_failed"
	}
	switch reason {
	case "knowledge_missing":
		prefix = "当前店铺知识没有足够依据回答。"
	case "conflicting_evidence":
		prefix = "现有资料存在冲突，暂时无法可靠回答。"
	case "unsupported_request":
		prefix = "当前知识客服不支持查询实时订单、库存、价格或作出配送承诺。"
	case "provider_failed", "gateway_restarted":
		prefix = "AI 服务暂时无法完成回答。"
	case "clarification_exhausted":
		prefix = "补充信息后仍不足以可靠回答。"
	}
	return prefix + "已进入人工客服等待队列，尚未有客服接单。您可以继续留言。"
}

func checkStaffTx(ctx context.Context, tx pgx.Tx, c conversation, user int64, admin bool) error {
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT u.revoked_at IS NULL AND t.status='active' AND sh.status='active'
 AND u.role=ANY($4::text[]) AND $3=ANY(u.shop_ids) FROM app_user u JOIN tenant t ON t.id=u.tenant_id
 JOIN shop sh ON sh.tenant_id=u.tenant_id AND sh.id=$3 WHERE u.tenant_id=$1 AND u.id=$2 FOR SHARE OF u,sh`, c.Tenant, user, c.Shop, func() []string {
		if admin {
			return []string{"owner", "admin"}
		}
		return []string{"owner", "admin", "operator"}
	}()).Scan(&allowed)
	if err != nil || !allowed {
		return errors.New("permission_changed")
	}
	return nil
}
func (s *Service) change(ctx context.Context, id, actor, key, action string, user int64, sessionHash string, epoch int64) (map[string]any, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	c, err := scanConversation(tx.QueryRow(ctx, conversationSQL+" FOR UPDATE", id))
	if err != nil {
		return nil, err
	}
	payload := action + ":" + strconv.FormatInt(epoch, 10)
	result, found, err := replay(ctx, tx, c, actor, key, payload)
	if err != nil || found {
		return result, err
	}
	if user > 0 {
		if err = checkStaffTx(ctx, tx, c, user, action == "requeue"); err != nil {
			return nil, err
		}
		if epoch != c.Epoch {
			return nil, errors.New("claim_stale")
		}
	} else if actor != "customer:"+c.Customer || action != "handoff" {
		return nil, errors.New("forbidden")
	}
	c, err = applyConversationAction(ctx, tx, c, id, action, user, sessionHash)
	if err != nil {
		return nil, err
	}
	result = map[string]any{"conversation_id": id, "state": c.State, "epoch": c.Epoch}
	if action != "heartbeat" && action != "handoff" && action != "release" && action != "requeue" {
		if err = emit(ctx, tx, c, "state", map[string]any{"actor_user_id": user, "action": action}); err != nil {
			return nil, err
		}
	}
	if err = saveCommand(ctx, tx, c, actor, key, payload, result); err != nil {
		return nil, err
	}
	return result, tx.Commit(ctx)
}
func applyConversationAction(ctx context.Context, tx pgx.Tx, c conversation, id, action string, user int64, sessionHash string) (conversation, error) {
	var err error
	switch action {
	case "handoff":
		if c.State == "AI" {
			err = queueTx(ctx, tx, c, "customer_requested")
			c.State = "WAITING_HUMAN"
			c.Epoch++
		}
	case "claim":
		if c.State != "WAITING_HUMAN" {
			return c, errors.New("claim_stale")
		}
		c.State = "HUMAN"
		c.Epoch++
		_, err = tx.Exec(ctx, `UPDATE customer_conversation SET state='HUMAN',epoch=$2,owner_user_id=$3,owner_session_hash=$4,lease_until=now()+interval '120 seconds' WHERE id=$1`, id, c.Epoch, user, sessionHash)
	case "heartbeat", "release", "resume-ai", "close":
		var owned bool
		err = tx.QueryRow(ctx, `SELECT state='HUMAN' AND owner_user_id=$2 AND owner_session_hash=$3 AND lease_until>now() FROM customer_conversation WHERE id=$1`, id, user, sessionHash).Scan(&owned)
		if err != nil || !owned {
			return c, errors.New("claim_stale")
		}
		if action == "heartbeat" {
			_, err = tx.Exec(ctx, `UPDATE customer_conversation SET lease_until=now()+interval '120 seconds' WHERE id=$1`, id)
		} else if action == "release" {
			err = queueTx(ctx, tx, c, "agent_released")
			c.State = "WAITING_HUMAN"
			c.Epoch++
		} else {
			c.State = "AI"
			if action == "close" {
				c.State = "CLOSED"
			}
			c.Epoch++
			_, err = tx.Exec(ctx, `UPDATE customer_conversation SET state=$2,epoch=$3,owner_user_id=NULL,owner_session_hash=NULL,lease_until=NULL,active_execution=NULL,clarification_count=0 WHERE id=$1`, id, c.State, c.Epoch)
		}
	case "requeue":
		if c.State != "HUMAN" {
			return c, errors.New("claim_stale")
		}
		err = queueTx(ctx, tx, c, "admin_requeued")
		c.State = "WAITING_HUMAN"
		c.Epoch++
	default:
		return c, errors.New("unsupported_action")
	}
	return c, err
}

func (s *Service) humanMessage(ctx context.Context, id string, staff Staff, key string, epoch int64, text string) (string, error) {
	if text == "" || len(text) > 8192 {
		return "", errors.New("invalid_text")
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	c, err := scanConversation(tx.QueryRow(ctx, conversationSQL+" FOR UPDATE", id))
	if err != nil {
		return "", err
	}
	if err = checkStaffTx(ctx, tx, c, staff.User, false); err != nil {
		return "", err
	}
	actor, payload := "staff:"+strconv.FormatInt(staff.User, 10), "message:"+strconv.FormatInt(epoch, 10)+":"+text
	result, found, err := replay(ctx, tx, c, actor, key, payload)
	if err != nil {
		return "", err
	}
	if found {
		return result["message_id"].(string), nil
	}
	var owned bool
	err = tx.QueryRow(ctx, `SELECT state='HUMAN' AND epoch=$2 AND owner_user_id=$3 AND owner_session_hash=$4 AND lease_until>now() FROM customer_conversation WHERE id=$1`, id, epoch, staff.User, staff.SessionHash).Scan(&owned)
	if err != nil || !owned {
		return "", errors.New("claim_stale")
	}
	message, err := addMessage(ctx, tx, c, "agent", text, "", &staff.User, nil)
	if err != nil {
		return "", err
	}
	if err = saveCommand(ctx, tx, c, actor, key, payload, map[string]any{"message_id": message}); err != nil {
		return "", err
	}
	return message, tx.Commit(ctx)
}

// Recover runs while the Gateway holds its database ownership lock and before
// readiness. Staff browser sessions do not survive a Gateway restart.
func (s *Service) Recover(ctx context.Context) error {
	return s.requeue(ctx, `state='HUMAN' OR (state='AI' AND active_execution IS NOT NULL)`, nil, "gateway_restarted")
}
func (s *Service) ReleaseSession(ctx context.Context, hash string) error {
	return s.requeue(ctx, `state='HUMAN' AND owner_session_hash=$1`, []any{hash}, "agent_logged_out")
}
func (s *Service) requeue(ctx context.Context, where string, args []any, reason string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, conversationSQL[:strings.Index(conversationSQL, " WHERE")]+" WHERE "+where+" FOR UPDATE", args...)
	if err != nil {
		return err
	}
	items := []conversation{}
	for rows.Next() {
		c, err := scanConversation(rows)
		if err != nil {
			rows.Close()
			return err
		}
		items = append(items, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, c := range items {
		if err = queueTx(ctx, tx, c, reason); err != nil {
			return err
		}
		s.cancel(c.ID)
	}
	return tx.Commit(ctx)
}
func (s *Service) Reap(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.requeue(ctx, `(state='AI' AND active_execution IS NOT NULL AND EXISTS(SELECT 1 FROM customer_execution e WHERE e.id=active_execution AND e.created_at<now()-interval '90 seconds')) OR state='HUMAN' AND (lease_until<=now() OR NOT EXISTS(SELECT 1 FROM app_user u WHERE u.tenant_id=customer_conversation.tenant_id AND u.id=owner_user_id AND u.revoked_at IS NULL AND u.role IN ('owner','admin','operator') AND customer_conversation.shop_id=ANY(u.shop_ids)))`, nil, "agent_unavailable")
		}
	}
}

// ErrNoCustomerMessage means an accessible support conversation has no customer
// message to use as a suggestion prompt.
var ErrNoCustomerMessage = errors.New("no_customer_message")

// ErrSupportConversationNotFound hides whether the conversation exists when the
// staff member is outside its tenant or shop.
var ErrSupportConversationNotFound = errors.New("support_conversation_not_found")

func lastCustomerMessage(ctx context.Context, db *pgxpool.Pool, tenantID int64, shop, conversationID string) (string, error) {
	var text string
	err := db.QueryRow(ctx, `SELECT m.content FROM customer_message m
		JOIN customer_conversation c ON c.tenant_id=m.tenant_id AND c.id=m.conversation_id
		WHERE m.tenant_id=$1 AND c.shop_id=$2 AND m.conversation_id=$3 AND m.source='customer'
		ORDER BY m.created_at DESC, m.id DESC LIMIT 1`, tenantID, shop, conversationID).Scan(&text)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNoCustomerMessage
	}
	return text, err
}

// conversationSQL is the canonical row shape for customer conversations. It
// lives in the store so service.go and ai.go hold no SQL literals.
const conversationSQL = `SELECT id,tenant_id,shop_id,customer_id,state,epoch,owner_user_id,lease_until,active_execution,clarification_count FROM customer_conversation WHERE id=$1`

func scanConversation(row pgx.Row) (conversation, error) {
	var c conversation
	err := row.Scan(&c.ID, &c.Tenant, &c.Shop, &c.Customer, &c.State, &c.Epoch, &c.Owner, &c.Lease, &c.Active, &c.Clarifications)
	return c, err
}

func customerSessionByToken(ctx context.Context, db *pgxpool.Pool, tokenHash string) (visitor, error) {
	var v visitor
	err := db.QueryRow(ctx, `SELECT cs.tenant_id,cs.shop_id,cs.customer_id,cs.id,cs.csrf_token
 FROM customer_session cs JOIN tenant t ON t.id=cs.tenant_id JOIN shop sh ON sh.tenant_id=cs.tenant_id AND sh.id=cs.shop_id
 WHERE cs.token_sha256=$1 AND cs.revoked_at IS NULL AND cs.expires_at>now() AND cs.idle_until>now()
 AND t.status='active' AND sh.status='active' AND sh.customer_chat_enabled`, tokenHash).Scan(&v.Tenant, &v.Shop, &v.Customer, &v.Session, &v.CSRF)
	return v, err
}

func touchCustomerSession(ctx context.Context, db *pgxpool.Pool, sessionID string) error {
	_, err := db.Exec(ctx, `UPDATE customer_session SET idle_until=LEAST(expires_at,now()+interval '24 hours') WHERE id=$1 AND idle_until<now()+interval '23 hours 55 minutes'`, sessionID)
	return err
}

func publicShop(ctx context.Context, db *pgxpool.Pool, slug string) (name, welcome string, err error) {
	err = db.QueryRow(ctx, `SELECT sh.name,sh.welcome_message FROM shop sh JOIN tenant t ON t.id=sh.tenant_id
 WHERE sh.public_slug=$1 AND sh.customer_chat_enabled AND sh.status='active' AND t.status='active'`, slug).Scan(&name, &welcome)
	return name, welcome, err
}

func sessionShop(ctx context.Context, db *pgxpool.Pool, slug string) (tenant int64, shop string, err error) {
	err = db.QueryRow(ctx, `SELECT sh.tenant_id,sh.id FROM shop sh JOIN tenant t ON t.id=sh.tenant_id WHERE sh.public_slug=$1 AND sh.customer_chat_enabled AND sh.status='active' AND t.status='active'`, slug).Scan(&tenant, &shop)
	return tenant, shop, err
}

func createCustomerSession(ctx context.Context, db *pgxpool.Pool, tenant int64, shop, customer, session, tokenHash, csrf string) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO customer_identity(id,tenant_id,shop_id) VALUES($1,$2,$3)`, customer, tenant, shop); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO customer_session(id,tenant_id,shop_id,customer_id,token_sha256,csrf_token,expires_at,idle_until)
 VALUES($1,$2,$3,$4,$5,$6,now()+interval '7 days',now()+interval '24 hours')`, session, tenant, shop, customer, tokenHash, csrf); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func upsertCustomerConversation(ctx context.Context, db *pgxpool.Pool, id string, tenant int64, shop, customer string) error {
	_, err := db.Exec(ctx, `INSERT INTO customer_conversation(id,tenant_id,shop_id,customer_id) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO NOTHING`, id, tenant, shop, customer)
	return err
}

func listCustomerConversations(ctx context.Context, db *pgxpool.Pool, tenant int64, shop, customer string) ([]map[string]any, error) {
	rows, err := db.Query(ctx, `SELECT id,state,epoch FROM customer_conversation WHERE tenant_id=$1 AND shop_id=$2 AND customer_id=$3 ORDER BY updated_at DESC LIMIT 100`, tenant, shop, customer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var id, state string
		var epoch int64
		if rows.Scan(&id, &state, &epoch) != nil {
			return nil, errors.New("conversation_scan_failed")
		}
		result = append(result, map[string]any{"id": id, "state": state, "epoch": epoch})
	}
	return result, nil
}

func loadConversation(ctx context.Context, db *pgxpool.Pool, id string) (conversation, error) {
	return scanConversation(db.QueryRow(ctx, conversationSQL, id))
}

func lockConversation(ctx context.Context, tx pgx.Tx, id string) (conversation, error) {
	return scanConversation(tx.QueryRow(ctx, conversationSQL+" FOR UPDATE", id))
}

func customerConversationOwned(ctx context.Context, db *pgxpool.Pool, id string, user int64, sessionHash string) (bool, error) {
	var owned bool
	err := db.QueryRow(ctx, `SELECT state='HUMAN' AND owner_user_id=$2 AND owner_session_hash=$3 AND lease_until>now() FROM customer_conversation WHERE id=$1`, id, user, sessionHash).Scan(&owned)
	return owned, err
}

func loadStaffQueue(ctx context.Context, db *pgxpool.Pool, tenant int64, shops []string, user int64) ([]map[string]any, error) {
	rows, err := db.Query(ctx, `SELECT id,state,epoch,owner_user_id,lease_until,updated_at FROM customer_conversation WHERE tenant_id=$1 AND shop_id=ANY($2::text[]) AND (state='WAITING_HUMAN' OR (state='HUMAN' AND owner_user_id=$3)) ORDER BY updated_at LIMIT 100`, tenant, shops, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, state string
		var epoch int64
		var owner *int64
		var lease *time.Time
		var updated time.Time
		if rows.Scan(&id, &state, &epoch, &owner, &lease, &updated) != nil {
			return nil, errors.New("queue_scan_failed")
		}
		items = append(items, map[string]any{"id": id, "state": state, "epoch": epoch, "owner_user_id": owner, "lease_until": lease, "updated_at": updated})
	}
	return items, nil
}

type customerEvent struct {
	seq  int64
	kind string
	data []byte
}

func loadCustomerEvents(ctx context.Context, db *pgxpool.Pool, tenant int64, conversationID string, after int64, limit int) ([]customerEvent, error) {
	rows, err := db.Query(ctx, `SELECT sequence,event_type,data_json FROM customer_event WHERE tenant_id=$1 AND conversation_id=$2 AND sequence>$3 ORDER BY sequence LIMIT $4`, tenant, conversationID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	batch := []customerEvent{}
	for rows.Next() {
		var event customerEvent
		if rows.Scan(&event.seq, &event.kind, &event.data) != nil {
			return nil, errors.New("event_scan_failed")
		}
		batch = append(batch, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return batch, nil
}

func loadCustomerMessage(ctx context.Context, db *pgxpool.Pool, tenant int64, conversationID, id string) (source, content string, citations []byte, created time.Time, state, behavior string, mock bool, err error) {
	err = db.QueryRow(ctx, `SELECT m.source,m.content,m.citations_json,m.created_at,
		COALESCE(e.state,''),COALESCE(e.behavior,''),COALESCE(e.is_mock,false)
		FROM customer_message m LEFT JOIN customer_execution e ON e.tenant_id=m.tenant_id AND e.id=m.execution_id
		WHERE m.tenant_id=$1 AND m.conversation_id=$2 AND m.id=$3`,
		tenant, conversationID, id).Scan(&source, &content, &citations, &created, &state, &behavior, &mock)
	return
}

func loadDocumentTitle(ctx context.Context, db *pgxpool.Pool, tenant int64, logicalKey string) (string, error) {
	var title string
	err := db.QueryRow(ctx, `SELECT title FROM document WHERE tenant_id=$1 AND logical_key=$2`, tenant, logicalKey).Scan(&title)
	return title, err
}

func loadCustomerMessageIDs(ctx context.Context, db *pgxpool.Pool, tenant int64, conversationID string) ([]string, error) {
	rows, err := db.Query(ctx, `SELECT id FROM customer_message WHERE tenant_id=$1 AND conversation_id=$2 ORDER BY created_at DESC,id DESC LIMIT 500`, tenant, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			return nil, errors.New("message_scan_failed")
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

func loadConversationScope(ctx context.Context, db *pgxpool.Pool, id string) (tenant int64, shop string, err error) {
	err = db.QueryRow(ctx, `SELECT tenant_id,shop_id FROM customer_conversation WHERE id=$1`, id).Scan(&tenant, &shop)
	return tenant, shop, err
}

func markCustomerExecutionExecuting(ctx context.Context, db *pgxpool.Pool, execution string) error {
	_, err := db.Exec(ctx, `UPDATE customer_execution SET state='EXECUTING' WHERE id=$1 AND state='PENDING'`, execution)
	return err
}

func loadCustomerHistoryTexts(ctx context.Context, db *pgxpool.Pool, conversationID string) ([]string, error) {
	rows, err := db.Query(ctx, `SELECT content FROM customer_message WHERE conversation_id=$1 AND source='customer' ORDER BY created_at DESC,id DESC LIMIT 7`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	texts := []string{}
	for rows.Next() {
		var text string
		if rows.Scan(&text) != nil {
			return nil, errors.New("history_scan_failed")
		}
		texts = append(texts, text)
	}
	return texts, nil
}

func lockCustomerExecution(ctx context.Context, tx pgx.Tx, execution string) (int64, string, error) {
	var epoch int64
	var state string
	err := tx.QueryRow(ctx, `SELECT epoch,state FROM customer_execution WHERE id=$1 FOR UPDATE`, execution).Scan(&epoch, &state)
	return epoch, state, err
}

func lockShopPublicRevision(ctx context.Context, tx pgx.Tx, tenant int64, shop string) (string, error) {
	var revision string
	err := tx.QueryRow(ctx, `SELECT public_permission_revision FROM shop WHERE tenant_id=$1 AND id=$2 AND status='active' AND customer_chat_enabled FOR SHARE`, tenant, shop).Scan(&revision)
	return revision, err
}

func lockCustomerSessionActive(ctx context.Context, tx pgx.Tx, sessionID string) (bool, error) {
	var active bool
	err := tx.QueryRow(ctx, `SELECT revoked_at IS NULL AND expires_at>now() AND idle_until>now() FROM customer_session WHERE id=$1 FOR SHARE`, sessionID).Scan(&active)
	return active, err
}

func failCustomerExecution(ctx context.Context, tx pgx.Tx, execution, reason string) error {
	_, err := tx.Exec(ctx, `UPDATE customer_execution SET state='FAILED',reason_code=$2,finished_at=now() WHERE id=$1`, execution, reason)
	return err
}

func completeCustomerExecution(ctx context.Context, tx pgx.Tx, execution, state, behavior, reason, answer string, evidence []byte, mock bool) error {
	_, err := tx.Exec(ctx, `UPDATE customer_execution SET state=$2,behavior=$3,reason_code=$4,answer=$5,evidence_json=$6,is_mock=$7,finished_at=now() WHERE id=$1`, execution, state, behavior, reason, answer, evidence, mock)
	return err
}

func clearCustomerActiveExecution(ctx context.Context, tx pgx.Tx, id string, clarifications int) error {
	_, err := tx.Exec(ctx, `UPDATE customer_conversation SET active_execution=NULL,clarification_count=$2 WHERE id=$1`, id, clarifications)
	return err
}
