-- +migrate Up

-- 用户外部自定义 token 别名表(per-user)。
--
-- 设计要点(YUJ-3538):
--   - secret_id   是稳定内部 ID(引用用它),重命名 display_name 不断引用。
--   - owner_uid + display_name_norm 唯一:同一用户下别名按 normalize 后去重,
--     撞名让上层提示换名。不同用户之间互不影响。
--   - display_name 保留用户原始输入(语音友好,可含中文/空格/大小写),
--     display_name_norm 是去空格 + 折叠大小写 + 简繁归一后的判重键。
--   - cipher_text 是 AES-256-GCM 密文(带版本前缀 "enc:v1:"),
--     **任何接口永不回显明文/密文**;主密钥沿用 botfather user-api-key 的
--     OCTO_USER_API_KEY_SECRET(32B)派生子密钥,与 oidc/botfather 同一套
--     AES-GCM 原语,不另造轮子。
--   - kind 仅作分类过滤用(llm / external),不参与鉴权或解析逻辑。
--   - last_used_at 由 resolve 成功后回写,便于用户识别长期未用的 key。
create table `user_secret_alias`
(
    id                bigint         not null primary key AUTO_INCREMENT,
    secret_id         VARCHAR(40)    not null,                                       -- 稳定内部 ID,引用锚点(uuid)
    owner_uid         VARCHAR(40)    not null,                                       -- 归属用户 UID
    display_name      VARCHAR(128)   not null,                                       -- 用户原始别名(语音友好,可重命名)
    display_name_norm VARCHAR(128)   not null,                                       -- normalize 后的判重键
    kind              VARCHAR(16)    not null default 'external',                    -- 分类:llm / external
    cipher_text       varbinary(8240) not null,                                      -- AES-256-GCM 密文(含 enc:v1: 前缀)。列宽覆盖明文上限 8192B + 开销(前缀7+nonce12+tag16=35B)≈8227B,留余量到 8240 防截断脏行
    masked            VARCHAR(8)     not null default '',                            -- 明文尾 4 位掩码(低敏,供 list 展示,免解密)
    created_at        timestamp      not null default CURRENT_TIMESTAMP,
    updated_at        timestamp      not null default CURRENT_TIMESTAMP on update CURRENT_TIMESTAMP,
    last_used_at      timestamp      null     default null,
    unique key uk_secret_id (secret_id),
    unique key uk_owner_norm (owner_uid, display_name_norm),
    key idx_owner (owner_uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- 别名 resolve 审计日志(P0)。
--
-- 记录每次 resolve:谁(caller_kind + caller_id)、何时、解了哪个 owner 的
-- 哪个 secret_id、命中结果(ok / not_found / ambiguous / request_invalid /
-- unauthorized / decrypt_fail / internal_error)。
-- 与 oidc_audit_log 风格一致:无 FK,best-effort 写入,审计失败不阻塞 resolve。
-- secret_id 命中歧义 / 未命中时为空,result 列标明原因。
create table `user_secret_resolve_audit`
(
    id          bigint         not null primary key AUTO_INCREMENT,
    owner_uid   VARCHAR(40)    not null default '',                                  -- 被解析 key 的归属用户
    caller_kind VARCHAR(16)    not null default '',                                  -- 调用方类型:当前仅 user_bot(bf_ token)
    caller_id   VARCHAR(64)    not null default '',                                  -- 调用方 ID(robot_id)
    query       VARCHAR(128)   not null default '',                                  -- 入参别名(按 rune 边界截断到 128 字符,不含明文 key)
    secret_id   VARCHAR(40)    not null default '',                                  -- 命中的 secret_id(唯一命中才有值)
    result      VARCHAR(24)    not null default '',                                  -- ok/not_found/ambiguous/request_invalid/unauthorized/decrypt_fail/internal_error
    candidates  int            not null default 0,                                   -- 匹配候选数(歧义时 >1)
    ip          VARCHAR(45)    not null default '',
    created_at  timestamp      not null default CURRENT_TIMESTAMP,
    updated_at  timestamp      not null default CURRENT_TIMESTAMP on update CURRENT_TIMESTAMP,
    key idx_owner_time (owner_uid, created_at),
    key idx_secret (secret_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;
