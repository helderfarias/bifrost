package configstore

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditLogStoreFiltersAndOrdersEvents(t *testing.T) {
	store := setupRDBTestStore(t)
	require.NoError(t, store.DB().AutoMigrate(&tables.TableAuditLog{}))
	ctx := context.Background()
	now := time.Now().UTC()
	for _, event := range []tables.TableAuditLog{
		{ID: "audit-older", OccurredAt: now.Add(-time.Hour), Action: "create", Outcome: "success", Initiator: "admin", Target: "/api/providers", Method: "POST", Path: "/api/providers", RequestIP: "127.0.0.1", TypeURI: "test"},
		{ID: "audit-newer", OccurredAt: now, Action: "delete", Outcome: "failure", Initiator: "admin", Target: "/api/identity/users/1", Method: "DELETE", Path: "/api/identity/users/1", RequestIP: "127.0.0.1", TypeURI: "test"},
	} {
		require.NoError(t, store.CreateAuditLog(ctx, &event))
	}

	events, count, err := store.ListAuditLogs(ctx, AuditLogFilter{Search: "identity", Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
	require.Len(t, events, 1)
	assert.Equal(t, "audit-newer", events[0].ID)

	events, count, err = store.ListAuditLogs(ctx, AuditLogFilter{Outcome: "success", Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
	assert.Equal(t, "audit-older", events[0].ID)
}
