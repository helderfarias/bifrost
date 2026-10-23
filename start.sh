#!/bin/bash

export BIFROST_USER=admin@bifrost.local
export BIFROST_PASSWORD=bifrost-local-admin

export BIFROST_KEYCLOAK_ISSUER=http://localhost:8081/realms/bifrost-local
export BIFROST_KEYCLOAK_CLIENT_ID=bifrost-oidc
export BIFROST_KEYCLOAK_CLIENT_SECRET=bifrost-local-secret-change-me
export BIFROST_KEYCLOAK_REDIRECT_URI=http://localhost:3000/api/session/oidc/callback
export BIFROST_SETUP_TOKEN=566b6d8c3405e72e4133c940442b5995

make dev