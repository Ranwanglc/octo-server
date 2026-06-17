-- +migrate Up
-- octo-server #394：建群与 IM 频道创建之间存在「孤儿群」窗口——CreateGroup 在
-- tx.Commit() 之后才创建 IM 频道，若 IM 创建失败且补偿删除也失败（或在 commit 与
-- IM 创建之间崩溃），group 行会残留而没有对应 IM 频道。channel_synced 标记把这种
-- 孤儿变得「可检测」：仅在确认 IM 频道创建成功后才置 1，后台 reconcile worker 据此
-- 找回并幂等重建。
--
-- 默认 1（已同步）：存量群以及所有不经过 CreateGroup 待确认流程的插入路径（系统群、
-- 注册建群、AddGroup 等）天然视为已同步，零回归、不会被 reconcile 误判为孤儿。
-- 只有 CreateGroup 显式把新行写成 0，确认 IM 频道后再翻成 1。
--
-- 幂等/可重入写法（INFORMATION_SCHEMA 守卫 + 存储过程，与 20260604000001、
-- 20260605000002 同范式）：MySQL DDL 隐式提交，sql-migrate 仅在「一个迁移的所有
-- 语句都成功」后才记账；多 pod 滚动发布或部分迁移重试时，裸 ADD COLUMN 重跑会因
-- 列已存在报 Duplicate column name 而 CrashLoopBackOff（见 #239/#253）。守卫后可安全重入。
-- +migrate StatementBegin
DROP PROCEDURE IF EXISTS __group_channel_synced;
-- +migrate StatementEnd

-- +migrate StatementBegin
CREATE PROCEDURE __group_channel_synced()
BEGIN
  IF NOT EXISTS (SELECT 1 FROM information_schema.COLUMNS
       WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group'
         AND COLUMN_NAME = 'channel_synced') THEN
    ALTER TABLE `group`
      ADD COLUMN `channel_synced` TINYINT NOT NULL DEFAULT 1 COMMENT 'IM channel sync flag: 1=backing IM channel confirmed (default, backward-compat), 0=group row committed but IM channel not yet confirmed (reconcile target, octo-server #394)';
  END IF;
  -- 二级索引：reconcile worker 周期性扫描 channel_synced=0 的孤儿行，避免全表扫描。
  -- 选择性极高（正常情况下几乎没有 0 行），低基数列单列索引足够。
  IF NOT EXISTS (SELECT 1 FROM information_schema.STATISTICS
       WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group'
         AND INDEX_NAME = 'idx_group_channel_synced') THEN
    ALTER TABLE `group`
      ADD INDEX `idx_group_channel_synced` (`channel_synced`);
  END IF;
END;
-- +migrate StatementEnd

CALL __group_channel_synced();

-- +migrate StatementBegin
DROP PROCEDURE IF EXISTS __group_channel_synced;
-- +migrate StatementEnd

-- +migrate Down
-- +migrate StatementBegin
DROP PROCEDURE IF EXISTS __group_channel_synced_down;
-- +migrate StatementEnd

-- +migrate StatementBegin
CREATE PROCEDURE __group_channel_synced_down()
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.STATISTICS
       WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group'
         AND INDEX_NAME = 'idx_group_channel_synced') THEN
    ALTER TABLE `group` DROP INDEX `idx_group_channel_synced`;
  END IF;
  IF EXISTS (SELECT 1 FROM information_schema.COLUMNS
       WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'group'
         AND COLUMN_NAME = 'channel_synced') THEN
    ALTER TABLE `group` DROP COLUMN `channel_synced`;
  END IF;
END;
-- +migrate StatementEnd

CALL __group_channel_synced_down();

-- +migrate StatementBegin
DROP PROCEDURE IF EXISTS __group_channel_synced_down;
-- +migrate StatementEnd
