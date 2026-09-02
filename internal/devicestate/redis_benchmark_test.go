package devicestate

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func BenchmarkTIMENODE006ReplaceSnapshot500Users(b *testing.B) {
	service := newTIMENODE006BenchmarkService(b)
	ctx := context.Background()
	devices := make(map[int64][]string, redisOperationBatch)
	for userID := int64(1); userID <= redisOperationBatch; userID++ {
		devices[userID] = []string{fmt.Sprintf("192.0.%d.%d", userID/256, userID%256)}
	}
	now := time.Now().UTC()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := service.ReplaceNodeDevices(ctx, 1, devices, true, now); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTIMENODE006List500Users(b *testing.B) {
	service := newTIMENODE006BenchmarkService(b)
	ctx := context.Background()
	devices := make(map[int64][]string, redisOperationBatch)
	userIDs := make([]int64, 0, redisOperationBatch)
	for userID := int64(1); userID <= redisOperationBatch; userID++ {
		devices[userID] = []string{fmt.Sprintf("198.51.%d.%d", userID/256, userID%256)}
		userIDs = append(userIDs, userID)
	}
	now := time.Now().UTC()
	if _, err := service.ReplaceNodeDevices(ctx, 1, devices, true, now); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := service.ListUserDevices(ctx, userIDs, now); err != nil {
			b.Fatal(err)
		}
	}
}

func newTIMENODE006BenchmarkService(b *testing.B) *RedisService {
	b.Helper()
	rawURL := os.Getenv("XBOARD_TEST_REDIS_URL")
	if rawURL == "" {
		b.Skip("XBOARD_TEST_REDIS_URL is required for Redis device-state benchmarks")
	}
	prefix := "xboard-go:bench-device:" + uuid.NewString() + ":"
	service, err := NewRedis(context.Background(), Options{
		URL: rawURL, Prefix: prefix, DatabaseThrottle: time.Minute,
		WriteSummaries: func(context.Context, []Summary) error { return nil },
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = service.Close()
		options, parseErr := redis.ParseURL(rawURL)
		if parseErr != nil {
			return
		}
		client := redis.NewClient(options)
		defer client.Close()
		var cursor uint64
		for {
			keys, next, scanErr := client.Scan(context.Background(), cursor, prefix+"*", 500).Result()
			if scanErr != nil {
				return
			}
			if len(keys) > 0 {
				_ = client.Del(context.Background(), keys...).Err()
			}
			cursor = next
			if cursor == 0 {
				return
			}
		}
	})
	return service
}
