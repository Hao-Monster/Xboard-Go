package attachments

import (
	"context"
	"os"
	"time"
)

type StatusReport struct {
	Healthy             bool    `json:"healthy"`
	Root                string  `json:"root"`
	Readable            bool    `json:"readable"`
	Writable            bool    `json:"writable"`
	UsedBytes           int64   `json:"used_bytes"`
	ReservedBytes       int64   `json:"reserved_bytes"`
	QuotaBytes          int64   `json:"quota_bytes"`
	QuotaAvailableBytes int64   `json:"quota_available_bytes"`
	QuotaUsagePercent   float64 `json:"quota_usage_percent"`
	FilesystemFreeBytes *uint64 `json:"filesystem_free_bytes"`
	Error               string  `json:"error,omitempty"`
}

func (s *Service) StatusReport(ctx context.Context, now time.Time) StatusReport {
	report := StatusReport{Root: s.root, QuotaBytes: s.totalQuota}
	if info, err := os.Stat(s.root); err == nil && info.IsDir() {
		report.Readable = true
	}
	if file, err := os.CreateTemp(s.root, ".xboard-attachment-health-*"); err == nil {
		name := file.Name()
		if closeErr := file.Close(); closeErr == nil {
			report.Writable = true
		}
		_ = os.Remove(name)
	}
	used, reserved, err := s.database.KnowledgeAttachmentUsage(ctx, now)
	if err != nil {
		report.Error = err.Error()
		return report
	}
	report.UsedBytes = used
	report.ReservedBytes = reserved
	consumed := used + reserved
	if consumed < s.totalQuota {
		report.QuotaAvailableBytes = s.totalQuota - consumed
	}
	if s.totalQuota > 0 {
		report.QuotaUsagePercent = float64(consumed) / float64(s.totalQuota) * 100
	}
	report.FilesystemFreeBytes = filesystemFreeBytes(s.root)
	report.Healthy = report.Readable && report.Writable && consumed <= s.totalQuota
	return report
}
