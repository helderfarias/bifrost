package configstore

import (
	"context"
	"errors"
	"time"

	"github.com/maximhq/bifrost/framework/configstore/tables"
	"gorm.io/gorm"
)

// APIGatewayRouteStore is deliberately optional so existing ConfigStore test
// doubles and file-only deployments do not acquire a persistence requirement.
type APIGatewayRouteStore interface {
	ListAPIGatewayRoutes(ctx context.Context) ([]tables.TableAPIGatewayRoute, error)
	CreateAPIGatewayRoute(ctx context.Context, route *tables.TableAPIGatewayRoute) error
	UpdateAPIGatewayRoute(ctx context.Context, route *tables.TableAPIGatewayRoute) error
	DeleteAPIGatewayRoute(ctx context.Context, id string) error
}

func (s *RDBConfigStore) ListAPIGatewayRoutes(ctx context.Context) ([]tables.TableAPIGatewayRoute, error) {
	var routes []tables.TableAPIGatewayRoute
	err := s.DB().WithContext(ctx).Order("id ASC").Find(&routes).Error
	return routes, err
}

func (s *RDBConfigStore) CreateAPIGatewayRoute(ctx context.Context, route *tables.TableAPIGatewayRoute) error {
	if err := s.DB().WithContext(ctx).Create(route).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) || isUniqueConstraintError(err) {
			return ErrAlreadyExists
		}
		return err
	}
	return nil
}

func (s *RDBConfigStore) UpdateAPIGatewayRoute(ctx context.Context, route *tables.TableAPIGatewayRoute) error {
	result := s.DB().WithContext(ctx).Model(&tables.TableAPIGatewayRoute{}).Where("id = ?", route.ID).
		Updates(map[string]any{
			"uri":                  route.URI,
			"predicates_json":      route.PredicatesJSON,
			"filters_json":         route.FiltersJSON,
			"virtual_key_ids_json": route.VirtualKeyIDsJSON,
			"enabled":              route.Enabled,
			"updated_at":           time.Now().UTC(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *RDBConfigStore) DeleteAPIGatewayRoute(ctx context.Context, id string) error {
	result := s.DB().WithContext(ctx).Delete(&tables.TableAPIGatewayRoute{}, "id = ?", id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
