package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSubscriptionSettingsAreValidatedAndUpdatedAtomically(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	initial, err := database.GetSubscriptionSettings(ctx)
	if err != nil {
		t.Fatalf("GetSubscriptionSettings() error = %v", err)
	}
	if initial.Revision != 1 || initial.Path != "s" || initial.ShowInfo || initial.ShowProtocol {
		t.Fatalf("initial subscription settings = %#v", initial)
	}
	if len(initial.Templates) != len(SubscriptionTemplateNames) {
		t.Fatalf("initial template count = %d, want %d", len(initial.Templates), len(SubscriptionTemplateNames))
	}
	for _, name := range SubscriptionTemplateNames {
		if _, ok := initial.Templates[name]; !ok {
			t.Fatalf("initial settings are missing template %q", name)
		}
	}

	if _, err := database.BootstrapAdmin(ctx, "subscription-admin@example.test", "opaque-hash", time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	administrator, err := database.FindUserByEmail(ctx, "subscription-admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	templates := make(map[string]string, len(SubscriptionTemplateNames))
	for _, name := range SubscriptionTemplateNames {
		templates[name] = "template:" + name
	}
	templates["singbox"] = `{"outbounds":[]}`
	for _, name := range []string{"clash", "clashmeta", "stash"} {
		templates[name] = "proxies: []\nproxy-groups: []\nrules: []\n"
	}
	updated, err := database.UpdateSubscriptionSettings(ctx, administrator.ID, initial.Revision, SaveSubscriptionSettingsInput{
		Path: "  feeds_1  ", ShowInfo: true, ShowProtocol: true, Templates: templates,
	}, time.Unix(2, 0))
	if err != nil {
		t.Fatalf("UpdateSubscriptionSettings() error = %v", err)
	}
	if updated.Revision != 2 || updated.Path != "feeds_1" || !updated.ShowInfo || !updated.ShowProtocol || updated.Templates["singbox"] != `{"outbounds":[]}` {
		t.Fatalf("updated subscription settings = %#v", updated)
	}
	if _, err := database.UpdateSubscriptionSettings(ctx, administrator.ID, initial.Revision, SaveSubscriptionSettingsInput{
		Path: "stale", Templates: templates,
	}, time.Unix(3, 0)); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale UpdateSubscriptionSettings() error = %v, want ErrRevisionConflict", err)
	}

	for _, test := range []SaveSubscriptionSettingsInput{
		{Path: "../unsafe", Templates: templates},
		{Path: "s", Templates: map[string]string{"unknown": "value"}},
		{Path: "s", Templates: map[string]string{"clash": strings.Repeat("x", maxSubscriptionTemplateBytes+1)}},
		{Path: "s", Templates: replaceSubscriptionTemplate(templates, "singbox", `{"outbounds":{}}`)},
		{Path: "s", Templates: replaceSubscriptionTemplate(templates, "clash", "proxies: {}")},
	} {
		if _, err := database.UpdateSubscriptionSettings(ctx, administrator.ID, updated.Revision, test, time.Unix(4, 0)); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid UpdateSubscriptionSettings(%#v) error = %v, want ErrInvalidInput", test, err)
		}
	}
	preserved, err := database.GetSubscriptionSettings(ctx)
	if err != nil || preserved.Revision != updated.Revision || preserved.Templates["clash"] != templates["clash"] {
		t.Fatalf("invalid update changed settings: %#v, err=%v", preserved, err)
	}
}

func TestSubscriptionRenderConfigLoadsOnlyTheSelectedTemplate(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	initial, err := database.GetSubscriptionSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.BootstrapAdmin(ctx, "render-config-admin@example.test", "opaque-hash", time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	administrator, err := database.FindUserByEmail(ctx, "render-config-admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	templates := emptySubscriptionTemplateMap()
	templates["clash"] = "proxies: []\nproxy-groups: []\nrules: []\n"
	templates["singbox"] = `{"outbounds":[]}`
	if _, err := database.UpdateSubscriptionSettings(ctx, administrator.ID, initial.Revision, SaveSubscriptionSettingsInput{
		Path: "feeds", ShowInfo: true, ShowProtocol: true, Templates: templates,
	}, time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	config, err := database.GetSubscriptionRenderConfig(ctx, "clash")
	if err != nil {
		t.Fatal(err)
	}
	if config.Path != "feeds" || !config.ShowInfo || !config.ShowProtocol || config.AppName != "Xboard-Go" ||
		len(config.Templates) != 1 || config.Templates["clash"] != templates["clash"] {
		t.Fatalf("render config = %#v", config)
	}
	if _, err := database.GetSubscriptionRenderConfig(ctx, "unknown"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown template error = %v, want ErrInvalidInput", err)
	}
}

func replaceSubscriptionTemplate(source map[string]string, name, content string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	result[name] = content
	return result
}

func TestSubscriptionAccountLookupPreservesLegacyEligibilityInputs(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	token := "11112222333344445555666677778888"
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO users (
			email,password_hash,banned,uuid,group_id,plan_id,transfer_enable,traffic_u,traffic_d,
			expired_at,subscription_token,created_at,updated_at
		) VALUES ('subscription@example.test','opaque',0,'11111111-2222-4333-8444-555555555555',NULL,NULL,100,90,20,NULL,?,1700000000,1700000000)
	`, token); err != nil {
		t.Fatal(err)
	}
	account, err := database.FindSubscriptionAccount(ctx, token)
	if err != nil {
		t.Fatalf("FindSubscriptionAccount() error = %v", err)
	}
	if account.UUID != "11111111-2222-4333-8444-555555555555" || account.GroupID != nil || account.PlanID != nil || account.TransferEnable != 100 || account.TrafficUpload+account.TrafficDownload != 110 || account.ExpiredAt != nil || !account.CreatedAt.Equal(time.Unix(1_700_000_000, 0)) {
		t.Fatalf("subscription account = %#v", account)
	}
	if !account.AvailableAt(now) {
		t.Fatal("legacy-compatible availability rejected an exhausted, permanent account without a plan")
	}
	if _, err := database.FindSubscriptionAccount(ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing token error = %v, want ErrNotFound", err)
	}
}

func TestResetSubscriptionSecurityAtomicallyRotatesBothSecrets(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	group, err := database.CreateServerGroup(ctx, "Subscription rotation", now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	groupID := group.ID
	created, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "rotate-subscription@example.test", PasswordHash: "opaque", GroupID: &groupID, TransferEnable: 1_000,
	}, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	before, err := database.GetSubscriptionAccount(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, node := createReportingNode(t, database, now.Add(-time.Hour))
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO node_device_ips (node_id,user_id,ip,expires_at) VALUES (?,?,'192.0.2.90',?);
		INSERT INTO node_user_online (node_id,user_id,connections,expires_at) VALUES (?,?,2,?);
		UPDATE users SET online_count=2 WHERE id=?
	`, node.ID, created.ID, now.Add(time.Hour).Unix(), node.ID, created.ID, now.Add(time.Hour).Unix(), created.ID); err != nil {
		t.Fatal(err)
	}
	rotated, mutation, err := database.ResetSubscriptionSecurity(ctx, created.ID, now)
	if err != nil {
		t.Fatalf("ResetSubscriptionSecurity() error = %v", err)
	}
	if rotated.ID != before.ID || rotated.SubscriptionToken == before.SubscriptionToken || rotated.UUID == before.UUID {
		t.Fatalf("rotated account = %#v, before = %#v", rotated, before)
	}
	if mutation.PreviousUUID != before.UUID || mutation.GroupID == nil || *mutation.GroupID != groupID {
		t.Fatalf("rotation mutation = %#v", mutation)
	}
	if !validSubscriptionToken(rotated.SubscriptionToken) {
		t.Fatalf("rotated token is not canonical: %q", rotated.SubscriptionToken)
	}
	parsed, err := uuid.Parse(rotated.UUID)
	if err != nil || parsed.String() != rotated.UUID || parsed.Version() != 4 {
		t.Fatalf("rotated UUID is not canonical v4: %q, err=%v", rotated.UUID, err)
	}
	if _, err := database.FindSubscriptionAccount(ctx, before.SubscriptionToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old token lookup error = %v, want ErrNotFound", err)
	}
	if account, err := database.FindSubscriptionAccount(ctx, rotated.SubscriptionToken); err != nil || account.UUID != rotated.UUID {
		t.Fatalf("new token lookup = (%#v, %v)", account, err)
	}
	if _, _, err := database.ResetSubscriptionSecurity(ctx, 0, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid reset error = %v, want ErrInvalidInput", err)
	}
	var devices, onlineRows, onlineCount int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_device_ips WHERE user_id=?`, created.ID).Scan(&devices); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_user_online WHERE user_id=?`, created.ID).Scan(&onlineRows); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT online_count FROM users WHERE id=?`, created.ID).Scan(&onlineCount); err != nil {
		t.Fatal(err)
	}
	if devices != 0 || onlineRows != 0 || onlineCount != 0 {
		t.Fatalf("subscription reset retained runtime state: devices=%d online_rows=%d online_count=%d", devices, onlineRows, onlineCount)
	}
}

func TestAdministratorSubscriptionSecurityResetUsesRevisionCAS(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_100_000, 0)
	created, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "administrator-rotate-subscription@example.test", PasswordHash: "opaque", TransferEnable: 1_000,
	}, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	before, err := database.GetSubscriptionAccount(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errorsByRequest := make(chan error, 2)
	var workers sync.WaitGroup
	for index := 0; index < 2; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, _, resetErr := database.ResetSubscriptionSecurityAtRevision(ctx, created.ID, created.Revision, now)
			errorsByRequest <- resetErr
		}()
	}
	close(start)
	workers.Wait()
	close(errorsByRequest)
	succeeded, conflicted := 0, 0
	for resetErr := range errorsByRequest {
		switch {
		case resetErr == nil:
			succeeded++
		case errors.Is(resetErr, ErrRevisionConflict):
			conflicted++
		default:
			t.Fatalf("concurrent reset error = %v", resetErr)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent resets succeeded=%d conflicted=%d, want 1/1", succeeded, conflicted)
	}
	after, err := database.GetSubscriptionAccount(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.UUID == before.UUID || after.SubscriptionToken == before.SubscriptionToken {
		t.Fatalf("administrator reset did not rotate both credentials: before=%#v after=%#v", before, after)
	}
	updated, err := database.GetAdminUser(ctx, created.ID)
	if err != nil || updated.Revision != created.Revision+1 {
		t.Fatalf("administrator reset revision=(%#v,%v), want %d", updated, err, created.Revision+1)
	}
	if _, err := database.FindSubscriptionAccount(ctx, before.SubscriptionToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old token lookup error = %v, want ErrNotFound", err)
	}
}

func TestAdministratorSubscriptionSecurityResetRejectsInternalAccounts(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_200_000, 0)
	created, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "internal-subscription-reset@example.test", PasswordHash: "opaque", TransferEnable: 1_000,
	}, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	before, err := database.GetSubscriptionAccount(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET account_kind=? WHERE id=?`, AccountKindInternalSubscription, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.ResetSubscriptionSecurityAtRevision(ctx, created.ID, created.Revision, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("internal subscription reset error = %v, want ErrNotFound", err)
	}
	var uuid, token string
	var revision int64
	if err := database.db.QueryRowContext(ctx, `SELECT uuid,subscription_token,admin_revision FROM users WHERE id=?`, created.ID).Scan(&uuid, &token, &revision); err != nil {
		t.Fatal(err)
	}
	if uuid != before.UUID || token != before.SubscriptionToken || revision != created.Revision {
		t.Fatalf("internal subscription account mutated: uuid_changed=%t token_changed=%t revision=%d want=%d", uuid != before.UUID, token != before.SubscriptionToken, revision, created.Revision)
	}
}

func TestListSubscriptionNodesUsesLegacyVisibilityGroupAndCapacityRules(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).Unix()
	for _, groupID := range []int64{3, 9} {
		if _, err := database.db.ExecContext(ctx, `INSERT INTO server_groups (id,name,created_at,updated_at) VALUES (?, ?, ?, ?)`, groupID, "group", now, now); err != nil {
			t.Fatal(err)
		}
	}
	type nodeFixture struct {
		id       int64
		name     string
		show     bool
		enabled  bool
		groupID  int64
		capacity int64
		upload   int64
		download int64
		sort     int
	}
	fixtures := []nodeFixture{
		{id: 41, name: "enabled", show: true, enabled: true, groupID: 3, sort: 2},
		{id: 42, name: "disabled but visible", show: true, enabled: false, groupID: 3, capacity: 100, upload: 40, download: 59, sort: 1},
		{id: 43, name: "exhausted", show: true, enabled: true, groupID: 3, capacity: 100, upload: 40, download: 60, sort: 3},
		{id: 44, name: "hidden", show: false, enabled: true, groupID: 3, sort: 4},
		{id: 45, name: "wrong group", show: true, enabled: true, groupID: 9, sort: 5},
	}
	for _, fixture := range fixtures {
		if _, err := database.db.ExecContext(ctx, `
			INSERT INTO nodes (id,name,type,host,port,show,enabled,sort,rate_micros,runtime_config,traffic_u,traffic_d,created_at,updated_at)
			VALUES (?,?,'vless','node.example.test','443',?,?,?,?,NULL,?,?,?,?)
		`, fixture.id, fixture.name, fixture.show, fixture.enabled, fixture.sort, 1_000_000, fixture.upload, fixture.download, now, now); err != nil {
			t.Fatal(err)
		}
		if _, err := database.db.ExecContext(ctx, `
			INSERT INTO node_protocol_definitions (
				node_id,server_port,protocol_settings_json,transfer_enable,configured_rate_micros
			) VALUES (?,443,'{"tls":1}',?,1000000)
		`, fixture.id, fixture.capacity); err != nil {
			t.Fatal(err)
		}
		if _, err := database.db.ExecContext(ctx, `INSERT INTO node_group_memberships (node_id,group_id) VALUES (?,?)`, fixture.id, fixture.groupID); err != nil {
			t.Fatal(err)
		}
	}
	nodes, err := database.ListSubscriptionNodes(ctx, 3)
	if err != nil {
		t.Fatalf("ListSubscriptionNodes() error = %v", err)
	}
	if len(nodes) != 2 || nodes[0].ID != 42 || nodes[1].ID != 41 || nodes[0].Enabled || !nodes[1].Enabled {
		t.Fatalf("subscription nodes = %#v", nodes)
	}
	if string(nodes[0].ProtocolSettings) != `{"tls":1}` || nodes[0].ServerPort != 443 {
		t.Fatalf("subscription node definition = %#v", nodes[0])
	}
	if nodes, err := database.ListSubscriptionNodes(ctx, 0); !errors.Is(err, ErrInvalidInput) || nodes != nil {
		t.Fatalf("ListSubscriptionNodes(group=0) = (%#v, %v)", nodes, err)
	}
}
