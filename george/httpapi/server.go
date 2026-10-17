package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/george/config"
	"github.com/lejianwen/rustdesk-api/v2/george/openapi"
	"github.com/lejianwen/rustdesk-api/v2/george/service"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

// Version da API George (exposta em /healthz e no OpenAPI).
const Version = "1.0.0"

var registerOnce sync.Once

// Register aplica middlewares globais e monta /api/v1, /healthz e /metrics.
func Register(g *gin.Engine, log *logrus.Logger) {
	cfg := config.Current
	registerOnce.Do(func() {
		prometheus.MustRegister(reqTotal, reqDur)
		prometheus.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "george_jobs_pending", Help: "Jobs pendentes na fila."}, func() float64 {
			if service.S == nil {
				return 0
			}
			return float64(service.S.PendingJobs())
		}))
	})
	g.Use(RequestID(), JSONLog(log), SecurityHeaders(), CORS(cfg.CORSOrigins))

	g.GET("/healthz", hHealthz)
	g.GET("/metrics", MetricsAuth(), gin.WrapH(promhttp.Handler()))

	v1 := g.Group("/api/v1")
	v1.GET("/healthz", hHealthz)
	v1.GET("/openapi.json", func(c *gin.Context) { c.Data(http.StatusOK, "application/json", openapi.JSON(Version, cfg.PublicURL)) })
	v1.GET("/releases/latest", RateLimit(60, time.Minute), hReleasesLatest)
	v1.POST("/webhooks/stripe", RateLimit(120, time.Minute), hStripeWebhook)

	a := v1.Group("/auth")
	a.POST("/signup", RateLimit(5, time.Minute), hSignup)
	a.POST("/login", RateLimit(10, time.Minute), hLogin)
	a.POST("/refresh", RateLimit(30, time.Minute), hRefresh)
	a.POST("/logout", hLogout)
	a.POST("/forgot", RateLimit(5, time.Minute), hForgot)
	a.POST("/reset", RateLimit(5, time.Minute), hReset)
	a.POST("/2fa/setup", Auth(), hTotpSetup)
	a.POST("/2fa/verify", Auth(), RateLimit(10, time.Minute), hTotpVerify)
	a.POST("/2fa/disable", Auth(), RateLimit(10, time.Minute), hTotpDisable)

	// convidado (público)
	v1.GET("/invites/:code", RateLimit(60, time.Minute), hInvitePublic)
	v1.POST("/invites/:code/consume", RateLimit(10, time.Minute), hInviteConsume)

	p := v1.Group("", Auth())
	p.GET("/me", hMe)

	p.GET("/devices", hDevices)
	p.GET("/devices/search", hDeviceSearch)
	p.POST("/devices/heartbeat", hHeartbeat)
	p.GET("/devices/:id", hDevice)
	p.PATCH("/devices/:id", hDevicePatch)
	p.DELETE("/devices/:id", hDeviceDelete)
	p.POST("/devices/:id/share", hDeviceShare)
	p.DELETE("/devices/:id/share/:shareId", hDeviceUnshare)

	p.POST("/invites", RateLimit(30, time.Minute), hInviteCreate)
	p.GET("/invites", hInvites)
	p.DELETE("/invites/:code", hInviteRevoke)

	p.GET("/groups", hGroups)
	p.POST("/groups", hGroupCreate)
	p.GET("/groups/:id", hGroup)
	p.PATCH("/groups/:id", hGroupPatch)
	p.DELETE("/groups/:id", hGroupDelete)
	p.POST("/groups/:id/devices", hGroupAddDevices)

	p.GET("/sessions", hSessions)
	p.GET("/sessions/:id", hSession)
	p.POST("/sessions/:id/summary", RateLimit(30, time.Minute), hSessionSummary)
	p.GET("/jobs/:id", hJob)

	o := p.Group("/org")
	o.GET("", hOrg)
	o.PATCH("", hOrgPatch)
	o.GET("/users", hOrgUsers)
	o.POST("/users", hOrgUserCreate)
	o.PATCH("/users/:id", hOrgUserPatch)
	o.DELETE("/users/:id", hOrgUserDelete)
	o.GET("/audit", hOrgAudit)
	o.GET("/branding", hBranding)
	o.PATCH("/branding", hBrandingPatch)
	o.GET("/sso", hSso)
	o.PATCH("/sso", hSsoPatch)
	o.GET("/billing", hBilling)
	o.POST("/billing/portal", hBillingPortal)

	ops := p.Group("/ops")
	ops.GET("/orgs", hOpsOrgs)
	ops.GET("/health", hOpsHealth)
}

