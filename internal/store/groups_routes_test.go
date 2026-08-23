package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSchemaV3BackfillsExistingGroupReferences(t *testing.T) {
	database, err := OpenSQLite(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	if _, err := database.db.ExecContext(ctx, schemaV1); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, schemaV2); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC).Unix()
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO users (email, password_hash, is_admin, banned, created_at, updated_at, group_id, transfer_enable)
		VALUES ('legacy-user@example.test', 'hash', 0, 0, ?, ?, 42, 1)
	`, now, now); err != nil {
		t.Fatal(err)
	}
	result, err := database.db.ExecContext(ctx, `
		INSERT INTO nodes (name, type, host, port, show, enabled, sort, created_at, updated_at)
		VALUES ('legacy-node', 'vless', 'legacy.example.test', '443', 1, 1, 0, ?, ?)
	`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	nodeID, _ := result.LastInsertId()
	if _, err := database.db.ExecContext(ctx, `INSERT INTO node_group_memberships (node_id, group_id) VALUES (?, 43)`, nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `PRAGMA user_version = 2`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v2 to v3) error = %v", err)
	}
	groups, err := database.ListServerGroups(ctx)
	if err != nil || len(groups) != 2 {
		t.Fatalf("backfilled groups = %#v, err=%v", groups, err)
	}
	byID := map[int64]ServerGroup{}
	for _, group := range groups {
		byID[group.ID] = group
	}
	if byID[42].Name != "Imported group 42" || byID[42].UsersCount != 1 || byID[43].Name != "Imported group 43" || byID[43].ServersCount != 1 {
		t.Fatalf("backfilled group details = %#v", byID)
	}
	if _, err := database.db.ExecContext(ctx, `INSERT INTO node_group_memberships (node_id, group_id) VALUES (?, 999999)`, nodeID); err == nil {
		t.Fatal("v3 node group foreign key accepted a missing group")
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO users (email, password_hash, is_admin, banned, created_at, updated_at, group_id)
		VALUES ('invalid-group@example.test', 'hash', 0, 0, ?, ?, 999999)
	`, now, now); err == nil {
		t.Fatal("v3 user group trigger accepted a missing group")
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM server_groups WHERE id = 42`); err == nil {
		t.Fatal("v3 user group trigger allowed deletion of a referenced group")
	}
}

func TestServerGroupsAndRoutingRulesMaintainReferences(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

	group, err := database.CreateServerGroup(ctx, "  Premium  ", now)
	if err != nil || group.Name != "Premium" {
		t.Fatalf("CreateServerGroup() = (%#v, %v)", group, err)
	}
	rule, err := database.CreateRoutingRule(ctx, SaveRoutingRuleInput{
		Remarks: "  Domestic direct  ",
		Match:   []string{" example.cn ", "", "example.cn", "10.0.0.0/8"},
		Action:  "direct",
	}, now)
	if err != nil {
		t.Fatalf("CreateRoutingRule() error = %v", err)
	}
	if rule.Remarks != "Domestic direct" || !reflect.DeepEqual(rule.Match, []string{"example.cn", "10.0.0.0/8"}) || rule.ActionValue != "" {
		t.Fatalf("normalized routing rule = %#v", rule)
	}

	node, err := database.CreateNode(ctx, CreateNodeInput{
		Name: "route-node", Type: "vless", Host: "route.example.test", Port: "443", Show: true, Enabled: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveNodeRuntime(ctx, node.ID, SaveNodeRuntimeInput{
		RateMicros: 1_000_000,
		GroupIDs:   []int64{group.ID},
		RouteIDs:   []int64{rule.ID},
		Config:     []byte(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":443}`),
	}, now); err != nil {
		t.Fatalf("SaveNodeRuntime() error = %v", err)
	}
	if _, err := database.CreateRuntimeUser(ctx, CreateRuntimeUserInput{
		Email: "group-user@example.test", PasswordHash: "hash",
		UUID: "5dbf222e-0f0b-4528-b8f3-2d88fbb89978", GroupID: group.ID, TransferEnable: 1,
	}, now); err != nil {
		t.Fatalf("CreateRuntimeUser() error = %v", err)
	}

	groups, err := database.ListServerGroups(ctx)
	if err != nil || len(groups) != 1 || groups[0].UsersCount != 1 || groups[0].ServersCount != 1 {
		t.Fatalf("ListServerGroups() = (%#v, %v)", groups, err)
	}
	runtime, err := database.GetNodeRuntime(ctx, node.ID)
	if err != nil || len(runtime.Routes) != 1 || runtime.Routes[0].ID != rule.ID {
		t.Fatalf("GetNodeRuntime() routes = (%#v, %v)", runtime.Routes, err)
	}
	if err := database.DeleteServerGroup(ctx, group.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("DeleteServerGroup(referenced) error = %v, want ErrConflict", err)
	}
	if err := database.DeleteRoutingRule(ctx, rule.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("DeleteRoutingRule(referenced) error = %v, want ErrConflict", err)
	}
}

