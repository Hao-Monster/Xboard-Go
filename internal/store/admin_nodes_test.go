package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestSchemaV40PreservesV39NodesAndAddsAdministratorRevision(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	node, err := database.CreateNode(ctx, CreateNodeInput{
		Name: "Preserved", Type: "vless", Host: "preserved.test", Port: "443", Show: true, Enabled: true, Sort: 9,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveNodeRuntime(ctx, node.ID, SaveNodeRuntimeInput{
		RateMicros: 3_000_000, Config: []byte(`{"protocol":"vless","server_port":443,"marker":"preserved"}`),
	}, now); err != nil {
		t.Fatal(err)
	}
	removeSchemaV40ForMigrationTest(t, database)
	if _, err := database.db.ExecContext(ctx, `PRAGMA user_version = 39`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v39 to v40) error = %v", err)
	}
	preserved, err := database.GetNode(ctx, node.ID)
	if err != nil || preserved.Revision != 1 || preserved.Name != node.Name || preserved.Sort != 9 || !preserved.RuntimeConfigured || preserved.Rate != 3 {
		t.Fatalf("preserved node = %#v, error=%v", preserved, err)
	}
	var version, indexCount int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sqlite_schema WHERE type = 'index' AND name IN ('idx_nodes_admin_sort', 'idx_nodes_admin_type_sort')
	`).Scan(&indexCount); err != nil {
		t.Fatal(err)
	}
	if version != 40 || indexCount != 2 {
		t.Fatalf("schema version=%d index_count=%d", version, indexCount)
	}
}

func TestListAdminNodesFiltersAndAggregatesWithoutChangingStableOrder(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	machine, _, err := database.CreateMachine(ctx, CreateMachineInput{Name: "edge-sg", IsActive: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	group, err := database.CreateServerGroup(ctx, "Premium", now)
	if err != nil {
		t.Fatal(err)
	}
	alpha, err := database.CreateNode(ctx, CreateNodeInput{
		Name: "Alpha VLESS", Type: "vless", Host: "alpha.example.test", Port: "443",
		Show: true, Enabled: true, Sort: 20, MachineID: &machine.ID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateNode(ctx, CreateNodeInput{
		Name: "Beta Trojan", Type: "trojan", Host: "beta.example.test", Port: "8443",
		Show: false, Enabled: true, Sort: 10,
	}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `INSERT INTO node_group_memberships (node_id, group_id) VALUES (?, ?)`, alpha.ID, group.ID); err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateRuntimeUser(ctx, CreateRuntimeUserInput{
		Email: "node-list-user@example.test", PasswordHash: "hash",
		UUID: "149a434f-4b20-4fe2-97d0-3bb82b70fb44", GroupID: group.ID, TransferEnable: 1,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO node_user_online (node_id, user_id, connections, expires_at) VALUES (?, ?, 3, ?)
	`, alpha.ID, user.ID, now.Add(time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	show := true
	page, err := database.ListAdminNodes(ctx, AdminNodeFilter{
		Page: 1, PageSize: 500, Query: " alpha.EXAMPLE ", Type: "vless", Show: &show, MachineID: &machine.ID,
	}, now)
	if err != nil {
		t.Fatalf("ListAdminNodes() error = %v", err)
	}
	if page.Total != 1 || page.Page != 1 || page.PageSize != 500 || len(page.Items) != 1 {
		t.Fatalf("page = %#v", page)
	}
	item := page.Items[0]
	if item.ID != alpha.ID || item.Revision != 1 || item.MachineName == nil || *item.MachineName != machine.Name ||
		item.OnlineCount != 3 || !reflect.DeepEqual(item.GroupIDs, []int64{group.ID}) {
		t.Fatalf("admin node = %#v", item)
	}

	if _, err := database.ListAdminNodes(ctx, AdminNodeFilter{Page: 1, PageSize: 501}, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized page error = %v, want ErrInvalidInput", err)
	}
	if _, err := database.ListAdminNodes(ctx, AdminNodeFilter{Page: 1, PageSize: 10, Type: "unknown"}, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unsupported type error = %v, want ErrInvalidInput", err)
	}
	if _, err := database.ListAdminNodes(ctx, AdminNodeFilter{Page: int(^uint(0) >> 1), PageSize: 500}, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("overflowing page error = %v, want ErrInvalidInput", err)
	}
}

func TestAdminNodeUpdateAndBatchesRejectStaleRevisionsAtomically(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 11, 0, 0, 0, time.UTC)
	first, err := database.CreateNode(ctx, CreateNodeInput{Name: "First", Type: "vless", Host: "first.test", Port: "443", Show: true, Enabled: true, Sort: 10}, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.CreateNode(ctx, CreateNodeInput{Name: "Second", Type: "trojan", Host: "second.test", Port: "8443", Show: true, Enabled: true, Sort: 20}, now)
	if err != nil {
		t.Fatal(err)
	}

	updated, _, err := database.UpdateAdminNode(ctx, first.ID, UpdateAdminNodeInput{
		Revision: first.Revision, Name: "First updated", Host: "updated.test", Port: "1443",
		Show: true, Enabled: true, Sort: 10, MachineIDSet: true,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("UpdateAdminNode() error = %v", err)
	}
	if updated.Revision != first.Revision+1 || updated.Name != "First updated" || updated.Type != first.Type {
		t.Fatalf("updated node = %#v", updated)
	}
	if _, _, err := database.UpdateAdminNode(ctx, first.ID, UpdateAdminNodeInput{
		Revision: first.Revision, Name: "stale overwrite", Host: "stale.test", Port: "443",
		Show: true, Enabled: true, Sort: 10, MachineIDSet: true,
	}, now.Add(2*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update error = %v, want ErrConflict", err)
	}

	if _, err := database.ReorderAdminNodes(ctx, []AdminNodeRevision{
		{ID: first.ID, Revision: updated.Revision},
		{ID: second.ID, Revision: second.Revision + 1},
	}, now.Add(3*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale reorder error = %v, want ErrConflict", err)
	}
	afterFirst, _ := database.GetNode(ctx, first.ID)
	afterSecond, _ := database.GetNode(ctx, second.ID)
	if afterFirst.Sort != 10 || afterSecond.Sort != 20 || afterFirst.Revision != updated.Revision || afterSecond.Revision != second.Revision {
		t.Fatalf("stale reorder was not atomic: first=%#v second=%#v", afterFirst, afterSecond)
	}

	show := false
	if _, err := database.UpdateAdminNodeStates(ctx, AdminNodeStateInput{
		Targets: []AdminNodeRevision{{ID: first.ID, Revision: afterFirst.Revision}, {ID: second.ID, Revision: second.Revision + 1}},
		Show:    &show,
	}, now.Add(4*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale state batch error = %v, want ErrConflict", err)
	}
	afterFirst, _ = database.GetNode(ctx, first.ID)
	if !afterFirst.Show {
		t.Fatal("first node changed after an atomic batch conflict")
	}
}

func TestReorderAdminNodesPreservesUnselectedSortSlots(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 11, 30, 0, 0, time.UTC)
	first, err := database.CreateNode(ctx, CreateNodeInput{Name: "First", Type: "vless", Host: "first.test", Port: "443", Show: true, Enabled: true, Sort: 10}, now)
	if err != nil {
		t.Fatal(err)
	}
	unselected, err := database.CreateNode(ctx, CreateNodeInput{Name: "Unselected", Type: "vmess", Host: "middle.test", Port: "443", Show: true, Enabled: true, Sort: 15}, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.CreateNode(ctx, CreateNodeInput{Name: "Second", Type: "trojan", Host: "second.test", Port: "443", Show: true, Enabled: true, Sort: 20}, now)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := database.ReorderAdminNodes(ctx, []AdminNodeRevision{
		{ID: second.ID, Revision: second.Revision},
		{ID: first.ID, Revision: first.Revision},
	}, now.Add(time.Minute)); err != nil {
		t.Fatalf("ReorderAdminNodes() error = %v", err)
	}

	firstAfter, _ := database.GetNode(ctx, first.ID)
	unselectedAfter, _ := database.GetNode(ctx, unselected.ID)
	secondAfter, _ := database.GetNode(ctx, second.ID)
	if secondAfter.Sort != 10 || unselectedAfter.Sort != 15 || firstAfter.Sort != 20 {
		t.Fatalf("sort slots changed outside the reordered set: first=%d unselected=%d second=%d", firstAfter.Sort, unselectedAfter.Sort, secondAfter.Sort)
	}
	if unselectedAfter.Revision != unselected.Revision {
		t.Fatalf("unselected revision=%d, want %d", unselectedAfter.Revision, unselected.Revision)
	}
}

func TestReorderAdminNodesRejectsAmbiguousDuplicateSortSlots(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 11, 45, 0, 0, time.UTC)
	first, err := database.CreateNode(ctx, CreateNodeInput{Name: "First", Type: "vless", Host: "first.test", Port: "443", Show: true, Enabled: true, Sort: 10}, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.CreateNode(ctx, CreateNodeInput{Name: "Second", Type: "trojan", Host: "second.test", Port: "443", Show: true, Enabled: true, Sort: 10}, now)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := database.ReorderAdminNodes(ctx, []AdminNodeRevision{
		{ID: second.ID, Revision: second.Revision},
		{ID: first.ID, Revision: first.Revision},
	}, now.Add(time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate sort slots error=%v, want ErrConflict", err)
	}
	firstAfter, _ := database.GetNode(ctx, first.ID)
	secondAfter, _ := database.GetNode(ctx, second.ID)
	if firstAfter.Sort != 10 || secondAfter.Sort != 10 || firstAfter.Revision != first.Revision || secondAfter.Revision != second.Revision {
		t.Fatalf("ambiguous reorder was not atomic: first=%#v second=%#v", firstAfter, secondAfter)
	}
}

func TestCopyAdminNodePreservesConfigurationButNotIdentityOrEphemeralState(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	machine, _, _ := database.CreateMachine(ctx, CreateMachineInput{Name: "edge-copy", IsActive: true}, now)
	group, _ := database.CreateServerGroup(ctx, "Copy group", now)
	route, _ := database.CreateRoutingRule(ctx, SaveRoutingRuleInput{Remarks: "Copy route", Match: []string{"example.test"}, Action: "direct"}, now)
	original, err := database.CreateNode(ctx, CreateNodeInput{
		Name: "Original", Type: "vless", Host: "copy.example.test", Port: "443", Show: true, Enabled: true, Sort: 7, MachineID: &machine.ID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	runtimeConfig := `{"protocol":"vless","server_port":9443,"marker":"copy-me"}`
	if _, err := database.SaveNodeRuntime(ctx, original.ID, SaveNodeRuntimeInput{
		RateMicros: 2_500_000, GroupIDs: []int64{group.ID}, RouteIDs: []int64{route.ID}, Config: []byte(runtimeConfig),
	}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO node_protocol_definitions (
			node_id, external_code, server_port, tags_json, protocol_settings_json, rate_time_enabled,
			rate_time_ranges_json, custom_outbounds_json, custom_routes_json, cert_config_json,
			transfer_enable, configured_rate_micros
		) VALUES (?, 'legacy-external-code', 9443, '["edge"]', '{"flow":"xtls-rprx-vision"}', 1,
			'[{"start":"00:00","end":"01:00","rate":0.5}]', '[{"tag":"direct"}]', '[{"match":"example.test"}]',
			'{"mode":"file"}', 1099511627776, 2500000)
	`, original.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE nodes SET traffic_u = 123, traffic_d = 456 WHERE id = ?`, original.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `INSERT INTO node_runtime_state (node_id, status_json, metrics_json, updated_at) VALUES (?, '{}', '{}', ?)`, original.ID, now.Unix()); err != nil {
		t.Fatal(err)
	}

	copyNode, _, err := database.CopyAdminNode(ctx, original.ID, original.Revision, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("CopyAdminNode() error = %v", err)
	}
	if copyNode.ID == original.ID || copyNode.Name != "Original - 副本" || copyNode.Show || !copyNode.Enabled || copyNode.MachineID == nil ||
		*copyNode.MachineID != machine.ID || copyNode.TrafficUpload != 0 || copyNode.TrafficDownload != 0 || copyNode.Revision != 1 {
		t.Fatalf("copied node = %#v", copyNode)
	}
	copiedRuntime, err := database.GetNodeRuntime(ctx, copyNode.ID)
	if err != nil || copiedRuntime.RateMicros != 2_500_000 || string(copiedRuntime.Config) != runtimeConfig ||
		!reflect.DeepEqual(copiedRuntime.GroupIDs, []int64{group.ID}) || len(copiedRuntime.Routes) != 1 || copiedRuntime.Routes[0].ID != route.ID {
		t.Fatalf("copied runtime = %#v, error=%v", copiedRuntime, err)
	}
	var externalCode any
	var tags, settings, certificate string
	if err := database.db.QueryRowContext(ctx, `
		SELECT external_code, tags_json, protocol_settings_json, cert_config_json
		FROM node_protocol_definitions WHERE node_id = ?
	`, copyNode.ID).Scan(&externalCode, &tags, &settings, &certificate); err != nil {
		t.Fatal(err)
	}
	if externalCode != nil || tags != `["edge"]` || settings != `{"flow":"xtls-rprx-vision"}` || certificate != `{"mode":"file"}` {
		t.Fatalf("copied definition = external=%#v tags=%s settings=%s cert=%s", externalCode, tags, settings, certificate)
	}
	var ephemeralCount int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_runtime_state WHERE node_id = ?`, copyNode.ID).Scan(&ephemeralCount); err != nil || ephemeralCount != 0 {
		t.Fatalf("copied ephemeral rows = %d, error=%v", ephemeralCount, err)
	}
}

func TestResetAndDeleteAdminNodesAreAtomicAndReconcileOnlineCounts(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 13, 0, 0, 0, time.UTC)
	parent, _ := database.CreateNode(ctx, CreateNodeInput{Name: "Parent", Type: "vless", Host: "parent.test", Port: "443", Show: true, Enabled: true}, now)
	child, _ := database.CreateNode(ctx, CreateNodeInput{Name: "Child", Type: "vless", Host: "child.test", Port: "443", Show: true, Enabled: true}, now)
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO node_protocol_definitions (node_id, parent_id, server_port, protocol_settings_json, configured_rate_micros)
		VALUES (?, NULL, 443, '{}', 1000000), (?, ?, 443, '{}', 1000000)
	`, parent.ID, child.ID, parent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE nodes SET traffic_u = 100, traffic_d = 200 WHERE id IN (?, ?)`, parent.ID, child.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ResetAdminNodeTraffic(ctx, []AdminNodeRevision{{ID: parent.ID, Revision: parent.Revision}, {ID: child.ID, Revision: child.Revision + 1}}, now.Add(time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale reset error = %v, want ErrConflict", err)
	}
	parentAfter, _ := database.GetNode(ctx, parent.ID)
	if parentAfter.TrafficUpload != 100 || parentAfter.TrafficDownload != 200 {
		t.Fatal("traffic reset was not atomic")
	}
	if _, err := database.DeleteAdminNodes(ctx, []AdminNodeRevision{{ID: parent.ID, Revision: parent.Revision}}, now.Add(2*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("referenced parent delete error = %v, want ErrConflict", err)
	}

	group, err := database.CreateServerGroup(ctx, "Delete group", now)
	if err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateRuntimeUser(ctx, CreateRuntimeUserInput{
		Email: "node-delete-user@example.test", PasswordHash: "hash",
		UUID: "11da9337-5fc5-48b3-977d-8946c549dc43", GroupID: group.ID, TransferEnable: 1,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `INSERT INTO node_device_ips (node_id, user_id, ip, expires_at) VALUES (?, ?, '203.0.113.10', ?)`, child.ID, user.ID, now.Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET online_count = 1 WHERE id = ?`, user.ID); err != nil {
		t.Fatal(err)
	}
	mutation, err := database.DeleteAdminNodes(ctx, []AdminNodeRevision{{ID: child.ID, Revision: child.Revision}}, now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("DeleteAdminNodes(child) error = %v", err)
	}
	if !reflect.DeepEqual(mutation.NodeIDs, []int64{child.ID}) || !reflect.DeepEqual(mutation.AffectedUserIDs, []int64{user.ID}) {
		t.Fatalf("delete mutation = %#v", mutation)
	}
	var online int
	if err := database.db.QueryRowContext(ctx, `SELECT online_count FROM users WHERE id = ?`, user.ID).Scan(&online); err != nil || online != 0 {
		t.Fatalf("online_count = %d, error=%v", online, err)
	}
}
