package httpapi

import (
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	gm "github.com/lejianwen/rustdesk-api/v2/george/model"
	"github.com/lejianwen/rustdesk-api/v2/george/service"
)

func hOrg(c *gin.Context) {
	sc := ctx(c)
	org, err := service.S.OrgByID(sc.User.OrgID)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, org)
}

func hOrgPatch(c *gin.Context) {
	var r struct {
		Name string `json:"name"`
	}
	if !bind(c, &r) {
		return
	}
	sc := ctx(c)
	org, err := service.S.OrgUpdate(sc, sc.User.OrgID, r.Name)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, org)
}

func hOrgUsers(c *gin.Context) {
	sc := ctx(c)
	rows, meta, err := service.S.OrgUsers(sc, sc.User.OrgID, page(c))
	if err != nil {
		fail(c, err)
		return
	}
	list(c, rows, meta)
}

func hOrgUserCreate(c *gin.Context) {
	var r struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Role     string `json:"role"`
		Password string `json:"password"`
	}
	if !bind(c, &r) {
		return
	}
	sc := ctx(c)
	u, temp, err := service.S.OrgUserCreate(sc, sc.User.OrgID, service.CreateUserInput{Email: r.Email, Name: r.Name, Role: r.Role, Password: r.Password})
	if err != nil {
		fail(c, err)
		return
	}
	out := gin.H{"user": u}
	if temp != "" {
		out["temp_password"] = temp
	}
	ok(c, http.StatusCreated, out)
}

func hOrgUserPatch(c *gin.Context) {
	id, okID := paramID(c, "id")
	if !okID {
		return
	}
	var r struct {
		Name   *string `json:"name"`
		Role   *string `json:"role"`
		Status *int    `json:"status"`
	}
	if !bind(c, &r) {
		return
	}
	sc := ctx(c)
	u, err := service.S.OrgUserUpdate(sc, sc.User.OrgID, id, service.UpdateUserInput{Name: r.Name, Role: r.Role, Status: r.Status})
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, u)
}

func hOrgUserDelete(c *gin.Context) {
	id, okID := paramID(c, "id")
	if !okID {
		return
	}
	sc := ctx(c)
	if err := service.S.OrgUserDelete(sc, sc.User.OrgID, id); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func hOrgAudit(c *gin.Context) {
	sc := ctx(c)
	actor, _ := strconv.ParseUint(c.Query("actor_id"), 10, 64)
	rows, meta, err := service.S.AuditList(sc, sc.User.OrgID, c.Query("action"), uint(actor), page(c))
	if err != nil {
		fail(c, err)
		return
	}
	list(c, rows, meta)
}

func hBranding(c *gin.Context) {
	sc := ctx(c)
	b, err := service.S.Branding(sc.User.OrgID)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, b)
}

func hBrandingPatch(c *gin.Context) {
	var r struct {
		ProductName  *string `json:"product_name"`
		LogoURL      *string `json:"logo_url"`
		PrimaryColor *string `json:"primary_color"`
	}
	if !bind(c, &r) {
		return
	}
	sc := ctx(c)
	cur, err := service.S.Branding(sc.User.OrgID)
	if err != nil {
		fail(c, err)
		return
	}
	in := gm.OrgBranding{ProductName: cur.ProductName, LogoURL: cur.LogoURL, PrimaryColor: cur.PrimaryColor}
	if r.ProductName != nil {
		in.ProductName = *r.ProductName
	}
	if r.LogoURL != nil {
		in.LogoURL = *r.LogoURL
	}
	if r.PrimaryColor != nil {
		in.PrimaryColor = *r.PrimaryColor
	}
	b, err := service.S.BrandingUpdate(sc, sc.User.OrgID, in)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, b)
}

func hSso(c *gin.Context) {
	sc := ctx(c)
	v, err := service.S.Sso(sc, sc.User.OrgID)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, v)
}

func hSsoPatch(c *gin.Context) {
	var r struct {
		Enabled      *bool   `json:"enabled"`
		Issuer       *string `json:"issuer"`
		ClientID     *string `json:"client_id"`
		ClientSecret *string `json:"client_secret"`
		Scopes       *string `json:"scopes"`
	}
	if !bind(c, &r) {
		return
	}
	sc := ctx(c)
	v, err := service.S.SsoUpdate(sc, sc.User.OrgID, service.SsoInput{Enabled: r.Enabled, Issuer: r.Issuer, ClientID: r.ClientID, ClientSecret: r.ClientSecret, Scopes: r.Scopes})
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, v)
}

func hBilling(c *gin.Context) {
	sc := ctx(c)
	b, err := service.S.Billing(sc, sc.User.OrgID)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, b)
}

func hBillingPortal(c *gin.Context) {
	var r struct {
		ReturnURL string `json:"return_url"`
	}
	_ = c.ShouldBindJSON(&r)
	sc := ctx(c)
	url, err := service.S.BillingPortal(sc, sc.User.OrgID, r.ReturnURL)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, gin.H{"url": url})
}

func hStripeWebhook(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		fail(c, service.ErrValidation("corpo inválido", nil))
		return
	}
	status, err := service.S.StripeWebhook(body, c.GetHeader("Stripe-Signature"))
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, gin.H{"status": status})
}

func hOpsOrgs(c *gin.Context) {
	rows, meta, err := service.S.OpsOrgs(ctx(c), page(c))
	if err != nil {
		fail(c, err)
		return
	}
	list(c, rows, meta)
}
