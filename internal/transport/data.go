package transport

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/jwcen/argent-go/internal/auth"
	"github.com/jwcen/argent-go/internal/infra/sqlite"
	"github.com/jwcen/argent-go/internal/portfolio"
)

// DataHandler 数据导入导出。
type DataHandler struct {
	dbFn func(userID int64) (*sql.DB, error)
}

func NewDataHandler(dbFn func(userID int64) (*sql.DB, error)) *DataHandler {
	return &DataHandler{dbFn: dbFn}
}

func (h *DataHandler) Register(r gin.IRouter) {
	g := r.Group("/data")
	g.GET("/export", h.Export)
	g.POST("/import", h.Import)
}

// ExportData 导出的数据结构。
type ExportData struct {
	Holdings []portfolio.Holding `json:"holdings"`
	Actions  []portfolio.Action  `json:"actions"`
	Brokers  []portfolio.Broker  `json:"brokers"`
}

// GET /api/data/export — 导出全量业务数据为 JSON
func (h *DataHandler) Export(c *gin.Context) {
	uid := auth.MustUserID(c.Request.Context())
	if uid == 0 {
		WriteError(c, http.StatusUnauthorized, "not authenticated")
		return
	}
	db, err := h.dbFn(uid)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	repo := sqlite.NewPortfolioRepo(db)
	svc := portfolio.NewService(repo)

	holdings, _ := svc.ListHoldings(c.Request.Context())
	actions, _ := repo.ListAllActions(c.Request.Context())
	brokers, _ := svc.ListBrokers(c.Request.Context())

	data := ExportData{
		Holdings: holdings,
		Actions:  actions,
		Brokers:  brokers,
	}

	c.Header("Content-Disposition", "attachment; filename=argent-export.json")
	WriteJSON(c, http.StatusOK, data)
}

// POST /api/data/import — 导入数据（merge 模式）
func (h *DataHandler) Import(c *gin.Context) {
	uid := auth.MustUserID(c.Request.Context())
	if uid == 0 {
		WriteError(c, http.StatusUnauthorized, "not authenticated")
		return
	}

	var data ExportData
	if err := c.ShouldBindJSON(&data); err != nil {
		WriteError(c, http.StatusBadRequest, "invalid JSON")
		return
	}

	db, err := h.dbFn(uid)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	repo := sqlite.NewPortfolioRepo(db)
	svc := portfolio.NewService(repo)

	// merge: 逐条插入，冲突跳过
	imported := 0
	for _, a := range data.Actions {
		_, err := svc.CreateAction(c.Request.Context(), &a)
		if err == nil {
			imported++
		}
	}

	for _, b := range data.Brokers {
		_, err := svc.CreateBroker(c.Request.Context(), &b)
		if err == nil {
			imported++
		}
	}

	WriteJSON(c, http.StatusOK, gin.H{"imported": imported})
}

// 确保 json 和 context 被引用
