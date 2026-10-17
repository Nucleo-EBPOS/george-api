// Package config lê a configuração do George a partir de variáveis de ambiente
// (prefixo GEORGE_) e aplica overrides sobre a configuração do upstream
// (rustdesk-api), de modo que a imagem possa ser configurada só por env.
package config

import (
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	upstream "github.com/lejianwen/rustdesk-api/v2/config"
)

// Config é a configuração própria do George.
type Config struct {
	DBURL               string        // GEORGE_DB_URL (postgres://user:pass@host:port/db?sslmode=disable)
	JWTSecret           string        // GEORGE_JWT_SECRET
	EncKey              string        // GEORGE_ENC_KEY (32 bytes; se vazio deriva do JWT secret)
	RustdeskKey         string        // GEORGE_RUSTDESK_KEY (chave pública do hbbs)
	IDServer            string        // GEORGE_ID_SERVER (host:porta)
	RelayServer         string        // GEORGE_RELAY_SERVER (host:porta)
	PublicURL           string        // GEORGE_PUBLIC_URL (https://georgedesk.com) usado em links
	CORSOrigins         []string      // GEORGE_CORS_ORIGINS (lista separada por vírgula)
	StripeSecretKey     string        // GEORGE_STRIPE_SECRET_KEY
	StripeWebhookSecret string        // GEORGE_STRIPE_WEBHOOK_SECRET
	AIKey               string        // GEORGE_AI_KEY (API Anthropic)
	AIModel             string        // GEORGE_AI_MODEL
	OpsEmails           []string      // GEORGE_OPS_EMAILS (super-admins da plataforma)
	LogFormat           string        // GEORGE_LOG_FORMAT (json|text)
	ReleasesRepo        string        // GEORGE_RELEASES_REPO (owner/repo no GitHub)
	AccessTTL           time.Duration // GEORGE_ACCESS_TTL (15m)
	RefreshTTL          time.Duration // GEORGE_REFRESH_TTL (720h)
	InviteTTL           time.Duration // GEORGE_INVITE_TTL (1h)
	AutoMigrate         bool          // GEORGE_AUTO_MIGRATE (true)
	Workers             bool          // GEORGE_WORKERS (true) — liga a fila de jobs
	MetricsToken        string        // GEORGE_METRICS_TOKEN (opcional; protege /metrics)
}

// Current é a configuração carregada em Load().
var Current Config

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := env(key, ""); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := env(key, ""); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func envList(key string, def []string) []string {
	v := env(key, "")
	if v == "" {
		return def
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Load lê as variáveis de ambiente e guarda em Current.
func Load() Config {
	Current = Config{
		DBURL:               env("GEORGE_DB_URL", ""),
		JWTSecret:           env("GEORGE_JWT_SECRET", ""),
		EncKey:              env("GEORGE_ENC_KEY", ""),
		RustdeskKey:         env("GEORGE_RUSTDESK_KEY", ""),
		IDServer:            env("GEORGE_ID_SERVER", ""),
		RelayServer:         env("GEORGE_RELAY_SERVER", ""),
		PublicURL:           strings.TrimRight(env("GEORGE_PUBLIC_URL", "https://georgedesk.com"), "/"),
		CORSOrigins:         envList("GEORGE_CORS_ORIGINS", []string{"https://georgedesk.com"}),
		StripeSecretKey:     env("GEORGE_STRIPE_SECRET_KEY", ""),
		StripeWebhookSecret: env("GEORGE_STRIPE_WEBHOOK_SECRET", ""),
		AIKey:               env("GEORGE_AI_KEY", ""),
		AIModel:             env("GEORGE_AI_MODEL", "claude-opus-5-5"),
		OpsEmails:           envList("GEORGE_OPS_EMAILS", nil),
		LogFormat:           env("GEORGE_LOG_FORMAT", "json"),
		ReleasesRepo:        env("GEORGE_RELEASES_REPO", "Nucleo-EBPOS/george-client"),
		AccessTTL:           envDuration("GEORGE_ACCESS_TTL", 15*time.Minute),
		RefreshTTL:          envDuration("GEORGE_REFRESH_TTL", 30*24*time.Hour),
		InviteTTL:           envDuration("GEORGE_INVITE_TTL", time.Hour),
		AutoMigrate:         envBool("GEORGE_AUTO_MIGRATE", true),
		Workers:             envBool("GEORGE_WORKERS", true),
		MetricsToken:        env("GEORGE_METRICS_TOKEN", ""),
	}
	for i, e := range Current.OpsEmails {
		Current.OpsEmails[i] = strings.ToLower(e)
	}
	return Current
}

// ApplyOverrides projeta as variáveis GEORGE_* sobre a configuração do upstream
// (banco Postgres, chave JWT do cliente RustDesk, servidores ID/relay).
func ApplyOverrides(c *upstream.Config) {
	g := Current
	if g.DBURL != "" {
		if u, err := url.Parse(g.DBURL); err == nil && (u.Scheme == "postgres" || u.Scheme == "postgresql") {
			c.Gorm.Type = upstream.TypePostgresql
			c.Postgresql.Host = u.Hostname()
			c.Postgresql.Port = u.Port()
			if c.Postgresql.Port == "" {
				c.Postgresql.Port = "5432"
			}
			c.Postgresql.User = u.User.Username()
			c.Postgresql.Password, _ = u.User.Password()
			c.Postgresql.Dbname = strings.TrimPrefix(u.Path, "/")
			q := u.Query()
			if ssl := q.Get("sslmode"); ssl != "" {
				c.Postgresql.Sslmode = ssl
			} else if c.Postgresql.Sslmode == "" {
				c.Postgresql.Sslmode = "disable"
			}
			// colunas "timestamp" do upstream dependem do fuso da conexão: UTC salvo se a URL disser outro
			if tz := q.Get("TimeZone"); tz != "" {
				c.Postgresql.TimeZone = tz
			} else {
				c.Postgresql.TimeZone = "UTC"
			}
		}
	}
	if g.JWTSecret != "" && c.Jwt.Key == "" {
		c.Jwt.Key = g.JWTSecret
	}
	if g.RustdeskKey != "" {
		c.Rustdesk.Key = g.RustdeskKey
	}
	if g.IDServer != "" {
		c.Rustdesk.IdServer = g.IDServer
	}
	if g.RelayServer != "" {
		c.Rustdesk.RelayServer = g.RelayServer
	}
	if g.PublicURL != "" && (c.Rustdesk.ApiServer == "" || c.Rustdesk.ApiServer == "http://127.0.0.1:21114") {
		c.Rustdesk.ApiServer = g.PublicURL
	}
}

// IsOpsEmail diz se o e-mail está na lista de super-admins da plataforma.
func IsOpsEmail(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	for _, e := range Current.OpsEmails {
		if e == email {
			return true
		}
	}
	return false
}
