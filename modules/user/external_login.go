package user

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
	"go.uber.org/zap"
)

// ExternalLoginReq external IdP（OIDC / OAuth）登录入参。
//
// ExistingUID 为空表示按 IdP 返回的 claims 新建本地用户;非空则按已知 UID 登录。
// 调用方（oidc 模块的 ResolveOrLink）负责完成 (issuer, sub) → uid 的解析与绑定，
// 这里只负责签发 DMWork 会话 token + 推 WuKongIM。
type ExternalLoginReq struct {
	ExistingUID string

	// 新建用户场景下使用,ExistingUID 非空时忽略
	UID   string // 调用方生成的 UID（避免重复 GenerUUID 后还要再回传）
	Name  string
	Email string
	Phone string
	Zone  string

	DeviceFlag config.DeviceFlag
	Device     *DeviceInfo

	// PublicIP 用于欢迎消息日志,可空
	PublicIP string
}

// DeviceInfo 登录设备信息（外部模块用,与内部 deviceReq 解耦）
type DeviceInfo struct {
	DeviceID    string
	DeviceName  string
	DeviceModel string
}

// ExternalLoginResp 外部登录结果。
//
// LoginRespJSON 是 loginUserDetailResp 序列化后的 JSON 字符串,可直接落到
// ThirdAuthcode Redis 缓冲区供前端短码轮询取走;调用方无需关心其内部结构。
type ExternalLoginResp struct {
	UID           string
	IsNewUser     bool
	LoginRespJSON string
}

// LoginByExternalIdentity 给外部 IdP（OIDC / OAuth）登录流程签发 DMWork 会话。
//
// ExistingUID 非空 → 走 execLogin（已有用户）；
// ExistingUID 为空 → 走 createUserWithRespAndTx（创建用户 + 登录,事务内）。
//
// 行为复用 GitHub 登录路径（api_github.go），oidc 模块通过 IService 间接调用。
func (u *User) LoginByExternalIdentity(ctx context.Context, req ExternalLoginReq) (*ExternalLoginResp, error) {
	if req.ExistingUID != "" {
		return u.externalLoginExisting(ctx, req)
	}
	return u.externalLoginCreate(ctx, req)
}

func (u *User) externalLoginExisting(ctx context.Context, req ExternalLoginReq) (*ExternalLoginResp, error) {
	userInfoM, err := u.db.QueryByUID(req.ExistingUID)
	if err != nil {
		return nil, fmt.Errorf("user: query existing user uid=%s: %w", req.ExistingUID, err)
	}
	if userInfoM == nil {
		return nil, errors.New("用户不存在")
	}
	// IsDestroy 三态(db.go:15):0=正常 1=冷静期(可撤销) 2=已注销(终态)。
	// 冷静期用户允许登录,登录动作即撤销注销;已注销用户拒绝。
	// 与 api_emaillogin.go:245 / api.go:1012 等其他登录入口对齐。
	if userInfoM.IsDestroy == IsDestroyDone {
		return nil, errors.New("用户不存在")
	}

	loginResp, err := u.execLogin(userInfoM, req.DeviceFlag, toDeviceReq(req.Device), ctx)
	if err != nil {
		return nil, err
	}
	go u.sentWelcomeMsg(req.PublicIP, userInfoM.UID)

	return &ExternalLoginResp{
		UID:           userInfoM.UID,
		IsNewUser:     false,
		LoginRespJSON: util.ToJson(loginResp),
	}, nil
}

func (u *User) externalLoginCreate(ctx context.Context, req ExternalLoginReq) (*ExternalLoginResp, error) {
	if req.UID == "" {
		return nil, errors.New("user: external login: UID is required when creating new user")
	}

	tx, err := u.ctx.DB().Begin()
	if err != nil {
		return nil, fmt.Errorf("user: external login begin tx: %w", err)
	}
	// 与 githubOAuth 一致:仅 panic 时回滚,正常路径由下方 commit/rollback 显式控制
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			fmt.Fprintf(os.Stderr, "recovered panic in LoginByExternalIdentity: %v\n%s\n", r, debug.Stack())
		}
	}()

	createUser := &createUserModel{
		UID:      req.UID,
		Name:     req.Name,
		Email:    req.Email,
		Phone:    req.Phone,
		Zone:     req.Zone,
		Flag:     int(req.DeviceFlag.Uint8()),
		Device:   toDeviceReq(req.Device),
	}

	loginResp, err := u.createUserWithRespAndTx(ctx, createUser, req.PublicIP, nil, tx, func() error {
		if commitErr := tx.Commit(); commitErr != nil {
			tx.Rollback()
			u.Error("数据库事务提交失败", zap.Error(commitErr))
			return fmt.Errorf("user: external login commit tx: %w", commitErr)
		}
		return nil
	})
	if err != nil {
		tx.Rollback()
		return nil, err
	}

	return &ExternalLoginResp{
		UID:           req.UID,
		IsNewUser:     true,
		LoginRespJSON: util.ToJson(loginResp),
	}, nil
}

func toDeviceReq(d *DeviceInfo) *deviceReq {
	if d == nil {
		return nil
	}
	return &deviceReq{
		DeviceID:    d.DeviceID,
		DeviceName:  d.DeviceName,
		DeviceModel: d.DeviceModel,
	}
}
