// Package openapi gera o documento OpenAPI 3.0 de /api/v1 a partir de uma
// tabela de rotas, garantindo um JSON sempre válido e alinhado ao router.
package openapi

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
)

type obj = map[string]interface{}

type route struct {
	method, path, tag, summary string
	auth                       bool
	body                       obj // schema do request (nil = sem corpo)
	resp                       obj // schema do data (nil = genérico)
	status                     int
	params                     []obj
}

func str(desc string) obj { return obj{"type": "string", "description": desc} }
func boolean() obj        { return obj{"type": "boolean"} }
func integer() obj        { return obj{"type": "integer"} }
func ref(name string) obj { return obj{"$ref": "#/components/schemas/" + name} }
func arr(items obj) obj   { return obj{"type": "array", "items": items} }
func object(props obj, req ...string) obj {
	o := obj{"type": "object", "properties": props}
	if len(req) > 0 {
		o["required"] = req
	}
	return o
}
func q(name, desc string, schema obj) obj {
	return obj{"name": name, "in": "query", "description": desc, "schema": schema}
}
func pathP(name string) obj {
	return obj{"name": name, "in": "path", "required": true, "schema": obj{"type": "string"}}
}

var schemas = obj{
	"Error": object(obj{"error": object(obj{"code": str(""), "message": str(""), "details": obj{}}, "code", "message")}, "error"),
	"Meta":  object(obj{"total": integer(), "page": integer(), "per_page": integer()}),
	"Tokens": object(obj{"access_token": str("JWT de 15 min"), "refresh_token": str("rotativo, 30 dias"),
		"token_type": str(""), "expires_in": integer()}),
	"User": object(obj{"id": integer(), "email": str(""), "name": str(""), "role": obj{"type": "string", "enum": []string{"owner", "admin", "tech", "viewer"}},
		"org_id": integer(), "is_ops": boolean(), "totp_enabled": boolean(), "status": integer(), "created_at": str("date-time")}),
	"Org": object(obj{"id": integer(), "name": str(""), "slug": str(""), "status": integer(), "created_at": str("")}),
	"Device": object(obj{"id": integer(), "rustdesk_id": str(""), "name": str(""), "hostname": str(""), "alias": str(""), "os": str(""),
		"platform": str(""), "version": str(""), "username": str(""), "owner_user_id": integer(), "org_id": integer(), "group_id": integer(),
		"host_enabled": boolean(), "last_seen": integer(), "last_ip": str(""), "online": boolean(), "shares": arr(ref("DeviceShare")),
		"group_name": str(""), "enrolled": boolean(), "enrolled_at": str("date-time | null"), "enrollment_key_id": integer(),
		"unattended": boolean(), "password_version": integer(), "password_applied": boolean(), "revoked": boolean(),
		"revoked_at": str("date-time | null"), "last_agent_at": integer()}),
	"EnrollmentKey": object(obj{"id": integer(), "org_id": integer(), "code": str("XXXX-XXXX-XXXX"), "label": str(""), "group_id": integer(), "group_name": str(""),
		"max_uses": integer(), "uses": integer(), "expires_at": str("date-time | null"), "revoked_at": str("date-time | null"), "created_by": integer(),
		"created_at": str(""), "status": obj{"type": "string", "enum": []string{"active", "expired", "exhausted", "revoked"}}, "url": str("https://…/e/<code>"), "deep_link": str("george://enroll/<code>")}),
	"Enroll": object(obj{"code": str("XXXX-XXXX-XXXX"), "rustdesk_id": str(""), "uuid": str(""), "hostname": str(""), "platform": str(""), "os": str(""),
		"version": str(""), "username": str(""), "password": str("opcional, mín. 8")}, "code", "rustdesk_id", "uuid"),
	"AgentPassword": object(obj{"version": integer(), "value": str("")}),
	"EnrollOut": object(obj{"device": object(obj{"id": integer(), "name": str(""), "group_id": integer(), "group_name": str("")}),
		"org": object(obj{"id": integer(), "name": str("")}), "device_token": str("base64url 32B — só nesta resposta"),
		"password": ref("AgentPassword"), "heartbeat_interval": integer()}),
	"AgentHeartbeat": object(obj{"rustdesk_id": str(""), "uuid": str(""), "hostname": str(""), "os": str(""), "version": str(""),
		"unattended": boolean(), "password_version": integer()}),
	"AgentHeartbeatOut": object(obj{"name": str(""), "group_name": str(""), "org_name": str(""),
		"password": obj{"allOf": []obj{ref("AgentPassword")}, "nullable": true}, "heartbeat_interval": integer()}),
	"DeviceCredentials": object(obj{"rustdesk_id": str(""), "password": str(""), "password_version": integer(), "password_applied": boolean()}),
	"DeviceShare": object(obj{"id": integer(), "device_id": integer(), "target_type": obj{"type": "string", "enum": []string{"user", "group"}},
		"target_id": integer(), "level": obj{"type": "string", "enum": []string{"connect", "manage"}}}),
	"Invite": object(obj{"id": integer(), "code": str(""), "device_id": integer(), "expires_at": str(""), "max_uses": integer(), "uses": integer(),
		"status": obj{"type": "string", "enum": []string{"active", "expired", "consumed", "revoked"}}, "url": str(""), "deep_link": str(""), "note": str("")}),
	"PublicInvite": object(obj{"code": str(""), "status": str(""), "expires_at": str(""), "remaining_uses": integer(), "device_name": str(""),
		"rustdesk_id": str(""), "product_name": str(""), "logo_url": str(""), "primary_color": str("")}),
	"ConsumeOut": object(obj{"code": str(""), "rustdesk_id": str(""), "session_password": str("uso único"), "id_server": str(""),
		"relay_server": str(""), "key": str(""), "remaining_uses": integer()}),
	"Group": object(obj{"id": integer(), "org_id": integer(), "name": str("")}),
	"Session": object(obj{"id": integer(), "org_id": integer(), "device_id": integer(), "peer_id": str(""), "from_peer": str(""), "from_name": str(""),
		"from_user_id": integer(), "started_at": str(""), "ended_at": str(""), "bytes": integer(), "summary": str(""), "summary_status": str("")}),
	"AuditLog": object(obj{"id": integer(), "org_id": integer(), "actor_id": integer(), "actor_name": str(""), "action": str(""), "target_type": str(""),
		"target_id": str(""), "ip": str(""), "request_id": str(""), "meta": obj{"type": "object"}, "created_at": str("")}),
	"Branding": object(obj{"org_id": integer(), "product_name": str(""), "logo_url": str(""), "primary_color": str("#RRGGBB")}),
	"Sso":      object(obj{"org_id": integer(), "enabled": boolean(), "issuer": str(""), "client_id": str(""), "scopes": str(""), "has_client_secret": boolean()}),
	"Plan":     object(obj{"id": integer(), "code": str(""), "name": str(""), "max_devices": integer(), "max_techs": integer(), "price_cents": integer()}),
	"Billing": object(obj{"plan": ref("Plan"), "subscription": obj{"type": "object"}, "usage": object(obj{"MaxDevices": integer(), "MaxTechs": integer(), "Devices": integer(), "Techs": integer()}),
		"stripe_configured": boolean(), "has_stripe_customer": boolean()}),
	"Job":       object(obj{"id": integer(), "type": str(""), "status": obj{"type": "string", "enum": []string{"pending", "running", "done", "failed"}}, "result": obj{"type": "object"}, "last_error": str("")}),
	"Heartbeat": object(obj{"id": str("ID RustDesk"), "uuid": str(""), "hostname": str(""), "platform": str(""), "os": str(""), "version": str(""), "username": str(""), "host_enabled": boolean()}, "id"),
	"HeartbeatOut": object(obj{"device": ref("Device"), "host_enabled": boolean(), "server_time": str(""),
		"pending_invites": arr(object(obj{"code": str(""), "session_password": str(""), "expires_at": str("")}))}),
	"Release": object(obj{"version": str(""), "tag": str(""), "published_at": str(""), "notes": str(""), "url": str(""),
		"assets": arr(object(obj{"name": str(""), "url": str(""), "size": integer(), "platform": str("")}))}),
	"Health": object(obj{"status": str(""), "version": str(""), "db": str(""), "uptime_s": integer()}),
}

