package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/global"
	"github.com/lejianwen/rustdesk-api/v2/george/service"
)

func hInviteCreate(c *gin.Context) {
	var r struct {
		DeviceID  uint   `json:"device_id"`
		ExpiresIn int    `json:"expires_in"`
		MaxUses   int    `json:"max_uses"`
		Note      string `json:"note"`
	}
	if !bind(c, &r) {
		return
	}
	inv, err := service.S.InviteCreate(ctx(c), service.InviteInput{DeviceID: r.DeviceID, ExpiresIn: r.ExpiresIn, MaxUses: r.MaxUses, Note: r.Note})
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusCreated, inv)
}

func hInvites(c *gin.Context) {
	rows, meta, err := service.S.Invites(ctx(c), c.Query("status"), page(c))
	if err != nil {
		fail(c, err)
		return
	}
	list(c, rows, meta)
}

func hInviteRevoke(c *gin.Context) {
	if err := service.S.InviteRevoke(ctx(c), c.Param("code")); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func hInvitePublic(c *gin.Context) {
	inv, err := service.S.InvitePublic(c.Param("code"))
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, inv)
}

func hInviteConsume(c *gin.Context) {
	rd := global.Config.Rustdesk
	out, err := service.S.InviteConsume(ctx(c), c.Param("code"), [3]string{rd.IdServer, rd.RelayServer, rd.Key})
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, out)
}

func hSessions(c *gin.Context) {
	var dev uint
	if d := queryUint(c, "device_id"); d != nil {
		dev = *d
	}
	rows, meta, err := service.S.Sessions(ctx(c), dev, page(c))
	if err != nil {
		fail(c, err)
		return
	}
	list(c, rows, meta)
}

func hSession(c *gin.Context) {
	id, okID := paramID(c, "id")
	if !okID {
		return
	}
	s, err := service.S.Session(ctx(c), id)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, s)
}

func hSessionSummary(c *gin.Context) {
	id, okID := paramID(c, "id")
	if !okID {
		return
	}
	var r struct {
		Transcript string `json:"transcript"`
	}
	_ = c.ShouldBindJSON(&r)
	job, sess, err := service.S.SessionSummaryRequest(ctx(c), id, r.Transcript)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"data": gin.H{"job_id": job.ID, "job_status": job.Status, "session": sess}})
}

func hJob(c *gin.Context) {
	id, okID := paramID(c, "id")
	if !okID {
		return
	}
	j, err := service.S.Job(ctx(c), id)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, j)
}
