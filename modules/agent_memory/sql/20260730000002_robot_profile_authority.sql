-- +migrate Up
CREATE TABLE IF NOT EXISTS agent_memory_profile_robot_20260730 (
  robot_id VARCHAR(40) PRIMARY KEY,
  enabled TINYINT(1) NOT NULL DEFAULT 0,
  include_own_tentative TINYINT(1) NOT NULL DEFAULT 0,
  trust_tier ENUM('low', 'normal', 'high') NOT NULL DEFAULT 'normal',
  policy_version_id VARCHAR(40) NOT NULL,
  version BIGINT NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  disabled_at DATETIME DEFAULT NULL,
  CONSTRAINT fk_agent_memory_profile_robot_20260730 FOREIGN KEY (robot_id) REFERENCES robot(robot_id),
  CONSTRAINT fk_agent_memory_profile_policy_20260730 FOREIGN KEY (policy_version_id) REFERENCES memory_policy_version(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

SET @agent_memory_profile_is_legacy := (
  SELECT COUNT(*) FROM information_schema.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'agent_memory_profile'
    AND COLUMN_NAME = 'app_bot_id'
);
SET @agent_memory_profile_up_sql := IF(
  @agent_memory_profile_is_legacy = 1,
  'RENAME TABLE agent_memory_profile TO agent_memory_profile_app_bot_legacy_20260730, agent_memory_profile_robot_20260730 TO agent_memory_profile',
  'DROP TABLE agent_memory_profile_robot_20260730'
);
PREPARE agent_memory_profile_up_stmt FROM @agent_memory_profile_up_sql;
EXECUTE agent_memory_profile_up_stmt;
DEALLOCATE PREPARE agent_memory_profile_up_stmt;

-- +migrate Down
SET @agent_memory_profile_has_legacy := (
  SELECT COUNT(*) FROM information_schema.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_memory_profile_app_bot_legacy_20260730'
);
SET @agent_memory_profile_drop_sql := IF(
  @agent_memory_profile_has_legacy = 1, 'DROP TABLE agent_memory_profile', 'SELECT 1'
);
PREPARE agent_memory_profile_drop_stmt FROM @agent_memory_profile_drop_sql;
EXECUTE agent_memory_profile_drop_stmt;
DEALLOCATE PREPARE agent_memory_profile_drop_stmt;
SET @agent_memory_profile_restore_sql := IF(
  @agent_memory_profile_has_legacy = 1,
  'RENAME TABLE agent_memory_profile_app_bot_legacy_20260730 TO agent_memory_profile',
  'SELECT 1'
);
PREPARE agent_memory_profile_restore_stmt FROM @agent_memory_profile_restore_sql;
EXECUTE agent_memory_profile_restore_stmt;
DEALLOCATE PREPARE agent_memory_profile_restore_stmt;
