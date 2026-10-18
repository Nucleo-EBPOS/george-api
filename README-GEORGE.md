# george-api

API do **George** (acesso remoto sobre RustDesk): contas, organizações multi-empresa,
dispositivos com Modo Host, convites por link, auditoria/sessões, billing (preparado) e
pontos de extensão de IA. Fork de `lejianwen/rustdesk-api` — as rotas que o cliente
RustDesk usa continuam funcionando (ver `GEORGE-UPSTREAM.md`).

- Contrato: `GET /api/v1/openapi.json` · Saúde: `GET /healthz` · Métricas: `GET /metrics`
- Erros: `{ "error": { "code", "message", "details?" } }` · Listas: `{ "data", "meta": { "total", "page", "per_page" } }`
- Auth: `Authorization: Bearer <access_token>` (JWT 15 min; refresh rotativo 30 dias).
  O `access_token` do `/api/login` do cliente RustDesk também é aceito nas rotas `/api/v1`.

## Rodar em desenvolvimento

```bash
# Postgres próprio (porta 5433)
docker run -d --name george-dev-pg -e POSTGRES_USER=george -e POSTGRES_PASSWORD=george \
  -e POSTGRES_DB=george -p 127.0.0.1:5433:5432 -v george-dev-pg:/var/lib/postgresql/data postgres:16-alpine
docker exec george-dev-pg psql -U george -c 'create database george_test;'

export PATH=$PATH:/usr/local/go/bin
export GEORGE_DB_URL='postgres://george:george@127.0.0.1:5433/george?sslmode=disable'
export GEORGE_JWT_SECRET="$(openssl rand -base64 48)"
export GEORGE_CORS_ORIGINS='http://localhost:5173,https://georgedesk.com'
export GEORGE_PUBLIC_URL='http://127.0.0.1:21214'
export RUSTDESK_API_GIN_API_ADDR='0.0.0.0:21214' RUSTDESK_API_LANG=en RUSTDESK_API_APP_WEB_CLIENT=0
go build -o apimain-dev ./cmd && ./apimain-dev            # migrações rodam sozinhas no boot

go vet ./george/... ./cmd/...
GEORGE_TEST_DB_URL='postgres://george:george@127.0.0.1:5433/george_test?sslmode=disable' go test ./george/... -count=1
```

Variáveis: ver `.env.example`.

## Fluxos (curl)

```bash
API=http://127.0.0.1:21214/api/v1
J='Content-Type: application/json'

# 1) signup → cria org + owner e já devolve tokens
curl -s $API/auth/signup -H "$J" -d '{"email":"owner@acme.test","password":"senha-forte-123","name":"Ana","org_name":"ACME"}'
# 2) login (se 2FA ativo, enviar também "totp_code")
TOK=$(curl -s $API/auth/login -H "$J" -d '{"email":"owner@acme.test","password":"senha-forte-123"}' | jq -r .data.tokens.access_token)
A="Authorization: Bearer $TOK"
curl -s $API/me -H "$A"
# 3) heartbeat do dispositivo (o cliente George chama a cada ~30s)
curl -s $API/devices/heartbeat -H "$A" -H "$J" -d '{"id":"123456789","uuid":"uuid-1","hostname":"PC-RECEPCAO","platform":"windows","version":"1.4.2"}'
# 4) host OFF / ON
curl -s -X PATCH $API/devices/1 -H "$A" -H "$J" -d '{"host_enabled":false}'
curl -s -X PATCH $API/devices/1 -H "$A" -H "$J" -d '{"host_enabled":true,"name":"Recepção"}'
# 5) convite por link (uso único, 1h) — a senha de sessão NÃO volta para quem cria
curl -s $API/invites -H "$A" -H "$J" -d '{"device_id":1,"expires_in":3600}'   # → code, url, deep_link
# 6) convidado: dados públicos e consumo (público, rate-limit por IP)
curl -s $API/invites/AB7K2M
curl -s -X POST $API/invites/AB7K2M/consume                                   # → session_password, rustdesk_id, id/relay/key
# 7) auditoria lista tudo (signup, login, register, host_on/off, invite.create/consume...)
curl -s "$API/org/audit?per_page=50" -H "$A"
# 8) refresh / logout
curl -s $API/auth/refresh -H "$J" -d '{"refresh_token":"<refresh>"}'
curl -s -X POST $API/auth/logout -H "$J" -d '{"refresh_token":"<refresh>"}'
```

