package configstore

import (
	"context"
	"strings"
	"time"

	"github.com/maximhq/bifrost/framework/configstore/tables"
)

// AuditLogFilter keeps filtering in the persistence layer, so exports and the
// dashboard always operate over the same result set.
type AuditLogFilter struct {
	Search  string
	Action  string
	Outcome string
	Start   *time.Time
	End     *time.Time
	Limit   int
	Offset  int
}

// AuditLogStore is optional to keep non-relational config stores compatible.
type AuditLogStore interface {
	CreateAuditLog(ctx context.Context, event *tables.TableAuditLog) error
	ListAuditLogs(ctx context.Context, filter AuditLogFilter) ([]tables.TableAuditLog, int64, error)
}

func (s *RDBConfigStore) CreateAuditLog(ctx context.Context, event *tables.TableAuditLog) error {
	return s.DB().WithContext(ctx).Create(event).Error
}

func (s *RDBConfigStore) ListAuditLogs(ctx context.Context, filter AuditLogFilter) ([]tables.TableAuditLog, int64, error) {
	db := s.DB().WithContext(ctx).Model(&tables.TableAuditLog{})
	if search := strings.TrimSpace(filter.Search); search != "" {
		like := "%" + search + "%"
		db = db.Where("initiator LIKE ? OR target LIKE ? OR path LIKE ? OR request_ip LIKE ?", like, like, like, like)
	}
	if filter.Action != "" {
		db = db.Where("action = ?", filter.Action)
	}
	if filter.Outcome != "" {
		db = db.Where("outcome = ?", filter.Outcome)
	}
	if filter.Start != nil {
		db = db.Where("occurred_at >= ?", *filter.Start)
	}
	if filter.End != nil {
		db = db.Where("occurred_at <= ?", *filter.End)
	}
	var count int64
	if err := db.Count(&count).Error; err != nil {
		return nil, 0, err
	}
	var events []tables.TableAuditLog
	err := db.Order("occurred_at DESC, id DESC").Limit(filter.Limit).Offset(filter.Offset).Find(&events).Error
	return events, count, err
}
