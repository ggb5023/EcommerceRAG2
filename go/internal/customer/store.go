package customer

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
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
	switch action {
	case "handoff":
		if c.State == "AI" {
			err = queueTx(ctx, tx, c, "customer_requested")
			c.State = "WAITING_HUMAN"
			c.Epoch++
		}
	case "claim":
		if c.State != "WAITING_HUMAN" {
			return nil, errors.New("claim_stale")
		}
		c.State = "HUMAN"
		c.Epoch++
		_, err = tx.Exec(ctx, `UPDATE customer_conversation SET state='HUMAN',epoch=$2,owner_user_id=$3,owner_session_hash=$4,lease_until=now()+interval '120 seconds' WHERE id=$1`, id, c.Epoch, user, sessionHash)
	case "heartbeat", "release", "resume-ai", "close":
		var owned bool
		err = tx.QueryRow(ctx, `SELECT state='HUMAN' AND owner_user_id=$2 AND owner_session_hash=$3 AND lease_until>now() FROM customer_conversation WHERE id=$1`, id, user, sessionHash).Scan(&owned)
		if err != nil || !owned {
			return nil, errors.New("claim_stale")
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
			return nil, errors.New("claim_stale")
		}
		err = queueTx(ctx, tx, c, "admin_requeued")
		c.State = "WAITING_HUMAN"
		c.Epoch++
	default:
		return nil, errors.New("unsupported_action")
	}
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
			_ = s.requeue(ctx, `state='HUMAN' AND (lease_until<=now() OR NOT EXISTS(SELECT 1 FROM app_user u WHERE u.tenant_id=customer_conversation.tenant_id AND u.id=owner_user_id AND u.revoked_at IS NULL AND u.role IN ('owner','admin','operator') AND customer_conversation.shop_id=ANY(u.shop_ids)))`, nil, "agent_unavailable")
		}
	}
}
