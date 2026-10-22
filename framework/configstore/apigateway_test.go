package configstore

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIGatewayRouteStoreCRUD(t *testing.T) {
	store := setupRDBTestStore(t)
	require.NoError(t, store.DB().AutoMigrate(&tables.TableAPIGatewayRoute{}))
	ctx := context.Background()
	route := &tables.TableAPIGatewayRoute{
		ID:                "health-proxy",
		URI:               "http://localhost:7001",
		PredicatesJSON:    `["Path=/health"]`,
		FiltersJSON:       `["RewritePath=/health,/actuator/health"]`,
		VirtualKeyIDsJSON: `["vk-health"]`,
		Enabled:           true,
	}
	require.NoError(t, store.CreateAPIGatewayRoute(ctx, route))
	require.ErrorIs(t, store.CreateAPIGatewayRoute(ctx, route), ErrAlreadyExists)

	routes, err := store.ListAPIGatewayRoutes(ctx)
	require.NoError(t, err)
	require.Len(t, routes, 1)
	assert.Equal(t, route.URI, routes[0].URI)
	assert.Equal(t, route.VirtualKeyIDsJSON, routes[0].VirtualKeyIDsJSON)

	route.Enabled = false
	require.NoError(t, store.UpdateAPIGatewayRoute(ctx, route))
	routes, err = store.ListAPIGatewayRoutes(ctx)
	require.NoError(t, err)
	assert.False(t, routes[0].Enabled)

	require.NoError(t, store.DeleteAPIGatewayRoute(ctx, route.ID))
	require.ErrorIs(t, store.DeleteAPIGatewayRoute(ctx, route.ID), ErrNotFound)
}
