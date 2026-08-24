package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func BenchmarkListServerGroups1000(b *testing.B) {
	database := openBenchmarkStore(b, "groups.db")
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC).Unix()
	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	for index := 1; index <= 1_000; index++ {
		if _, err := tx.ExecContext(ctx, `INSERT INTO server_groups (id, name, created_at, updated_at) VALUES (?, ?, ?, ?)`, index, fmt.Sprintf("group-%04d", index), now, now); err != nil {
			b.Fatal(err)
		}
		result, err := tx.ExecContext(ctx, `
			INSERT INTO nodes (name, type, host, port, show, enabled, sort, created_at, updated_at)
			VALUES (?, 'vless', ?, '443', 1, 1, ?, ?, ?)
		`, fmt.Sprintf("node-%04d", index), fmt.Sprintf("node-%04d.example.test", index), index, now, now)
		if err != nil {
			b.Fatal(err)
		}
		nodeID, _ := result.LastInsertId()
		if _, err := tx.ExecContext(ctx, `INSERT INTO node_group_memberships (node_id, group_id) VALUES (?, ?)`, nodeID, index); err != nil {
			b.Fatal(err)
		}
	}
	for index := 1; index <= 10_000; index++ {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO users (email, password_hash, is_admin, banned, group_id, subscription_token, created_at, updated_at)
			VALUES (?, 'hash', 0, 0, ?, ?, ?, ?)
		`, fmt.Sprintf("user-%05d@example.test", index), (index-1)%1_000+1, testSubscriptionToken(b), now, now); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		groups, err := database.ListServerGroups(ctx)
		if err != nil || len(groups) != 1_000 {
			b.Fatalf("ListServerGroups() groups=%d err=%v", len(groups), err)
		}
	}
}

func BenchmarkGetNodeRuntime1000RoutingRules(b *testing.B) {
	database := openBenchmarkStore(b, "routes.db")
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC).Unix()
	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO server_groups (id, name, created_at, updated_at) VALUES (1, 'runtime', ?, ?)`, now, now); err != nil {
		b.Fatal(err)
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO nodes (name, type, host, port, show, enabled, sort, rate_micros, runtime_config, created_at, updated_at)
		VALUES ('route-node', 'vless', 'route.example.test', '443', 1, 1, 0, 1000000, '{"protocol":"vless","server_port":443}', ?, ?)
	`, now, now)
	if err != nil {
		b.Fatal(err)
	}
	nodeID, _ := result.LastInsertId()
	if _, err := tx.ExecContext(ctx, `INSERT INTO node_group_memberships (node_id, group_id) VALUES (?, 1)`, nodeID); err != nil {
		b.Fatal(err)
	}
	for index := 1; index <= 1_000; index++ {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO routing_rules (remarks, match_json, action, action_value, created_at, updated_at)
			VALUES (?, ?, 'direct', '', ?, ?)
		`, fmt.Sprintf("route-%04d", index), fmt.Sprintf(`["route-%04d.example.test"]`, index), now, now)
		if err != nil {
			b.Fatal(err)
		}
		routeID, _ := result.LastInsertId()
		if _, err := tx.ExecContext(ctx, `INSERT INTO node_route_memberships (node_id, route_id) VALUES (?, ?)`, nodeID, routeID); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		runtime, err := database.GetNodeRuntime(ctx, nodeID)
		if err != nil || len(runtime.Routes) != 1_000 {
			b.Fatalf("GetNodeRuntime() routes=%d err=%v", len(runtime.Routes), err)
		}
	}
}

func openBenchmarkStore(b *testing.B, name string) *Store {
	b.Helper()
	database, err := OpenSQLite("file:" + filepath.ToSlash(filepath.Join(b.TempDir(), name)))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(context.Background()); err != nil {
		b.Fatal(err)
	}
	return database
}