var startedAt = time.Now()

func hHealthz(c *gin.Context) {
	status := http.StatusOK
	db := "ok"
	if service.S == nil {
		db, status = "uninitialized", http.StatusServiceUnavailable
	} else if sqlDB, err := service.S.DB.DB(); err != nil {
		db, status = err.Error(), http.StatusServiceUnavailable
	} else if err := sqlDB.Ping(); err != nil {
		db, status = err.Error(), http.StatusServiceUnavailable
	}
	c.JSON(status, gin.H{"status": map[bool]string{true: "ok", false: "degraded"}[status == http.StatusOK],
		"version": Version, "db": db, "uptime_s": int(time.Since(startedAt).Seconds())})
}

func hOpsHealth(c *gin.Context) {
	sc := ctx(c)
	if !sc.User.IsOps {
		fail(c, service.ErrForbidden())
		return
	}
	var orgs, users, devices, sessions int64
	db := service.S.DB
	db.Table("orgs").Count(&orgs)
	db.Table("users").Count(&users)
	db.Table("peers").Count(&devices)
	db.Table("sessions").Count(&sessions)
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	ok(c, http.StatusOK, gin.H{
		"version": Version, "uptime_s": int(time.Since(startedAt).Seconds()), "go": runtime.Version(),
		"goroutines": runtime.NumGoroutine(), "heap_mb": m.HeapAlloc / 1024 / 1024,
		"counts": gin.H{"orgs": orgs, "users": users, "devices": devices, "sessions": sessions, "jobs_pending": service.S.PendingJobs()},
		"ai_enabled": service.S.AI.Enabled(), "stripe_configured": config.Current.StripeSecretKey != "",
	})
}

// releases: manifest do último GitHub Release do cliente (cache de 10 min).
type releaseAsset struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Size     int64  `json:"size"`
	Platform string `json:"platform"`
}

type releaseManifest struct {
	Version     string         `json:"version"`
	Tag         string         `json:"tag"`
	PublishedAt string         `json:"published_at"`
	Notes       string         `json:"notes"`
	URL         string         `json:"url"`
	Assets      []releaseAsset `json:"assets"`
}

var relCache struct {
	sync.Mutex
	at   time.Time
	data *releaseManifest
}

func platformOf(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.HasSuffix(n, ".exe") || strings.HasSuffix(n, ".msi") || strings.Contains(n, "windows") || strings.Contains(n, "win"):
		return "windows"
	case strings.HasSuffix(n, ".dmg") || strings.Contains(n, "macos") || strings.Contains(n, "mac"):
		return "macos"
	case strings.HasSuffix(n, ".deb") || strings.HasSuffix(n, ".appimage") || strings.HasSuffix(n, ".flatpak") || strings.Contains(n, "linux"):
		return "linux"
	case strings.HasSuffix(n, ".apk") || strings.Contains(n, "android"):
		return "android"
	case strings.Contains(n, "web"):
		return "web"
	}
	return "other"
}

func fetchLatestRelease(repo string) (*releaseManifest, error) {
	req, _ := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+repo+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "george-api/"+Version)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, &service.Error{Status: http.StatusBadGateway, Code: "releases_unavailable", Message: "GitHub respondeu " + resp.Status}
	}
	var gh struct {
		TagName     string `json:"tag_name"`
		Name        string `json:"name"`
		Body        string `json:"body"`
		PublishedAt string `json:"published_at"`
		HTMLURL     string `json:"html_url"`
		Assets      []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &gh); err != nil {
		return nil, err
	}
	m := &releaseManifest{Version: strings.TrimPrefix(gh.TagName, "v"), Tag: gh.TagName, PublishedAt: gh.PublishedAt, Notes: gh.Body, URL: gh.HTMLURL, Assets: []releaseAsset{}}
	for _, a := range gh.Assets {
		m.Assets = append(m.Assets, releaseAsset{Name: a.Name, URL: a.URL, Size: a.Size, Platform: platformOf(a.Name)})
	}
	return m, nil
}

func hReleasesLatest(c *gin.Context) {
	relCache.Lock()
	defer relCache.Unlock()
	if relCache.data == nil || time.Since(relCache.at) > 10*time.Minute {
		m, err := fetchLatestRelease(config.Current.ReleasesRepo)
		if err != nil {
			if relCache.data == nil {
				fail(c, err)
				return
			}
		} else {
			relCache.data, relCache.at = m, time.Now()
		}
	}
	c.Header("Cache-Control", "public, max-age=300")
	ok(c, http.StatusOK, relCache.data)
}
