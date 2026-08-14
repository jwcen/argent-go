package transport

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/jwcen/argent-go/internal/auth"
	"github.com/jwcen/argent-go/internal/external"
	"github.com/jwcen/argent-go/internal/infra/sqlite"
)

type ExternalHandler struct {
	dbFn func(userID int64) (*sql.DB, error)
}

func NewExternalHandler(dbFn func(userID int64) (*sql.DB, error)) *ExternalHandler {
	return &ExternalHandler{dbFn: dbFn}
}

func (h *ExternalHandler) svc(c *gin.Context) (*external.Service, error) {
	uid := auth.MustUserID(c.Request.Context())
	if uid == 0 {
		return nil, errors.New("no user in context")
	}
	db, err := h.dbFn(uid)
	if err != nil {
		return nil, err
	}
	return external.NewService(sqlite.NewExternalRepo(db)), nil
}

func (h *ExternalHandler) Register(r gin.IRouter) {
	g := r.Group("/assets")
	g.GET("", h.ListAssets)
	g.POST("", h.CreateAsset)
	g.PUT("/:id", h.UpdateAsset)
	g.DELETE("/:id", h.DeleteAsset)
	g.GET("/:id/actions", h.ListActions)
	g.POST("/:id/add-lot", h.AddLot)
	g.POST("/:id/reduce-lot", h.ReduceLot)
	g.POST("/:id/actions/:action_id/confirm", h.ConfirmAction)
	g.DELETE("/:id/actions/:action_id", h.DeleteAction)

	dca := r.Group("/dca")
	dca.GET("", h.ListDCA)
	dca.POST("", h.CreateDCA)
	dca.PUT("/:id", h.UpdateDCA)
	dca.DELETE("/:id", h.DeleteDCA)
}

func (h *ExternalHandler) ListAssets(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	assets, err := svc.ListAssets(c.Request.Context())
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, assets)
}

func (h *ExternalHandler) CreateAsset(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	var a external.Asset
	if err := c.ShouldBindJSON(&a); err != nil {
		WriteError(c, http.StatusBadRequest, "invalid request")
		return
	}
	if a.AssetType == "" || a.Code == "" || a.Name == "" {
		WriteError(c, http.StatusBadRequest, "asset_type / code / name 为必填")
		return
	}
	id, err := svc.CreateAsset(c.Request.Context(), &a)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusCreated, gin.H{"id": id})
}

func (h *ExternalHandler) UpdateAsset(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var a external.Asset
	if err := c.ShouldBindJSON(&a); err != nil {
		WriteError(c, http.StatusBadRequest, "invalid request")
		return
	}
	a.ID = id
	if err := svc.UpdateAsset(c.Request.Context(), &a); err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, gin.H{"ok": true})
}

func (h *ExternalHandler) DeleteAsset(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := svc.DeleteAsset(c.Request.Context(), id); err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, gin.H{"ok": true})
}

func (h *ExternalHandler) ListActions(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	actions, err := svc.ListActions(c.Request.Context(), id)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, actions)
}

type lotReq struct {
	Amount    float64  `json:"amount"`
	Shares    *float64 `json:"shares"`
	UnitPrice *float64 `json:"unit_price"`
	Fee       float64  `json:"fee"`
	TradeDate string   `json:"trade_date"`
	Status    string   `json:"status"`
	Note      string   `json:"note"`
}

func (h *ExternalHandler) AddLot(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req lotReq
	if err := c.ShouldBindJSON(&req); err != nil {
		WriteError(c, http.StatusBadRequest, "invalid request")
		return
	}
	a := &external.Action{Amount: req.Amount, Shares: req.Shares, UnitPrice: req.UnitPrice, Fee: req.Fee, TradeDate: req.TradeDate, Status: req.Status, Note: req.Note}
	actionID, err := svc.AddLot(c.Request.Context(), id, a)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusCreated, gin.H{"id": actionID})
}

func (h *ExternalHandler) ReduceLot(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req lotReq
	if err := c.ShouldBindJSON(&req); err != nil {
		WriteError(c, http.StatusBadRequest, "invalid request")
		return
	}
	a := &external.Action{Amount: req.Amount, Shares: req.Shares, UnitPrice: req.UnitPrice, Fee: req.Fee, TradeDate: req.TradeDate, Status: req.Status, Note: req.Note}
	actionID, err := svc.ReduceLot(c.Request.Context(), id, a)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusCreated, gin.H{"id": actionID})
}

func (h *ExternalHandler) ConfirmAction(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	actionID, _ := strconv.ParseInt(c.Param("action_id"), 10, 64)
	if err := svc.ConfirmAction(c.Request.Context(), actionID); err != nil {
		WriteError(c, http.StatusBadRequest, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, gin.H{"ok": true})
}

func (h *ExternalHandler) DeleteAction(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	actionID, _ := strconv.ParseInt(c.Param("action_id"), 10, 64)
	if err := svc.DeleteAction(c.Request.Context(), actionID); err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, gin.H{"ok": true})
}

func (h *ExternalHandler) ListDCA(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	schedules, err := svc.ListDCA(c.Request.Context())
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, schedules)
}

func (h *ExternalHandler) CreateDCA(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	var d external.DCASchedule
	if err := c.ShouldBindJSON(&d); err != nil {
		WriteError(c, http.StatusBadRequest, "invalid request")
		return
	}
	id, err := svc.CreateDCA(c.Request.Context(), &d)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusCreated, gin.H{"id": id})
}

func (h *ExternalHandler) UpdateDCA(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var d external.DCASchedule
	if err := c.ShouldBindJSON(&d); err != nil {
		WriteError(c, http.StatusBadRequest, "invalid request")
		return
	}
	d.ID = id
	if err := svc.UpdateDCA(c.Request.Context(), &d); err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, gin.H{"ok": true})
}

func (h *ExternalHandler) DeleteDCA(c *gin.Context) {
	svc, err := h.svc(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := svc.DeleteDCA(c.Request.Context(), id); err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, gin.H{"ok": true})
}