Outros: `GET /devices?q=&online=&host_enabled=&group_id=`, `GET /devices/search?q=windows offline`,
`POST /devices/{id}/share {"target_type":"user","target_id":2,"level":"connect"}`,
`GET/POST/PATCH/DELETE /groups`, `POST /groups/{id}/devices`, `GET /sessions`,
`POST /sessions/{id}/summary` (202 + `job_id`; `GET /jobs/{id}`), `GET/PATCH /org`,
`GET/POST/PATCH/DELETE /org/users`, `GET/PATCH /org/branding`, `GET/PATCH /org/sso`,
`GET /org/billing`, `POST /org/billing/portal`, `POST /webhooks/stripe`, `GET /ops/orgs`,
`GET /ops/health`, `GET /releases/latest`, `POST /auth/2fa/{setup,verify,disable}`,
`POST /auth/forgot|reset`.

## Papéis e permissões (`george/perm`)

| Ação | owner/admin | tech | viewer |
|---|---|---|---|
| ver dispositivos | todos da org | os seus + compartilhados + **PCs vinculados da org** | todos (leitura) |
| conectar | sim | seus + share `connect`/`manage` + **PCs vinculados da org** (inclui `GET /devices/{id}/credentials`) | não |
| gerenciar (nome, setor, host on/off, apagar, compartilhar) | sim | seus + share `manage` (PC vinculado: só com share `manage`) | não |
| PC vinculado: rotacionar senha / revogar vínculo | sim | não | não |
| códigos de vínculo (`/enrollment-keys`) | criar/listar/revogar | só listar | não |
| convites | criar/listar todos/revogar | criar (para o que acessa) e os próprios | não |
| grupos, usuários, branding, SSO, billing, auditoria | sim | não | não |
| `ops/*` | só `is_ops` (`GEORGE_OPS_EMAILS`) | | |

Limites do plano (`plans.max_devices`, `max_techs`) são aplicados em `device.add`
(heartbeat de dispositivo novo → `402 plan_limit_reached`), em `POST /enroll` (PC novo na org) e `user.add`.

**PCs vinculados (enrollment)** têm `user_id = 0` (são da org, não de um usuário). Regra para
técnicos: enquanto o vínculo está ativo (não revogado), todo `tech` da org **vê e conecta**
(é o caso de uso da aba "Organização" do cliente); **gerenciar** (renomear/mover/apagar) exige
owner/admin ou share `manage`; **rotacionar senha e revogar** são só owner/admin. PC revogado
some da lista dos técnicos (owner/admin continuam vendo, com `revoked: true`). O `/api/sysinfo`
do upstream não consegue dar dono a um PC vinculado (hook do gorm mantém `user_id = 0`).

## Convites

Código de 6 caracteres (base32 sem ambíguos), gerado no servidor, único. Senha de
sessão gerada na criação, **cifrada (AES-GCM)** e entregue:
1. ao **host** no `pending_invites` do heartbeat (para aplicar como senha temporária);
2. ao **convidado** uma única vez em `POST /invites/{code}/consume` (atômico; após o último uso a senha é apagada).
Expiração, `max_uses`, revogação e consumo são auditados.

## Enrollment de PCs (spec `docs/specs/06-enrollment.md`)

Código de vínculo da org (`XXXX-XXXX-XXXX`, reutilizável até `max_uses`, `0` = ilimitado) →
o PC chama `POST /enroll` (público, 20/min/IP) → recebe **uma vez** o `device_token`
(a API guarda só o SHA-256) e a senha permanente (AES-GCM em repouso, versionada) → o agente
faz `POST /agent/heartbeat` com `Authorization: Device <token>` a cada `heartbeat_interval` s.
O token de dispositivo **não** vale em nenhuma rota de usuário (`Auth()` só lê `Bearer`), e JWT
de usuário **não** vale no agente.

