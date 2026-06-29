package robot

import (
	"context"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
	"github.com/Mininglamp-OSS/octo-server/modules/user"
	"github.com/Mininglamp-OSS/octo-server/pkg/avatarversion"
	"go.uber.org/zap"
)

// 总结助手（Summary Assistant）专属账号自举。
//
// 设计要点（OCT-4 / OCT-5 已批准方案）：
//   - 不复用 Account.SystemUID，而是新建一个独立的官方助手账号，让 octo-smart-summary
//     的异步通知以这个助手身份发出（专属 name / avatar，与系统账号区分）。
//   - 标识、显示名、头像、bot_token 全部从环境/密钥配置读取，不硬编码、不打印、不落日志。
//   - bot_token 必须显式落库（bf_ 前缀，命中 bot_api/auth.go 的 User Bot 分支；
//     db.go queryRobotByBotToken 要求 bot_token!='' AND status=1），否则 smart-summary
//     拿 token 调 /v1/bot/sendMessage 会鉴权失败。
//   - 幂等：重复启动无副作用。若 DB 已存在该 bot 但 bot_token 与配置不一致，以配置为准
//     UPDATE 落库并 warn（不含 token 明文），避免配置漂移导致 token 长期不匹配。
const (
	// 环境变量键。部署方通过 env/secret 注入；缺省时自举静默跳过（不创建助手）。
	EnvSummaryBotUID    = "SUMMARY_BOT_UID"
	EnvSummaryBotName   = "SUMMARY_BOT_NAME"
	EnvSummaryBotAvatar = "SUMMARY_BOT_AVATAR"
	EnvSummaryBotToken  = "SUMMARY_BOT_TOKEN"

	// 显示名兜底，仅在 SUMMARY_BOT_NAME 未配置时使用。
	defaultSummaryBotName = "总结助手"
)

// summaryBotConfig 汇总从环境读取的总结助手配置。
type summaryBotConfig struct {
	UID    string
	Name   string
	Avatar string
	Token  string // bot_token，bf_ 前缀
}

// loadSummaryBotConfig 从环境读取总结助手配置。
// 仅当 UID 与 Token 同时存在时才视为"已启用"（enabled=true）；任一缺失则不自举。
func loadSummaryBotConfig() (cfg summaryBotConfig, enabled bool) {
	cfg = summaryBotConfig{
		UID:    strings.TrimSpace(os.Getenv(EnvSummaryBotUID)),
		Name:   strings.TrimSpace(os.Getenv(EnvSummaryBotName)),
		Avatar: strings.TrimSpace(os.Getenv(EnvSummaryBotAvatar)),
		Token:  strings.TrimSpace(os.Getenv(EnvSummaryBotToken)),
	}
	if cfg.Name == "" {
		cfg.Name = defaultSummaryBotName
	}
	enabled = cfg.UID != "" && cfg.Token != ""
	return cfg, enabled
}

// SummaryBotUID 返回配置中的总结助手 UID（未配置时为空串）。
// 供 bot_api 的 ensureFriend 端点做 UID 白名单门控（仅放行这一个 UID）。
func SummaryBotUID() string {
	return strings.TrimSpace(os.Getenv(EnvSummaryBotUID))
}

// insertSummaryRobot 幂等地自举"总结助手"账号（user 行 + robot 行 + 显式 bot_token）。
//
// 与 insertSystemRobot 的范式对齐（事务 + GenSeq(RobotSeqKey) + Status=Enable +
// Token=GenerUUID），但额外：
//   - 创建对应的 user 账号（Robot=1），否则发送/好友链路缺少该 UID 的用户实体；
//   - 必须显式落 bot_token；
//   - 存在性检查后做配置防漂移（bot_token / status 不一致 → 以配置为准 UPDATE）。
func (rb *Robot) insertSummaryRobot() error {
	cfg, enabled := loadSummaryBotConfig()
	if !enabled {
		// 未配置总结助手 → 静默跳过，不影响其余 bot 初始化。
		rb.Info("总结助手未配置（缺少 SUMMARY_BOT_UID / SUMMARY_BOT_TOKEN），跳过自举")
		return nil
	}

	existing, err := rb.db.queryRobotWithRobtID(cfg.UID)
	if err != nil {
		rb.Error("查询总结助手机器人错误", zap.Error(err))
		return err
	}

	if existing != nil {
		// 已存在 → 配置防漂移：bot_token / status 以配置为准，落库纠偏（不打印 token 明文）。
		return rb.reconcileSummaryRobot(cfg, existing)
	}

	// 不存在 → 创建 user 账号 + robot 行（含显式 bot_token）。
	robotVersion, err := rb.ctx.GenSeq(common.RobotSeqKey)
	if err != nil {
		rb.Error("总结助手 GenSeq 失败", zap.Error(err))
		return err
	}

	// 先建 user 账号（AddUser 自身是单条 Insert；Robot=1 标记为机器人用户）。
	// 若 user 已存在（如此前部分初始化）AddUser 会因唯一键失败，这里容忍并继续建 robot 行，
	// 以保证整体幂等。
	if uerr := rb.userService.AddUser(&user.AddUserReq{
		UID:      cfg.UID,
		Name:     cfg.Name,
		Username: cfg.UID,
		Robot:    1,
	}); uerr != nil {
		rb.Warn("创建总结助手 user 账号失败（可能已存在），继续建 robot 行", zap.Error(uerr))
	}

	if err := rb.db.insert(&robot{
		RobotID:  cfg.UID,
		Username: cfg.UID,
		Status:   int(Enable),
		Token:    util.GenerUUID(),
		BotToken: cfg.Token,
		Version:  robotVersion,
	}); err != nil {
		rb.Error("添加总结助手 robot 行失败", zap.Error(err))
		return err
	}

	// PR#483 review (Jerry-Xin + OctoBoooot 复核确认 🟡)：SUMMARY_BOT_AVATAR 在
	// 之前的版本读了但没应用 —— 这一步把配置的 avatar URL 下载并落到对象存储，
	// 同时把 user 表的 is_upload_avatar / avatar_version 标记好，后续 /users/{uid}/avatar
	// 取头像能命中 octo 内置 avatar 字节流。best-effort：失败仅 warn，不阻断自举。
	rb.applySummaryBotAvatar(cfg)

	rb.Info("总结助手自举完成", zap.String("uid", cfg.UID))
	return nil
}

