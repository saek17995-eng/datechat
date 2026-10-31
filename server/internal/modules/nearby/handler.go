package nearby

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/datechat/server/internal/middleware"
	"github.com/datechat/server/internal/pkg/errs"
	"github.com/datechat/server/internal/pkg/resp"
)

// Handler 附近的人与在线接口。
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register 注册路由。
func (h *Handler) Register(v1 *gin.RouterGroup, auth gin.HandlerFunc) {
	g := v1.Group("")
	g.Use(auth)
	{
		g.POST("/users/me/location", h.updateLocation)
		g.GET("/nearby", h.nearby)
		g.GET("/users/me/visits", h.visits)
		g.POST("/users/me/visits/read", h.readVisits)
		g.GET("/users/online", h.online)
	}
}

type locationReq struct {
	Lng float64 `json:"lng" binding:"required"`
	Lat float64 `json:"lat" binding:"required"`
}

func (h *Handler) updateLocation(c *gin.Context) {
	var req locationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, errs.ErrInvalidParam.WithMsg("缺少经纬度"))
		return
	}
	out, err := h.svc.UpdateLocation(c.Request.Context(), middleware.MustUID(c), req.Lng, req.Lat)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, out)
}

func (h *Handler) nearby(c *gin.Context) {
	uid := middleware.MustUID(c)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "0"))
	gender, _ := strconv.Atoi(c.DefaultQuery("gender", "0"))
	ageMin, _ := strconv.Atoi(c.DefaultQuery("age_min", "0"))
	ageMax, _ := strconv.Atoi(c.DefaultQuery("age_max", "0"))

	// 坐标必填：不接受"用资料里的城市代替"。
	// 用城市中心点算距离会给出"她在 12km 外"这种精确到吓人的假象。
	lng, err1 := strconv.ParseFloat(c.Query("lng"), 64)
	lat, err2 := strconv.ParseFloat(c.Query("lat"), 64)
	if err1 != nil || err2 != nil {
		resp.Fail(c, errs.ErrInvalidParam.WithMsg("缺少经纬度"))
		return
	}
	radius, err := strconv.ParseFloat(c.DefaultQuery("radius", "0"), 64)
	if err != nil {
		radius = 0
	}
	list, total, err := h.svc.Nearby(c.Request.Context(), uid, Filter{
		Lng:      lng,
		Lat:      lat,
		RadiusKM: radius,
		Gender:   gender,
		AgeMin:   ageMin,
		AgeMax:   ageMax,
		Page:     page,
		Size:     size,
	})
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"list": list, "total": total, "page": page, "size": size})
}

func (h *Handler) visits(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	list, total, unread, err := h.svc.Visits(c.Request.Context(), middleware.MustUID(c), page, size)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"list": list, "total": total, "unread": unread, "page": page, "size": size})
}

func (h *Handler) readVisits(c *gin.Context) {
	n, err := h.svc.MarkVisitsRead(c.Request.Context(), middleware.MustUID(c))
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"updated": n})
}

// online 批量查在线状态，供列表页轮询刷新，避免逐人请求。
func (h *Handler) online(c *gin.Context) {
	raw := strings.Split(c.Query("ids"), ",")
	ids := make([]int64, 0, len(raw))
	for _, s := range raw {
		if v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil && v > 0 {
			ids = append(ids, v)
		}
	}
	if len(ids) > 200 {
		resp.Fail(c, errs.ErrInvalidParam.WithMsg("一次最多查询 200 个用户"))
		return
	}
	m, err := h.svc.Online(c.Request.Context(), ids)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"online": m})
}
