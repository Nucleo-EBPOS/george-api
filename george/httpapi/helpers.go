package httpapi

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/george/service"
)

// fail responde {error:{code,message,details}} com o status do erro.
func fail(c *gin.Context, err error) {
	e := service.AsError(err)
	if e.Status >= 500 {
		_ = c.Error(err)
	}
	c.JSON(e.Status, gin.H{"error": e})
}

// ok responde {data: ...}.
func ok(c *gin.Context, status int, data interface{}) {
	c.JSON(status, gin.H{"data": data})
}

// list responde {data, meta}.
func list(c *gin.Context, data interface{}, meta service.Meta) {
	c.JSON(http.StatusOK, gin.H{"data": data, "meta": meta})
}

// bind faz o bind JSON e devolve false (já respondendo) em caso de erro.
func bind(c *gin.Context, v interface{}) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		fail(c, service.ErrValidation("corpo JSON inválido", err.Error()))
		return false
	}
	return true
}

func page(c *gin.Context) service.Page {
	p, _ := strconv.Atoi(c.Query("page"))
	pp, _ := strconv.Atoi(c.Query("per_page"))
	return service.Page{Page: p, PerPage: pp}
}

func paramID(c *gin.Context, name string) (uint, bool) {
	v, err := strconv.ParseUint(c.Param(name), 10, 64)
	if err != nil || v == 0 {
		fail(c, service.ErrValidation("id inválido", map[string]string{"param": name}))
		return 0, false
	}
	return uint(v), true
}

func queryBool(c *gin.Context, name string) *bool {
	v := c.Query(name)
	if v == "" {
		return nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return nil
	}
	return &b
}

func queryUint(c *gin.Context, name string) *uint {
	v := c.Query(name)
	if v == "" {
		return nil
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return nil
	}
	u := uint(n)
	return &u
}
