package legacymigration

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestReadDistributorsSnapshotPreservesRepresentativeNonEmptyDomain(t *testing.T) {
	path := createLegacyHumanUsersSnapshot(t)
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		UPDATE v2_user SET is_distributor = 1, distributor_name = '固定渠道' WHERE id = 2;
		INSERT INTO v2_user
		(id,email,password,uuid,group_id,plan_id,transfer_enable,u,d,banned,expired_at,speed_limit,device_limit,
		 online_count,last_online_at,next_reset_at,last_reset_at,reset_count,token,created_at,updated_at)
		VALUES (100,'dist-2026082612000012345678901@internal.invalid','$2y$10$internal-hash',
		 '11111111-2222-4333-8444-555555555555',7,9,107374182400,10,20,0,1800000000,200,3,0,
		 1700000200,1700000300,1700000250,2,'12345678901234567890123456789012',1700000000,1700000400);
		CREATE TABLE v2_order (id INTEGER PRIMARY KEY, distributor_order_id INTEGER);
		INSERT INTO v2_order VALUES (10,20),(11,20);
		CREATE TABLE v2_distributor_order (
		 id INTEGER PRIMARY KEY,order_id INTEGER,distributor_user_id INTEGER,subscriber_user_id INTEGER,
		 customer_name TEXT,remark TEXT,claim_token_hash TEXT,delivery_status INTEGER,settlement_status INTEGER,
		 config_issued_at INTEGER,connected_at INTEGER,connected_node_id INTEGER,connected_node_name TEXT,
		 claimed_at INTEGER,closed_at INTEGER,settled_at INTEGER,settled_by INTEGER,claim_ip TEXT,claim_ua TEXT,
		 hwid_enabled INTEGER,hwid_limit INTEGER,created_at INTEGER,updated_at INTEGER
		);
		INSERT INTO v2_distributor_order VALUES
		(20,10,2,100,NULL,'=FORMULA','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
		 1,0,1700000100,1700000200,7,'节点甲',1700000050,NULL,NULL,NULL,'203.0.113.10','clash',1,2,1700000000,1700000400);
		CREATE TABLE v2_distributor_hwid_device (
		 id INTEGER PRIMARY KEY,distributor_order_id INTEGER,hwid TEXT,device_os TEXT,os_version TEXT,
		 device_model TEXT,user_agent TEXT,ip TEXT,first_seen_at INTEGER,last_seen_at INTEGER
		);
		INSERT INTO v2_distributor_hwid_device VALUES
		(30,20,'LEGACYHWID123','Android','16','Pixel 7','clash','203.0.113.10',1700000200,1700000300);
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadDistributorsSnapshot(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadDistributorsSnapshot() error = %v", err)
	}
	if snapshot.Path != path || snapshot.Size < 512 || len(snapshot.SHA256) != 64 || len(snapshot.Checksum) != 64 ||
		len(snapshot.Data.Subscribers) != 1 || len(snapshot.Data.Subscriptions) != 1 ||
		len(snapshot.Data.OrderLinks) != 2 || len(snapshot.Data.HWIDDevices) != 1 {
		t.Fatalf("distributor snapshot = %#v", snapshot)
	}
	value := snapshot.Data.Subscriptions[0]
	if value.SubscriberUserID != 100 || value.Remark == nil || *value.Remark != "=FORMULA" ||
		value.ConnectedNodeName == nil || *value.ConnectedNodeName != "节点甲" || value.ClaimIP == nil ||
		!strings.HasPrefix(*value.ClaimIP, "203.0.113") || !snapshot.Data.Subscribers[0].Banned && snapshot.Data.Subscribers[0].SubscriptionToken == "" {
		t.Fatalf("subscription=%#v subscriber=%#v", value, snapshot.Data.Subscribers[0])
	}
}