```bash
# admin: cria o código (setor opcional, validade opcional)
curl -s $API/enrollment-keys -H "$A" -H "$J" -d '{"label":"Notebooks financeiro","group_id":3,"max_uses":0,"expires_in_days":30}'
#   → data.code "ABCD-EFGH-JKMN", data.url "https://georgedesk.com/e/ABCD-EFGH-JKMN", data.status "active"
curl -s $API/enrollment-keys -H "$A"                      # tech também lê
# PC: vincula (sem "password" a API gera 14 chars; com "password" valida mín. 8)
curl -s $API/enroll -H "$J" -d '{"code":"ABCD-EFGH-JKMN","rustdesk_id":"123456789","uuid":"<base64>","hostname":"NOTE-EB101",
  "platform":"windows","os":"Windows 11 Pro 23H2","version":"0.2.0","username":"maria"}'
#   → data.device{id,name,group_id,group_name}, data.org{id,name}, data.device_token, data.password{version,value}, data.heartbeat_interval
#   erros: 404 enrollment_key_invalid (inexistente/revogado/expirado/esgotado — resposta única),
#          409 device_other_org, 402 plan_limit_reached, 422 validation_error
DT="Authorization: Device <device_token>"
curl -s $API/agent/heartbeat -H "$DT" -H "$J" -d '{"rustdesk_id":"123456789","uuid":"<base64>","hostname":"NOTE-EB101",
  "os":"Windows 11 Pro 23H2","version":"0.2.0","unattended":true,"password_version":1}'
#   → data{name,group_name,org_name,password:null|{version,value},heartbeat_interval}; 401 device_token_invalid; 410 device_revoked
# admin/técnico
curl -s "$API/devices?enrolled=true" -H "$A"              # + enrolled, enrolled_at, unattended, password_version, password_applied, revoked, last_agent_at, group_name, online
curl -s -X PATCH $API/devices/42 -H "$A" -H "$J" -d '{"alias":"Maria - Financeiro","group_id":4}'   # audita device.rename / device.move
curl -s -X POST $API/devices/42/rotate-password -H "$A"   # → data.password_version (PC aplica no próximo heartbeat)
curl -s $API/devices/42/credentials -H "$A"               # → rustdesk_id, password, password_version, password_applied (auditado)
curl -s -X POST $API/devices/42/revoke -H "$A"            # 204; agente passa a receber 410 e apaga token+senha
curl -s -X DELETE $API/enrollment-keys/7 -H "$A"          # 204; PCs já vinculados continuam
```

- **Re-vínculo** (mesmo `uuid`, mesma org): token novo (o antigo morre), mantém nome/setor/senha
  e devolve a senha atual em `password.value` (se vier `password` diferente no corpo, vira nova versão).
  PC revogado pode ser re-vinculado com um código válido (gera senha nova). Todo vínculo bem-sucedido
  consome 1 uso do código; falhas não consomem.
- `password_applied` = `password_version > 0 && pw_applied_version >= password_version`
  (o agente informa a versão aplicada em `password_version` no heartbeat).
- Revogar mantém o hash do token só para responder `410` ao agente; a senha cifrada é apagada.
- `/agent/heartbeat` não tem rate-limit por IP (vários PCs atrás do mesmo NAT); autenticado por token.
- Auditoria: `enrollment_key.create|revoke`, `device.enroll|rename|move|rotate_password|revoke|credentials.read`.

## Importar o SQLite da produção atual

```bash
GEORGE_DB_URL=... ./apimain import-sqlite /caminho/rustdeskapi.db "George (legado)"
```
Copia todas as tabelas do upstream (ids preservados, sequences ajustadas), cria a org
"legado", marca o admin upstream como `owner`, demais como `tech`, e gera `sessions` a partir
de `audit_conns`. Idempotente.

## Imagem Docker

`Dockerfile.george` (multi-stage). CI em `.github/workflows/george.yml` roda vet/test
(Postgres de serviço) e publica `ghcr.io/nucleo-ebpos/george-api:{latest,sha-…,vX.Y.Z}`.

## Limitações conhecidas (F1)

- **`/api/users` do cliente stock** lista só o próprio usuário para quem não é `is_admin`
  do upstream (comportamento original). O `is_admin` do upstream é **global** (painel
  `/_admin` de todas as orgs), por isso nenhum usuário George recebe essa flag; a lista de
  membros da org fica em `GET /api/v1/org/users`.
- **Painel `/_admin` e rotas `/api/admin/*` do upstream** continuam existindo e enxergam
  todas as orgs. O usuário `admin` (id=1) semeado no primeiro boot deve ter a senha guardada
  no cofre ou ser desativado; o ideal é bloquear `/_admin` e `/api/admin` no Caddy.
- **E-mail**: sem SMTP, `POST /auth/forgot` só registra o link no log (job `email`).
- **Rate-limit** é em memória (por instância); com mais de uma réplica, mover para Redis.
- **IA**: sem `GEORGE_AI_KEY`, o resumo de sessão usa um texto factual (template) e a busca
  natural usa heurística (`provider: "heuristic"`).
