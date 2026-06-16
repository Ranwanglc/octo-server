-- +migrate Up
-- bot_mention_pref: 群级免@偏好稀疏表。维度 (robot_id, group_no) → no_mention。
-- 只存「偏离账号级默认」的覆盖项；无记录时调用方回退账号级 requireMention（零回归）。
CREATE TABLE IF NOT EXISTS `bot_mention_pref` (
  `id`         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `robot_id`   VARCHAR(40)     NOT NULL DEFAULT '' COMMENT 'Bot UID',
  `group_no`   VARCHAR(40)     NOT NULL DEFAULT '' COMMENT '群唯一编号',
  `no_mention` TINYINT         NOT NULL DEFAULT 0  COMMENT '0=遵循账号级默认 1=本群免@回答',
  `created_at` TIMESTAMP       NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP       NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `updated_by` VARCHAR(40)     NOT NULL DEFAULT '' COMMENT '最后修改人 UID',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_robot_group` (`robot_id`, `group_no`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='Bot 群级免@偏好（稀疏覆盖表）';

-- +migrate Down
DROP TABLE IF EXISTS `bot_mention_pref`;
