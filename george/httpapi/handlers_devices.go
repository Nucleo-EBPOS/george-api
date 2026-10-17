package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/george/service"
)

func hDevices(c *gin.Context) {
	f := service.DeviceFilter{Q: c.Query("q"), Online: queryBool(c, "online"), HostEnabled: queryBool(c, "host_enabled"), GroupID: queryUint(c, "group_id"), Os: c.Query("os")}
	rows, meta, err := service.S.Devices(ctx(c), f, page(c))
	if err != nil {
		fail(c, err)
		return
	}
	list(c, rows, meta)
}

func hDeviceSearch(c *gin.Context) {
	q := c.Query("q")
	if q == "" {
		fail(c, service.ErrValidation("informe q", map[string]string{"param": "q"}))
		return
	}
	rows, meta, f, provider, err := service.S.DeviceSearch(ctx(c), q, page(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rows, "meta": meta, "filter": f, "provider": provider})
}

func hDevice(c *gin.Context) {
	id, okID := paramID(c, "id")
	if !okID {
		return
	}
	d, err := service.S.Device(ctx(c), id)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, d)
}

func hDevicePatch(c *gin.Context) {
	id, okID := paramID(c, "id")
	if !okID {
		return
	}
	var r struct {
		Name        *string `json:"name"`
		HostEnabled *bool   `json:"host_enabled"`
		GroupID     *uint   `json:"group_id"`
	}
	if !bind(c, &r) {
		return
	}
	d, err := service.S.DeviceUpdate(ctx(c), id, service.DeviceUpdateInput{Name: r.Name, HostEnabled: r.HostEnabled, GroupID: r.GroupID})
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, d)
}

func hDeviceDelete(c *gin.Context) {
	id, okID := paramID(c, "id")
	if !okID {
		return
	}
	if err := service.S.DeviceDelete(ctx(c), id); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func hDeviceShare(c *gin.Context) {
	id, okID := paramID(c, "id")
	if !okID {
		return
	}
	var r struct {
		TargetType string `json:"target_type"`
		TargetID   uint   `json:"target_id"`
		Level      string `json:"level"`
	}
	if !bind(c, &r) {
		return
	}
	sh, err := service.S.DeviceShare(ctx(c), id, service.ShareInput{TargetType: r.TargetType, TargetID: r.TargetID, Level: r.Level})
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusCreated, sh)
}

func hDeviceUnshare(c *gin.Context) {
	id, okID := paramID(c, "id")
	if !okID {
		return
	}
	sid, okS := paramID(c, "shareId")
	if !okS {
		return
	}
	if err := service.S.DeviceUnshare(ctx(c), id, sid); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func hHeartbeat(c *gin.Context) {
	var r service.HeartbeatInput
	if !bind(c, &r) {
		return
	}
	out, err := service.S.Heartbeat(ctx(c), r)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, out)
}

// grupos de dispositivos

func hGroups(c *gin.Context) {
	sc := ctx(c)
	rows, meta, err := service.S.DeviceGroups(sc, sc.User.OrgID, page(c))
	if err != nil {
		fail(c, err)
		return
	}
	list(c, rows, meta)
}

func hGroupCreate(c *gin.Context) {
	var r struct {
		Name string `json:"name"`
	}
	if !bind(c, &r) {
		return
	}
	sc := ctx(c)
	g, err := service.S.DeviceGroupCreate(sc, sc.User.OrgID, r.Name)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusCreated, g)
}

func hGroup(c *gin.Context) {
	id, okID := paramID(c, "id")
	if !okID {
		return
	}
	sc := ctx(c)
	g, err := service.S.DeviceGroupGet(sc, sc.User.OrgID, id)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, g)
}

func hGroupPatch(c *gin.Context) {
	id, okID := paramID(c, "id")
	if !okID {
		return
	}
	var r struct {
		Name string `json:"name"`
	}
	if !bind(c, &r) {
		return
	}
	sc := ctx(c)
	g, err := service.S.DeviceGroupUpdate(sc, sc.User.OrgID, id, r.Name)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, g)
}

func hGroupDelete(c *gin.Context) {
	id, okID := paramID(c, "id")
	if !okID {
		return
	}
	sc := ctx(c)
	if err := service.S.DeviceGroupDelete(sc, sc.User.OrgID, id); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func hGroupAddDevices(c *gin.Context) {
	id, okID := paramID(c, "id")
	if !okID {
		return
	}
	var r struct {
		DeviceIDs []uint `json:"device_ids"`
	}
	if !bind(c, &r) {
		return
	}
	sc := ctx(c)
	n, err := service.S.DeviceGroupAddDevices(sc, sc.User.OrgID, id, r.DeviceIDs)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, http.StatusOK, gin.H{"updated": n})
}
