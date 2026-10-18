package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	gm "github.com/lejianwen/rustdesk-api/v2/george/model"
	"github.com/lejianwen/rustdesk-api/v2/george/service"
)

const deviceCtxKey = "george.device"

// deviceToken extrai "Authorization: Device <token>".
func deviceToken(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "device ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// DeviceAuth autentica o agente do PC vinculado. Só aceita o esquema "Device";
// um JWT de usuário (Bearer) nunca passa aqui, e o token de dispositivo nunca
// passa em Auth() (que só lê "Bearer").
func DeviceAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		d, err := service.S.DeviceByToken(deviceToken(c))
		if err != nil {
			fail(c, err)
			c.Abort()
			return
		}
		c.Set(deviceCtxKey, d)
		c.Next()
	}
}

func hEnrollKeys(c *gin.Context) {
	rows, meta, err := service.S.EnrollmentKeys(ctx(c), page(c))
	if err != nil {
		fail(c, err)
		return
	}
	list(c, rows, meta)
}

func hEnrollKeyCreate(c *gin.Context) {
	var r struct {
		Label         string `json:"label"`
		GroupID       uint   `json:"group_id"`
		MaxUses       int    `json:"max_uses"`
		ExpiresInDays *int   `json:"expires_in_days"`
	}
	if !bind(c, &r) {
		return
	}
	k, err := service.S.EnrollmentKeyCreate(ctx(c), service.EnrollmentKeyInput{Label: r.Label, GroupID: r.GroupID, MaxUses: r.MaxUses, ExpiresInDays: r.ExpiresInDays})
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusCreated, k)
}

func hEnrollKeyRevoke(c *gin.Context) {
	id, okID := paramID(c, "id")
	if !okID {
		return
	}
	if err := service.S.EnrollmentKeyRevoke(ctx(c), id); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func hEnroll(c *gin.Context) {
	var in service.EnrollInput
	if !bind(c, &in) {
		return
	}
	out, err := service.S.Enroll(ctx(c), in)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, out)
}

func hAgentHeartbeat(c *gin.Context) {
	v, _ := c.Get(deviceCtxKey)
	d, _ := v.(*gm.Device)
	if d == nil {
		fail(c, service.ErrDeviceTokenInvalid())
		return
	}
	var in service.AgentHeartbeatInput
	if !bind(c, &in) {
		return
	}
	out, err := service.S.AgentHeartbeat(ctx(c), d, in)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, out)
}

func hDeviceRotatePassword(c *gin.Context) {
	id, okID := paramID(c, "id")
	if !okID {
		return
	}
	v, err := service.S.DeviceRotatePassword(ctx(c), id)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, gin.H{"password_version": v})
}

func hDeviceRevoke(c *gin.Context) {
	id, okID := paramID(c, "id")
	if !okID {
		return
	}
	if err := service.S.DeviceRevoke(ctx(c), id); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func hDeviceCredentials(c *gin.Context) {
	id, okID := paramID(c, "id")
	if !okID {
		return
	}
	cr, err := service.S.DeviceCredentialsGet(ctx(c), id)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, cr)
}
