package doc_event_receiver

// doc_event_receiver: 收 doc 侧 (octo-docs-html) 回投的评论/事件，翻译成 IM 消息
// 按 doc_binding 的挂载坐标投递到父群/子区。挂载类型 space 暂不落地投递（方案未定）。
//
// 未配 OCTO_DOC_EVENT_WEBHOOK_TOKEN → endpoint 一律 503 拒收，日志明写「未配 token」。

import (
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/register"
)

func init() {
	register.AddModule(func(ctx interface{}) register.Module {
		return register.Module{
			Name: "doc_event_receiver",
			SetupAPI: func() register.APIRouter {
				return New(ctx.(*config.Context))
			},
		}
	})
}
