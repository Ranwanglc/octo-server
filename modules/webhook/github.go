package webhook

import (
	"io"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"go.uber.org/zap"
)

func (w *Webhook) github(c *wkhttp.Context) {
	w.Debug("github webhook", zap.Any("params", c.Params))

	result, _ := io.ReadAll(c.Request.Body)
	w.Debug("github webhook result", zap.ByteString("result", result))
}