var routes = []route{
	{"get", "/healthz", "Infra", "Saúde do serviço", false, nil, ref("Health"), 200, nil},
	{"get", "/openapi.json", "Infra", "Este documento", false, nil, obj{"type": "object"}, 200, nil},
	{"get", "/releases/latest", "Infra", "Manifest da última versão do cliente", false, nil, ref("Release"), 200, nil},
	{"post", "/releases/latest", "Infra", "Verificação de atualização do cliente (formato RustDesk, sem envelope)", false, obj{"type": "object"}, object(obj{"url": str("página do release")}), 200, nil},

	{"post", "/auth/signup", "Auth", "Cria org + owner", false, object(obj{"email": str(""), "password": str("mín. 10"), "name": str(""), "org_name": str("")}, "email", "password"),
		object(obj{"tokens": ref("Tokens"), "user": ref("User"), "org": ref("Org")}), 201, nil},
	{"post", "/auth/login", "Auth", "Login (e-mail/senha + totp_code se 2FA)", false, object(obj{"email": str(""), "password": str(""), "totp_code": str("")}, "email", "password"),
		object(obj{"tokens": ref("Tokens"), "user": ref("User"), "org": ref("Org")}), 200, nil},
	{"post", "/auth/refresh", "Auth", "Rotaciona refresh token", false, object(obj{"refresh_token": str("")}, "refresh_token"), object(obj{"tokens": ref("Tokens"), "user": ref("User")}), 200, nil},
	{"post", "/auth/logout", "Auth", "Revoga refresh token", false, object(obj{"refresh_token": str("")}), nil, 204, nil},
	{"post", "/auth/forgot", "Auth", "Solicita redefinição de senha", false, object(obj{"email": str("")}, "email"), object(obj{"message": str("")}), 200, nil},
	{"post", "/auth/reset", "Auth", "Redefine senha com token", false, object(obj{"token": str(""), "password": str("")}, "token", "password"), object(obj{"message": str("")}), 200, nil},
	{"post", "/auth/2fa/setup", "Auth", "Gera segredo TOTP", true, nil, object(obj{"secret": str(""), "otpauth_url": str("")}), 200, nil},
	{"post", "/auth/2fa/verify", "Auth", "Confirma código e habilita 2FA", true, object(obj{"code": str("")}, "code"), object(obj{"totp_enabled": boolean()}), 200, nil},
	{"post", "/auth/2fa/disable", "Auth", "Desabilita 2FA", true, object(obj{"code": str(""), "password": str("")}), object(obj{"totp_enabled": boolean()}), 200, nil},
	{"get", "/me", "Auth", "Usuário logado + org + plano", true, nil, object(obj{"user": ref("User"), "org": ref("Org"), "plan": ref("Plan"), "usage": obj{"type": "object"}}), 200, nil},

	{"get", "/devices", "Dispositivos", "Lista dispositivos visíveis", true, nil, arr(ref("Device")), 200,
		[]obj{q("q", "texto", str("")), q("online", "", boolean()), q("host_enabled", "", boolean()), q("group_id", "", integer()), q("os", "", str("")), q("enrolled", "só vinculados por código", boolean()), q("page", "", integer()), q("per_page", "", integer())}},
	{"get", "/devices/search", "Dispositivos", "Busca natural (IA quando configurada)", true, nil, arr(ref("Device")), 200, []obj{q("q", "pergunta em linguagem natural", str("")), q("page", "", integer()), q("per_page", "", integer())}},
	{"post", "/devices/heartbeat", "Dispositivos", "Heartbeat do cliente (registra/atualiza; devolve host_enabled e convites pendentes)", true, ref("Heartbeat"), ref("HeartbeatOut"), 200, nil},
	{"get", "/devices/{id}", "Dispositivos", "Detalhe", true, nil, ref("Device"), 200, []obj{pathP("id")}},
	{"patch", "/devices/{id}", "Dispositivos", "Renomear / host on-off / grupo", true, object(obj{"name": str(""), "alias": str("sinônimo de name"), "host_enabled": boolean(), "group_id": integer()}), ref("Device"), 200, []obj{pathP("id")}},
	{"post", "/devices/{id}/rotate-password", "Enrollment", "Gera nova senha permanente (PC aplica no próximo heartbeat)", true, nil, object(obj{"password_version": integer()}), 200, []obj{pathP("id")}},
	{"post", "/devices/{id}/revoke", "Enrollment", "Revoga o vínculo (agente recebe 410; senha apagada)", true, nil, nil, 204, []obj{pathP("id")}},
	{"get", "/devices/{id}/credentials", "Enrollment", "Senha permanente para conectar (perm connect; auditado)", true, nil, ref("DeviceCredentials"), 200, []obj{pathP("id")}},

	{"get", "/enrollment-keys", "Enrollment", "Lista códigos de vínculo (owner/admin/tech)", true, nil, arr(ref("EnrollmentKey")), 200, []obj{q("page", "", integer()), q("per_page", "", integer())}},
	{"post", "/enrollment-keys", "Enrollment", "Cria código de vínculo (owner/admin)", true, object(obj{"label": str(""), "group_id": integer(), "max_uses": integer(), "expires_in_days": obj{"type": "integer", "nullable": true}}), ref("EnrollmentKey"), 201, nil},
	{"delete", "/enrollment-keys/{id}", "Enrollment", "Revoga código (dispositivos vinculados continuam)", true, nil, nil, 204, []obj{pathP("id")}},
	{"post", "/enroll", "Enrollment", "Vincula o PC à org (público; 20/min/IP; 404 enrollment_key_invalid, 409 device_other_org, 402 plan_limit_reached)", false, ref("Enroll"), ref("EnrollOut"), 200, nil},
	{"post", "/agent/heartbeat", "Enrollment", "Heartbeat do agente (Authorization: Device <token>; 401 device_token_invalid, 410 device_revoked)", false, ref("AgentHeartbeat"), ref("AgentHeartbeatOut"), 200, nil},
	{"delete", "/devices/{id}", "Dispositivos", "Remove da conta", true, nil, nil, 204, []obj{pathP("id")}},
	{"post", "/devices/{id}/share", "Dispositivos", "Compartilha com usuário/grupo", true, object(obj{"target_type": str("user|group"), "target_id": integer(), "level": str("connect|manage")}, "target_type", "target_id"), ref("DeviceShare"), 201, []obj{pathP("id")}},
	{"delete", "/devices/{id}/share/{shareId}", "Dispositivos", "Remove compartilhamento", true, nil, nil, 204, []obj{pathP("id"), pathP("shareId")}},

	{"post", "/invites", "Convites", "Cria convite por link", true, object(obj{"device_id": integer(), "expires_in": integer(), "max_uses": integer(), "note": str("")}), ref("Invite"), 201, nil},
	{"get", "/invites", "Convites", "Lista convites", true, nil, arr(ref("Invite")), 200, []obj{q("status", "active|expired|consumed|revoked", str("")), q("page", "", integer()), q("per_page", "", integer())}},
	{"delete", "/invites/{code}", "Convites", "Revoga convite (id numérico ou código)", true, nil, nil, 204, []obj{pathP("code")}},
	{"get", "/invites/{code}", "Convites", "Dados públicos do convite", false, nil, ref("PublicInvite"), 200, []obj{pathP("code")}},
	{"post", "/invites/{code}/consume", "Convites", "Consome (uso único) e entrega senha de sessão", false, nil, ref("ConsumeOut"), 200, []obj{pathP("code")}},

	{"get", "/groups", "Grupos", "Lista grupos de dispositivos", true, nil, arr(ref("Group")), 200, nil},
	{"post", "/groups", "Grupos", "Cria grupo", true, object(obj{"name": str("")}, "name"), ref("Group"), 201, nil},
	{"get", "/groups/{id}", "Grupos", "Detalhe", true, nil, ref("Group"), 200, []obj{pathP("id")}},
	{"patch", "/groups/{id}", "Grupos", "Renomeia", true, object(obj{"name": str("")}, "name"), ref("Group"), 200, []obj{pathP("id")}},
	{"delete", "/groups/{id}", "Grupos", "Apaga", true, nil, nil, 204, []obj{pathP("id")}},
	{"post", "/groups/{id}/devices", "Grupos", "Move dispositivos para o grupo", true, object(obj{"device_ids": arr(integer())}, "device_ids"), object(obj{"updated": integer()}), 200, []obj{pathP("id")}},

	{"get", "/sessions", "Sessões", "Lista sessões", true, nil, arr(ref("Session")), 200, []obj{q("device_id", "", integer()), q("page", "", integer()), q("per_page", "", integer())}},
	{"get", "/sessions/{id}", "Sessões", "Detalhe (inclui resumo)", true, nil, ref("Session"), 200, []obj{pathP("id")}},
	{"post", "/sessions/{id}/summary", "Sessões", "Enfileira resumo por IA", true, object(obj{"transcript": str("opcional")}), object(obj{"job_id": integer(), "job_status": str(""), "session": ref("Session")}), 202, []obj{pathP("id")}},
	{"get", "/jobs/{id}", "Sessões", "Estado de um job", true, nil, ref("Job"), 200, []obj{pathP("id")}},

	{"get", "/org", "Organização", "Dados da org", true, nil, ref("Org"), 200, nil},
	{"patch", "/org", "Organização", "Renomeia", true, object(obj{"name": str("")}, "name"), ref("Org"), 200, nil},
	{"get", "/org/users", "Organização", "Lista membros", true, nil, arr(ref("User")), 200, []obj{q("page", "", integer()), q("per_page", "", integer())}},
	{"post", "/org/users", "Organização", "Adiciona membro", true, object(obj{"email": str(""), "name": str(""), "role": str(""), "password": str("opcional")}, "email"), object(obj{"user": ref("User"), "temp_password": str("")}), 201, nil},
	{"patch", "/org/users/{id}", "Organização", "Altera membro", true, object(obj{"name": str(""), "role": str(""), "status": integer()}), ref("User"), 200, []obj{pathP("id")}},
	{"delete", "/org/users/{id}", "Organização", "Remove membro", true, nil, nil, 204, []obj{pathP("id")}},
	{"get", "/org/audit", "Organização", "Auditoria", true, nil, arr(ref("AuditLog")), 200, []obj{q("action", "prefixo", str("")), q("actor_id", "", integer()), q("page", "", integer()), q("per_page", "", integer())}},
	{"get", "/org/branding", "Organização", "White-label", true, nil, ref("Branding"), 200, nil},
	{"patch", "/org/branding", "Organização", "Altera white-label", true, object(obj{"product_name": str(""), "logo_url": str(""), "primary_color": str("")}), ref("Branding"), 200, nil},
	{"get", "/org/sso", "Organização", "Config OIDC", true, nil, ref("Sso"), 200, nil},
	{"patch", "/org/sso", "Organização", "Altera OIDC", true, object(obj{"enabled": boolean(), "issuer": str(""), "client_id": str(""), "client_secret": str(""), "scopes": str("")}), ref("Sso"), 200, nil},
	{"get", "/org/billing", "Billing", "Plano e uso", true, nil, ref("Billing"), 200, nil},
	{"post", "/org/billing/portal", "Billing", "Sessão do portal Stripe", true, object(obj{"return_url": str("")}), object(obj{"url": str("")}), 200, nil},
	{"post", "/webhooks/stripe", "Billing", "Webhook Stripe (assinado)", false, obj{"type": "object"}, object(obj{"status": str("")}), 200, nil},

	{"get", "/ops/orgs", "Ops", "Todas as orgs (super-admin)", true, nil, arr(obj{"type": "object"}), 200, []obj{q("page", "", integer()), q("per_page", "", integer())}},
	{"get", "/ops/health", "Ops", "Saúde detalhada (super-admin)", true, nil, obj{"type": "object"}, 200, nil},
}

