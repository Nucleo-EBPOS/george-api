// Package httpapi expõe as rotas /api/v1 do George sobre o gin do upstream.
package httpapi

import (
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lejianwen/rustdesk-api/v2/george/config"
	"github.com/lejianwen/rustdesk-api/v2/george/ratelimit"
	"github.com/lejianwen/rustdesk-api/v2/george/service"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

const ctxKey = "george.ctx"

var (
	reqTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total", Help: "Requisições HTTP por método, rota e status."},
		[]string{"method", "route", "status"})
	reqDur = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "http_request_duration_seconds", Help: "Latência das requisições.", Buckets: prometheus.DefBuckets},
		[]string{"method", "route"})
)

// RequestID garante X-Request-Id em toda requisição.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-Id")
		if id == "" || len(id) > 64 {
			id = uuid.NewString()
		}
		c.Set("request_id", id)
		c.Header("X-Request-Id", id)
		c.Next()
	}
}

// JSONLog registra cada requisição em JSON com request_id.
func JSONLog(log *logrus.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		status := c.Writer.Status()
		reqTotal.WithLabelValues(c.Request.Method, route, strconv.Itoa(status)).Inc()
		reqDur.WithLabelValues(c.Request.Method, route).Observe(time.Since(start).Seconds())
		f := logrus.Fields{
			"request_id": c.GetString("request_id"), "method": c.Request.Method, "path": c.Request.URL.Path,
			"route": route, "status": status, "latency_ms": time.Since(start).Milliseconds(), "ip": c.ClientIP(),
		}
		if gc, ok := c.Get(ctxKey); ok {
			if sc, ok := gc.(service.Ctx); ok && sc.User != nil {
				f["user_id"] = sc.User.ID
				f["org_id"] = sc.User.OrgID
			}
		}
		if len(c.Errors) > 0 {
			f["errors"] = c.Errors.String()
		}
		e := log.WithFields(f)
		switch {
		case status >= 500:
			e.Error("request")
		case status >= 400:
			e.Warn("request")
		default:
			e.Info("request")
		}
	}
}

// CORS restringe às origens configuradas (GEORGE_CORS_ORIGINS).
func CORS(origins []string) gin.HandlerFunc {
	allowed := map[string]bool{}
	for _, o := range origins {
		allowed[strings.ToLower(strings.TrimRight(o, "/"))] = true
	}
	return func(c *gin.Context) {
		origin := strings.ToLower(c.GetHeader("Origin"))
		if origin != "" && (allowed[origin] || allowed["*"]) {
			c.Header("Access-Control-Allow-Origin", c.GetHeader("Origin"))
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-Id, Accept-Language")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			c.Header("Access-Control-Expose-Headers", "X-Request-Id")
			c.Header("Access-Control-Max-Age", "600")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// SecurityHeaders aplica cabeçalhos seguros padrão.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		h.Set("Cache-Control", "no-store")
		c.Next()
	}
}

// RateLimit limita por IP para a rota.
func RateLimit(limit int, window time.Duration) gin.HandlerFunc {
	l := ratelimit.New(limit, window)
	return func(c *gin.Context) {
		if !l.Allow(c.ClientIP()) {
			c.Header("Retry-After", strconv.Itoa(int(window.Seconds())))
			fail(c, &service.Error{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "muitas tentativas; aguarde"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func bearer(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// Auth exige JWT do George; aceita também o access_token do cliente RustDesk (/api/login).
func Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := bearer(c)
		if token == "" {
			fail(c, service.ErrUnauthorized("unauthorized", "token ausente"))
			c.Abort()
			return
		}
		s := service.S
		var u *service.User
		if claims, err := s.Signer.Parse(token); err == nil {
			u, _ = s.UserByID(claims.UserID)
		} else if uu, err2 := s.UserFromUpstreamToken(token); err2 == nil {
			u = uu
		}
		if u == nil || !u.Enabled() {
			fail(c, service.ErrUnauthorized("unauthorized", "token inválido ou expirado"))
			c.Abort()
			return
		}
		c.Set(ctxKey, service.Ctx{User: u, IP: c.ClientIP(), UserAgent: c.GetHeader("User-Agent"), RequestID: c.GetString("request_id")})
		c.Next()
	}
}

// MetricsAuth protege /metrics com GEORGE_METRICS_TOKEN quando definido.
func MetricsAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		tok := config.Current.MetricsToken
		if tok == "" {
			c.Next()
			return
		}
		if subtle.ConstantTimeCompare([]byte(bearer(c)), []byte(tok)) != 1 {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	}
}

// ctx devolve o contexto de serviço (anônimo quando não autenticado).
func ctx(c *gin.Context) service.Ctx {
	if v, ok := c.Get(ctxKey); ok {
		if sc, ok := v.(service.Ctx); ok {
			return sc
		}
	}
	return service.Ctx{IP: c.ClientIP(), UserAgent: c.GetHeader("User-Agent"), RequestID: c.GetString("request_id")}
}
