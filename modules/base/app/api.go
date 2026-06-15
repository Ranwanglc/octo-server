package app

import (
	"net/http"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"go.uber.org/zap"
)

type App struct {
	service IService
	log.Log
}

func New(ctx *config.Context) *App {
	return &App{
		service: NewService(ctx),
		Log:     log.NewTLog("App"),
	}
}

func (a *App) Route(r *wkhttp.WKHttp) {
	r.GET("/v1/apps/:app_id", a.get)
}

func (a *App) get(c *wkhttp.Context) {
	appID := c.Param("app_id")
	resp, err := a.service.GetApp(appID)
	if err != nil {
		// GetApp conflates "not found" with read errors; log so a genuine
		// storage failure is observable rather than an unobservable 404.
		a.Error("GetApp failed", zap.String("app_id", appID), zap.Error(err))
		respondAppNotFound(c)
		return
	}
	if resp.Status == StatusDisable {
		respondAppDisabled(c)
		return
	}
	c.JSON(http.StatusOK, &appResp{
		AppID:   resp.AppID,
		AppName: resp.AppName,
		AppLogo: resp.AppLogo,
	})
}

type appResp struct {
	AppID   string `json:"app_id,omitempty"`
	AppName string `json:"app_name,omitempty"`
	AppLogo string `json:"app_logo,omitempty"`
}