func TestSaveNodeRuntimeRejectsMissingReferencesAtomically(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	group, _ := database.CreateServerGroup(ctx, "Known group", now)
	rule, _ := database.CreateRoutingRule(ctx, SaveRoutingRuleInput{Remarks: "Known route", Match: []string{"example.com"}, Action: "block"}, now)
	node, err := database.CreateNode(ctx, CreateNodeInput{Name: "atomic-node", Type: "vless", Host: "atomic.example.test", Port: "443", Show: true, Enabled: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":443,"marker":"original"}`)
	if _, err := database.SaveNodeRuntime(ctx, node.ID, SaveNodeRuntimeInput{RateMicros: 1_000_000, GroupIDs: []int64{group.ID}, RouteIDs: []int64{rule.ID}, Config: original}, now); err != nil {
		t.Fatal(err)
	}
	_, err = database.SaveNodeRuntime(ctx, node.ID, SaveNodeRuntimeInput{
		RateMicros: 2_000_000, GroupIDs: []int64{group.ID}, RouteIDs: []int64{rule.ID, 999999},
		Config: []byte(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":443,"marker":"changed"}`),
	}, now.Add(time.Minute))
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("SaveNodeRuntime(missing route) error = %v, want ErrInvalidInput", err)
	}
	runtime, err := database.GetNodeRuntime(ctx, node.ID)
	if err != nil || runtime.RateMicros != 1_000_000 || string(runtime.Config) != string(original) || len(runtime.Routes) != 1 || runtime.Routes[0].ID != rule.ID {
		t.Fatalf("runtime changed after rejected save: %#v, err=%v", runtime, err)
	}
}

func TestGetNodeRuntimeDoesNotExhaustConnectionPoolAcrossConcurrentRouteReads(t *testing.T) {
	database := newTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	group, _ := database.CreateServerGroup(ctx, "Concurrent group", now)
	rule, _ := database.CreateRoutingRule(ctx, SaveRoutingRuleInput{Remarks: "Concurrent route", Match: []string{"example.com"}, Action: "direct"}, now)
	node, err := database.CreateNode(ctx, CreateNodeInput{Name: "concurrent-node", Type: "vless", Host: "concurrent.example.test", Port: "443", Show: true, Enabled: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveNodeRuntime(ctx, node.ID, SaveNodeRuntimeInput{
		RateMicros: 1_000_000, GroupIDs: []int64{group.ID}, RouteIDs: []int64{rule.ID},
		Config: []byte(`{"protocol":"vless","server_port":443}`),
	}, now); err != nil {
		t.Fatal(err)
	}
	const readers = 32
	start := make(chan struct{})
	errorsFound := make(chan error, readers)
	var wait sync.WaitGroup
	for range readers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			runtime, err := database.GetNodeRuntime(ctx, node.ID)
			if err != nil {
				errorsFound <- err
				return
			}
			if len(runtime.Routes) != 1 {
				errorsFound <- fmt.Errorf("routes = %d, want 1", len(runtime.Routes))
			}
		}()
	}
	close(start)
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("concurrent GetNodeRuntime() error = %v", err)
	}
}

func TestRoutingRuleValidation(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	cases := []SaveRoutingRuleInput{
		{Remarks: "", Match: []string{"example.com"}, Action: "block"},
		{Remarks: "empty match", Match: []string{"", "  "}, Action: "block"},
		{Remarks: "bad action", Match: []string{"example.com"}, Action: "unknown"},
		{Remarks: "missing target", Match: []string{"example.com"}, Action: "proxy"},
		{Remarks: "control", Match: []string{"example.com\ninvalid"}, Action: "direct"},
	}
	for _, input := range cases {
		if _, err := database.CreateRoutingRule(ctx, input, now); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("CreateRoutingRule(%#v) error = %v, want ErrInvalidInput", input, err)
		}
	}
}

func TestGroupAndRoutingTextLimitsCountUnicodeCharacters(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	if _, err := database.CreateServerGroup(ctx, strings.Repeat("组", 255), now); err != nil {
		t.Fatalf("CreateServerGroup(255 Unicode characters) error = %v", err)
	}
	if _, err := database.CreateServerGroup(ctx, strings.Repeat("组", 256), now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("CreateServerGroup(256 Unicode characters) error = %v, want ErrInvalidInput", err)
	}
	if _, err := database.CreateRoutingRule(ctx, SaveRoutingRuleInput{
		Remarks: strings.Repeat("路", 255), Match: []string{"example.com"}, Action: "dns", ActionValue: strings.Repeat("出", 255),
	}, now); err != nil {
		t.Fatalf("CreateRoutingRule(255 Unicode characters) error = %v", err)
	}
	if _, err := database.CreateRoutingRule(ctx, SaveRoutingRuleInput{
		Remarks: strings.Repeat("路", 256), Match: []string{"example.com"}, Action: "block",
	}, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("CreateRoutingRule(256 Unicode characters) error = %v, want ErrInvalidInput", err)
	}
}
