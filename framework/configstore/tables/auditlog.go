package tables

import "time"

// TableAuditLog is an immutable administrative activity record. Payloads are
// deliberately not stored here: an audit trail records who changed which
// resource, without copying credentials or request bodies into a second store.
type TableAuditLog struct {
	ID         string    `gorm:"primaryKey;type:varchar(64)" json:"id"`
	OccurredAt time.Time `gorm:"index;not null" json:"occurred_at"`
	Action     string    `gorm:"index;type:varchar(32);not null" json:"action"`
	Outcome    string    `gorm:"index;type:varchar(16);not null" json:"outcome"`
	Initiator  string    `gorm:"index;type:varchar(255);not null" json:"initiator"`
	Target     string    `gorm:"index;type:text;not null" json:"target"`
	Method     string    `gorm:"type:varchar(12);not null" json:"method"`
	Path       string    `gorm:"type:text;not null" json:"path"`
	RequestIP  string    `gorm:"index;type:varchar(64);not null" json:"request_ip"`
	UserAgent  string    `gorm:"type:text;not null" json:"user_agent"`
	DurationMS int64     `gorm:"not null" json:"duration_ms"`
	TypeURI    string    `gorm:"type:varchar(255);not null" json:"type_uri"`
	Signature  string    `gorm:"type:varchar(128);not null" json:"signature,omitempty"`
}

func (TableAuditLog) TableName() string { return "audit_logs" }