var (
	once   sync.Once
	cached []byte
)

func build(version, publicURL string) obj {
	paths := obj{}
	for _, r := range routes {
		op := obj{"tags": []string{r.tag}, "summary": r.summary, "operationId": r.method + strings.NewReplacer("/", "_", "{", "", "}", "").Replace(r.path)}
		if r.auth {
			op["security"] = []obj{{"bearerAuth": []string{}}}
		}
		if strings.HasPrefix(r.path, "/agent/") {
			op["security"] = []obj{{"deviceToken": []string{}}}
		}
		if len(r.params) > 0 {
			op["parameters"] = r.params
		}
		if r.body != nil {
			op["requestBody"] = obj{"required": true, "content": obj{"application/json": obj{"schema": r.body}}}
		}
		responses := obj{}
		if r.status == 204 {
			responses["204"] = obj{"description": "sem conteúdo"}
		} else {
			data := r.resp
			if data == nil {
				data = obj{"type": "object"}
			}
			props := obj{"data": data}
			if _, isArr := data["items"]; isArr {
				props["meta"] = ref("Meta")
			}
			responses[itoa(r.status)] = obj{"description": "ok", "content": obj{"application/json": obj{"schema": object(props)}}}
		}
		responses["default"] = obj{"description": "erro", "content": obj{"application/json": obj{"schema": ref("Error")}}}
		op["responses"] = responses
		item, _ := paths[r.path].(obj)
		if item == nil {
			item = obj{}
		}
		item[r.method] = op
		paths[r.path] = item
	}
	return obj{
		"openapi": "3.0.3",
		"info": obj{"title": "george-api", "version": version,
			"description": "API do George (acesso remoto sobre RustDesk). Erros: {error:{code,message,details}}. Listas: {data, meta:{total,page,per_page}}."},
		"servers": []obj{{"url": publicURL + "/api/v1"}},
		"components": obj{"schemas": schemas, "securitySchemes": obj{"bearerAuth": obj{"type": "http", "scheme": "bearer", "bearerFormat": "JWT"},
			"deviceToken": obj{"type": "apiKey", "in": "header", "name": "Authorization", "description": "Device <token> (entregue no POST /enroll)"}}},
		"paths": paths,
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

// JSON devolve o documento serializado (cacheado).
func JSON(version, publicURL string) []byte {
	once.Do(func() {
		b, err := json.Marshal(build(version, publicURL))
		if err != nil {
			b = []byte(`{"openapi":"3.0.3","info":{"title":"george-api","version":"` + version + `"},"paths":{}}`)
		}
		cached = b
	})
	return cached
}

// Doc devolve o documento como mapa (para testes).
func Doc(version, publicURL string) map[string]interface{} { return build(version, publicURL) }
