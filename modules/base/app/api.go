package app

import (
	"net/http"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
)

type App struct {
	service IService
}

func New(ctx *config.Context) *App {
	return &App{
		service: NewService(ctx),
	}
}

func (a *App) Route(r *wkhttp.WKHttp) {
	r.GET("/v1/apps/:app_id", a.get)
}

func (a *App) get(c *wkhttp.Context) {
	appID := c.Param("app_id")
	resp, err := a.service.GetApp(appID)
	if err != nil {
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
