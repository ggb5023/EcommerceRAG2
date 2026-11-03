package admin

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestCustomerHandoffSQLCounts exercises the read-only query against real
// customer tables: one handed-off conversation, one message-only conversation,
// then a window with no customer messages to prove the null denominator.
func TestCustomerHandoffSQLCounts(t *testing.T) {
	_, migration, gateway, _, handler := integrationHandlerWithIngest(t, false)
	ctx := context.Background()
	tenant := handler.Session.TenantID
	shop := "shop-import"
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	handoffConversation := "handoff-conv-" + suffix
	messageConversation := "message-conv-" + suffix
	customer := "handoff-customer-" + suffix
	t.Cleanup(func() {
		for _, statement := range []string{
			`DELETE FROM customer_event WHERE tenant_id=$1`,
			`DELETE FROM customer_message WHERE tenant_id=$1`,
			`DELETE FROM customer_conversation WHERE tenant_id=$1`,
			`DELETE FROM customer_identity WHERE tenant_id=$1`,
		} {
			if _, err := migration.Exec(context.Background(), statement, tenant); err != nil {
				t.Logf("handoff cleanup skipped: %v", err)
			}
		}
	})
	if _, err := migration.Exec(ctx, `INSERT INTO customer_identity(id,tenant_id,shop_id) VALUES($1,$2,$3)`,
		customer, tenant, shop); err != nil {
		t.Fatal(err)
	}
	for _, conversationID := range []string{handoffConversation, messageConversation} {
		if _, err := migration.Exec(ctx, `INSERT INTO customer_conversation(id,tenant_id,shop_id,customer_id)
			VALUES($1,$2,$3,$4)`, conversationID, tenant, shop, customer); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := migration.Exec(ctx, `INSERT INTO customer_message(id,tenant_id,conversation_id,source,content)
		VALUES($1,$2,$3,'customer','synthetic message')`, "handoff-message-"+suffix, tenant, handoffConversation); err != nil {
		t.Fatal(err)
	}
	if _, err := migration.Exec(ctx, `INSERT INTO customer_message(id,tenant_id,conversation_id,source,content)
		VALUES($1,$2,$3,'customer','synthetic message')`, "message-only-"+suffix, tenant, messageConversation); err != nil {
		t.Fatal(err)
	}
	if _, err := migration.Exec(ctx, `INSERT INTO customer_event(tenant_id,conversation_id,sequence,event_type,data_json)
		VALUES($1,$2,1,'state','{"state":"WAITING_HUMAN"}')`, tenant, handoffConversation); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	summary, err := adminStore.CustomerHandoff(ctx, gateway, tenant, []string{shop}, false,
		now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if summary.HandoffConversations != 1 || summary.CustomerConversations != 2 {
		t.Fatalf("summary = %+v, want handoff=1 customer=2", summary)
	}
	if summary.HandoffRate == nil || *summary.HandoffRate != 0.5 {
		t.Fatalf("handoff_rate = %v, want 0.5", summary.HandoffRate)
	}
	empty, err := adminStore.CustomerHandoff(ctx, gateway, tenant, []string{shop}, false,
		now.Add(2*time.Hour), now.Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if empty.HandoffConversations != 0 || empty.CustomerConversations != 0 || empty.HandoffRate != nil {
		t.Fatalf("empty window = %+v, want 0/0/null", empty)
	}
}
