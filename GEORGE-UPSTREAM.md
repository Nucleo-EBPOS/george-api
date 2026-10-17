# George × upstream (`lejianwen/rustdesk-api`)

Este fork mantém o upstream sincronizável: **tudo do George vive em `george/`**
(pacotes novos) e o upstream é tocado em apenas 4 pontos, todos marcados com
comentário `// george:`.

## Pontos de contato com o upstream

| Arquivo upstream | O que foi mudado | Por quê |
|---|---|---|
| `cmd/apimain.go` | +2 imports; `gconfig.Load()` + `gconfig.ApplyOverrides(&global.Config)` logo após `config.Init`; `george.Init(global.DB, global.Logger)` no fim de `InitGlobal()` | Ler `GEORGE_*`, forçar Postgres/JWT/servidores RustDesk; rodar migrações, hooks e fila |
| `http/http.go` | `georgehttp.Register(g, global.Logger)` logo após o `g.Use(...)` do upstream | Middlewares (request_id, log JSON, CORS, headers) e rotas `/api/v1`, `/healthz`, `/metrics` |
| `utils/password.go` | `EncryptPassword` → argon2id; `VerifyPassword` aceita argon2id, bcrypt e MD5 legado, devolvendo hash novo para migração transparente | Regra 5 da spec (argon2id) sem quebrar `/api/login` dos clientes RustDesk |
| `cmd/george_import.go` (novo, mesmo pacote `main`) | subcomando `import-sqlite` registrado via `init()` | Importar o SQLite da produção atual sem editar `apimain.go` além do acima |

Arquivos novos fora de `george/`: `Dockerfile.george`, `.github/workflows/george.yml`,
`.env.example`, `README-GEORGE.md`, este arquivo.

## O que NÃO foi alterado (de propósito)

- Rotas que o cliente RustDesk usa (`/api/login`, `/api/ab*`, `/api/peers`, `/api/users`,
  `/api/oidc/*`, `/api/heartbeat`, `/api/sysinfo`, `/api/audit/*`): intactas. O isolamento
  por org nessas rotas vem do próprio modelo do upstream (cada org = um `groups` upstream;
  `/api/users` e `/api/peers` já listam por grupo).
- Modelos e `AutoMigrate` do upstream: intactos. O George acrescenta colunas/tabelas via
  **migrações SQL numeradas** em `george/migrate/migrations/` (`ADD COLUMN IF NOT EXISTS`),
  que rodam depois do `AutoMigrate`. Nunca editamos os structs do upstream.
- Ingestão `audit_conns → sessions` e preenchimento de `org_id` em `peers/address_books/tags`
  são **callbacks do gorm** registrados em `george/service/session.go` (`RegisterHooks`),
  sem tocar nos controllers do upstream.

## Como sincronizar com o upstream

```bash
git fetch upstream
git merge upstream/master        # conflitos possíveis só nos 3 arquivos da tabela acima
go build ./cmd && go test ./george/...
```

Se o upstream mudar a assinatura de `utils.VerifyPassword`, ajustar só esse arquivo.
Se o upstream adicionar tabelas com `user_id`, avaliar incluir na lista de `backfillOrg`
(`george/service/session.go`) e em `cmd/george_import.go`.

## Layout de `george/`

```
george/
  config/    GEORGE_* → struct + ApplyOverrides (Postgres, JWT, ID/relay/key)
  migrate/   runner + migrations/0001..0005 (schema_migrations)
  model/     structs das tabelas George (+ visões de users/peers/device_groups)
  auth/      argon2id, JWT HS256 (15 min), AES-GCM, TOTP, geradores de código
  perm/      Can(actor, action, resource) — puro, testado
  service/   regras: org, auth, device, invite, session, audit, billing, jobs
  httpapi/   gin: middlewares + handlers /api/v1, /healthz, /metrics, releases
  openapi/   gerador do /api/v1/openapi.json a partir da tabela de rotas
  ai/        cliente Claude (resumo de sessão, busca natural) + fallback heurístico
  ratelimit/ limitador em memória por IP
```
