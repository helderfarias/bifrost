package tables

import "time"

// TableAPIGatewayRoute is a runtime-managed reverse-proxy route. Predicate and
// filter JSON are stored separately so route matching can be compiled once on
// every configuration update, never on the proxy request path.
type TableAPIGatewayRoute struct {
	ID                string    `gorm:"type:varchar(128);primaryKey" json:"id"`
	URI               string    `gorm:"type:varchar(2048);not null" json:"uri"`
	PredicatesJSON    string    `gorm:"type:text;not null" json:"-"`
	FiltersJSON       string    `gorm:"type:text;not null" json:"-"`
	VirtualKeyIDsJSON string    `gorm:"type:text;not null;default:'[]'" json:"-"`
	Enabled           bool      `gorm:"not null;default:true" json:"enabled"`
	CreatedAt         time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt         time.Time `gorm:"not null" json:"updated_at"`
}

func (TableAPIGatewayRoute) TableName() string { return "api_gateway_routes" }
