package devicestate

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

const (
	OnlineWindow          = 300 * time.Second
	DatabaseThrottle      = 10 * time.Second
	DefaultFlushInterval  = 5 * time.Second
	DefaultFlushLimit     = 500
	MaximumFlushLimit     = 5_000
	MaximumDevicesPerUser = 64
)

var ErrNotImplemented = errors.New("Redis device state is not implemented")

type Summary struct {
	UserID      int64
	OnlineCount int
	ObservedAt  time.Time
}

type SummaryWriter func(context.Context, []Summary) error

type Options struct {
	URL              string
	Prefix           string
	WriteSummaries   SummaryWriter
	Logger           *slog.Logger
	DatabaseThrottle time.Duration
	FlushInterval    time.Duration
}

type Service interface {
	ReplaceNodeDevices(context.Context, int64, map[int64][]string, bool, time.Time) ([]int64, error)
	ListUserDevices(context.Context, []int64, time.Time) (map[int64][]string, error)
	ClearNodeDevices(context.Context, []int64, time.Time) ([]int64, error)
	ClearUserDevices(context.Context, []int64, time.Time) ([]int64, error)
	FlushPending(context.Context, time.Time, int) (int, error)
	Run(context.Context)
	Close() error
}

type RedisService struct{}

func NewRedis(context.Context, Options) (*RedisService, error) {
	return &RedisService{}, nil
}

func (*RedisService) ReplaceNodeDevices(context.Context, int64, map[int64][]string, bool, time.Time) ([]int64, error) {
	return nil, ErrNotImplemented
}

func (*RedisService) ListUserDevices(context.Context, []int64, time.Time) (map[int64][]string, error) {
	return nil, ErrNotImplemented
}

func (*RedisService) ClearNodeDevices(context.Context, []int64, time.Time) ([]int64, error) {
	return nil, ErrNotImplemented
}

func (*RedisService) ClearUserDevices(context.Context, []int64, time.Time) ([]int64, error) {
	return nil, ErrNotImplemented
}

func (*RedisService) FlushPending(context.Context, time.Time, int) (int, error) {
	return 0, ErrNotImplemented
}

func (*RedisService) Run(context.Context) {}

func (*RedisService) Close() error { return nil }
