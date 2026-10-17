package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/george/service"
)

type signupReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
	OrgName  string `json:"org_name"`
}

func hSignup(c *gin.Context) {
	var r signupReq
	if !bind(c, &r) {
		return
	}
	sc := ctx(c)
	u, org, err := service.S.Signup(sc, service.SignupInput{Email: r.Email, Password: r.Password, Name: r.Name, OrgName: r.OrgName})
	if err != nil {
		fail(c, err)
		return
	}
	sc.User = u
	t, _, err := service.S.Login(sc, u.Email, r.Password, "")
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": gin.H{"tokens": t, "user": u, "org": org}})
}

type loginReq struct {
	Email    string `json:"email"`
	Username string `json:"username"`
	Password string `json:"password"`
	TotpCode string `json:"totp_code"`
}

func hLogin(c *gin.Context) {
	var r loginReq
	if !bind(c, &r) {
		return
	}
	if r.Email == "" {
		r.Email = r.Username
	}
	t, u, err := service.S.Login(ctx(c), r.Email, r.Password, r.TotpCode)
	if err != nil {
		fail(c, err)
		return
	}
	org, _ := service.S.OrgByID(u.OrgID)
	ok(c, http.StatusOK, gin.H{"tokens": t, "user": u, "org": org})
}

type refreshReq struct {
	RefreshToken string `json:"refresh_token"`
}

func hRefresh(c *gin.Context) {
	var r refreshReq
	if !bind(c, &r) {
		return
	}
	t, u, err := service.S.Refresh(ctx(c), r.RefreshToken)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, gin.H{"tokens": t, "user": u})
}

func hLogout(c *gin.Context) {
	var r refreshReq
	_ = c.ShouldBindJSON(&r)
	service.S.Logout(ctx(c), r.RefreshToken)
	c.Status(http.StatusNoContent)
}

func hForgot(c *gin.Context) {
	var r struct {
		Email string `json:"email"`
	}
	if !bind(c, &r) {
		return
	}
	if err := service.S.Forgot(ctx(c), r.Email); err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, gin.H{"message": "se o e-mail existir, enviaremos instruções"})
}

func hReset(c *gin.Context) {
	var r struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !bind(c, &r) {
		return
	}
	if err := service.S.Reset(ctx(c), r.Token, r.Password); err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, gin.H{"message": "senha redefinida"})
}

func hTotpSetup(c *gin.Context) {
	secret, url, err := service.S.TotpSetup(ctx(c))
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, gin.H{"secret": secret, "otpauth_url": url})
}

func hTotpVerify(c *gin.Context) {
	var r struct {
		Code string `json:"code"`
	}
	if !bind(c, &r) {
		return
	}
	if err := service.S.TotpVerify(ctx(c), r.Code); err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, gin.H{"totp_enabled": true})
}

func hTotpDisable(c *gin.Context) {
	var r struct {
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	if !bind(c, &r) {
		return
	}
	if err := service.S.TotpDisable(ctx(c), r.Code, r.Password); err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, gin.H{"totp_enabled": false})
}

func hMe(c *gin.Context) {
	me, err := service.S.Me(ctx(c))
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, me)
}
