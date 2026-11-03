-- +migrate Up
ALTER TABLE robot
  ADD COLUMN space_id VARCHAR(40) NULL AFTER creator_uid,
  ADD COLUMN credential_epoch BIGINT NOT NULL DEFAULT 0 AFTER bot_token,
  ADD KEY idx_robot_memory_space (space_id, status);

CREATE TABLE robot_memory_space_migration_audit (
  robot_id VARCHAR(40) PRIMARY KEY,
  candidate_count BIGINT NOT NULL,
  resolution ENUM('resolved', 'unresolved') NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

INSERT INTO robot_memory_space_migration_audit (robot_id, candidate_count, resolution)
SELECT r.robot_id, COUNT(DISTINCT s.space_id),
       IF(COUNT(DISTINCT s.space_id) = 1, 'resolved', 'unresolved')
FROM robot r
LEFT JOIN space_member sm ON sm.uid = r.robot_id AND sm.status = 1
LEFT JOIN space s ON s.space_id = sm.space_id AND s.status = 1
GROUP BY r.robot_id;

UPDATE robot r
JOIN (
  SELECT sm.uid AS robot_id, MIN(s.space_id) AS space_id
  FROM space_member sm
  JOIN space s ON s.space_id = sm.space_id AND s.status = 1
  WHERE sm.status = 1
  GROUP BY sm.uid
  HAVING COUNT(DISTINCT s.space_id) = 1
) candidate ON candidate.robot_id = r.robot_id
SET r.space_id = candidate.space_id;

-- +migrate Down
DROP TABLE IF EXISTS robot_memory_space_migration_audit;
ALTER TABLE robot
  DROP INDEX idx_robot_memory_space,
  DROP COLUMN credential_epoch,
  DROP COLUMN space_id;