// reconcileSummaryRobot 已存在时的配置防漂移：以配置为准纠正 bot_token / status。
// 不在日志中输出 bot_token 明文。
func (rb *Robot) reconcileSummaryRobot(cfg summaryBotConfig, existing *robot) error {
	fields := map[string]interface{}{}

	if existing.BotToken != cfg.Token {
		fields["bot_token"] = cfg.Token
		rb.Warn("总结助手 bot_token 与配置不一致，以配置为准纠偏（token 不打印）",
			zap.String("uid", cfg.UID))
	}
	// 助手必须可用：命中 bot_api User Bot 分支要求 status=1。
	if existing.Status != int(Enable) {
		fields["status"] = int(Enable)
		rb.Warn("总结助手 status 非启用，纠正为启用", zap.String("uid", cfg.UID))
	}

	if len(fields) != 0 {
		if err := rb.db.updateRobotInfo(cfg.UID, fields); err != nil {
			rb.Error("总结助手配置纠偏 UPDATE 失败", zap.Error(err))
			return err
		}
	}

	// PR#483 review (Jerry-Xin 🟡)：reconcile 也以配置为准应用 avatar —— 漂移时
	// （配置改了，部署重启）旧 avatar 会被替换。best-effort：失败仅 warn。
	rb.applySummaryBotAvatar(cfg)
	return nil
}

// applySummaryBotAvatar 把 cfg.Avatar（远程 URL）下载并落到 octo 头像对象存储，
// 标记对应 user 行 is_upload_avatar=1 / avatar_version=<新版本>。avatar 路径
// 沿用 modules/user/avatar_path.go 的 `avatar/{crc32(uid)%partition}/{uid}/{ver}.png`
// 公式（与 api_github.go 创建用户走的同一路径），保证后续
// /users/{uid}/avatar 取头像能命中。
//
// 失败语义：best-effort。任何一步出错（空配置、下载失败、上传失败、DB update 失败）
// 只 warn，不影响 bot 自举主链路；漂移路径下次启动还会重试。
func (rb *Robot) applySummaryBotAvatar(cfg summaryBotConfig) {
	if strings.TrimSpace(cfg.Avatar) == "" {
		return
	}
	downloadCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	imgReader, err := rb.fileService.DownloadImage(cfg.Avatar, downloadCtx)
	if err != nil || imgReader == nil {
		rb.Warn("总结助手 avatar 下载失败（best-effort，跳过）",
			zap.String("uid", cfg.UID), zap.Error(err))
		return
	}
	defer imgReader.Close()

	avatarVersion := avatarversion.New()
	partition := rb.ctx.GetConfig().Avatar.Partition
	if partition <= 0 {
		// 与 modules/user/avatar_path.go 一致的兜底（partition 必须 >0 取模）。
		partition = 1
	}
	avatarID := crc32.ChecksumIEEE([]byte(cfg.UID)) % uint32(partition)
	objPath := fmt.Sprintf("avatar/%d/%s/%d.png", avatarID, cfg.UID, avatarVersion)

	if _, err := rb.fileService.UploadFile(objPath, "image/png", "", func(w io.Writer) error {
		_, copyErr := io.Copy(w, imgReader)
		return copyErr
	}); err != nil {
		rb.Warn("总结助手 avatar 上传对象存储失败（best-effort，跳过）",
			zap.String("uid", cfg.UID), zap.Error(err))
		return
	}

	if _, err := rb.ctx.DB().Update("user").SetMap(map[string]interface{}{
		"is_upload_avatar": 1,
		"avatar_version":   avatarVersion,
	}).Where("uid=?", cfg.UID).Exec(); err != nil {
		rb.Warn("总结助手 avatar user 表标记失败（best-effort，跳过）",
			zap.String("uid", cfg.UID), zap.Error(err))
		return
	}
	rb.Info("总结助手 avatar 已落库", zap.String("uid", cfg.UID), zap.Int64("avatar_version", avatarVersion))
}
