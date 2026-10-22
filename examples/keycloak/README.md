# Keycloak local para Bifrost

Este ambiente é exclusivamente para desenvolvimento local. As credenciais são
intencionalmente públicas e não podem ser reutilizadas fora da máquina local.

```bash
docker compose -f examples/keycloak/docker-compose.yml up -d
```

- Keycloak Admin Console: `http://localhost:8081/admin`
- Admin do Keycloak: `admin` / `admin`
- Realm: `bifrost-local`
- Usuário de teste: `admin@bifrost.local` / `bifrost-local-admin`
- OIDC issuer: `http://localhost:8081/realms/bifrost-local`
- Client ID: `bifrost-oidc`
- Client secret: `bifrost-local-secret-change-me`

O client importa os grupos `/Engineering` e `/Platform`, e o papel de realm
`bifrost-admin`. O serviço de inicialização atribui à service account somente
`view-users`, `view-realm` e `query-groups`, necessários para sincronização de
leitura do Keycloak Admin API.

Para recomeçar o realm do zero, remova apenas o volume dedicado e suba os
serviços novamente:

```bash
docker compose -f examples/keycloak/docker-compose.yml down -v
docker compose -f examples/keycloak/docker-compose.yml up -d
```

O Bifrost continuará em `http://localhost:8080` e o dashboard de desenvolvimento
em `http://localhost:3000`. A integração OIDC usa por padrão o callback
`http://localhost:3000/api/session/oidc/callback`.

Para habilitar a sincronização de usuários ao executar o Bifrost localmente,
exporte estas variáveis antes de iniciar o servidor:

```bash
export BIFROST_KEYCLOAK_ISSUER=http://localhost:8081/realms/bifrost-local
export BIFROST_KEYCLOAK_CLIENT_ID=bifrost-oidc
export BIFROST_KEYCLOAK_CLIENT_SECRET=bifrost-local-secret-change-me
export BIFROST_KEYCLOAK_REDIRECT_URI=http://localhost:3000/api/session/oidc/callback
```

When these variables are set, Bifrost synchronizes users and their mapped
realm roles once at startup. The **Governance → Users → Sync Keycloak** action
can be used at any time to perform another on-demand synchronization.
