#!/bin/sh
set -eu

KCADM=/opt/keycloak/bin/kcadm.sh

until "$KCADM" config credentials \
  --server "$KEYCLOAK_URL" \
  --realm master \
  --user "$KEYCLOAK_ADMIN" \
  --password "$KEYCLOAK_ADMIN_PASSWORD" >/dev/null 2>&1; do
  sleep 2
done

client_id="$($KCADM get clients -r "$KEYCLOAK_REALM" -q "clientId=$BIFROST_CLIENT_ID" --fields id --format csv --noquotes)"
realm_management_id="$($KCADM get clients -r "$KEYCLOAK_REALM" -q "clientId=realm-management" --fields id --format csv --noquotes)"
service_account_id="$($KCADM get "clients/$client_id/service-account-user" -r "$KEYCLOAK_REALM" --fields id --format csv --noquotes)"

for role in view-users view-realm query-groups; do
  role_id="$($KCADM get "clients/$realm_management_id/roles/$role" -r "$KEYCLOAK_REALM" --fields id --format csv --noquotes)"
  "$KCADM" create "users/$service_account_id/role-mappings/clients/$realm_management_id" \
    -r "$KEYCLOAK_REALM" \
    -b "[{\"id\":\"$role_id\",\"name\":\"$role\",\"clientRole\":true,\"containerId\":\"$realm_management_id\"}]" >/dev/null
done

echo "Configured Keycloak service-account roles for $BIFROST_CLIENT_ID."
