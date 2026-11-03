-- +migrate Up
ALTER TABLE agent_memory_profile
  ADD COLUMN authorization_epoch BIGINT NOT NULL DEFAULT 0 AFTER policy_version_id;

ALTER TABLE memory_bank
  ADD COLUMN created_by_uid VARCHAR(40) NOT NULL DEFAULT '' AFTER owner_uid;

ALTER TABLE memory_grant
  ADD COLUMN source_kind ENUM('ownership', 'offer_acceptance', 'platform_override', 'explicit_deny') NOT NULL DEFAULT 'ownership' AFTER effect,
  ADD COLUMN source_offer_id VARCHAR(40) DEFAULT NULL AFTER source_kind,
  ADD COLUMN source_acceptance_id VARCHAR(40) DEFAULT NULL AFTER source_offer_id,
  ADD COLUMN grant_set_version BIGINT NOT NULL DEFAULT 0 AFTER source_acceptance_id,
  ADD KEY idx_memory_grant_source_offer (source_offer_id),
  ADD KEY idx_memory_grant_source_acceptance (source_acceptance_id);

CREATE TABLE IF NOT EXISTS memory_capability_offer (
  id VARCHAR(40) NOT NULL,
  revision BIGINT NOT NULL,
  bank_id VARCHAR(40) NOT NULL,
  target_robot_id VARCHAR(40) NOT NULL,
  audience_space_id VARCHAR(40) NOT NULL,
  offered_capabilities_json JSON NOT NULL,
  status ENUM('active', 'superseded', 'revoked', 'expired') NOT NULL DEFAULT 'active',
  valid_from DATETIME NOT NULL,
  valid_until DATETIME DEFAULT NULL,
  issuer_uid VARCHAR(40) NOT NULL,
  reason VARCHAR(500) NOT NULL,
  policy_version_id VARCHAR(40) NOT NULL,
  version BIGINT NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  revoked_at DATETIME DEFAULT NULL,
  expired_at DATETIME DEFAULT NULL,
  PRIMARY KEY (id, revision),
  KEY idx_memory_offer_bank (bank_id, status),
  KEY idx_memory_offer_robot (target_robot_id, status),
  CONSTRAINT fk_memory_offer_bank FOREIGN KEY (bank_id) REFERENCES memory_bank(id),
  CONSTRAINT fk_memory_offer_robot FOREIGN KEY (target_robot_id) REFERENCES robot(robot_id),
  CONSTRAINT fk_memory_offer_policy FOREIGN KEY (policy_version_id) REFERENCES memory_policy_version(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS memory_offer_acceptance (
  id VARCHAR(40) PRIMARY KEY,
  offer_id VARCHAR(40) NOT NULL,
  offer_revision BIGINT NOT NULL,
  robot_id VARCHAR(40) NOT NULL,
  accepted_capabilities_json JSON NOT NULL,
  status ENUM('pending', 'accepted', 'declined', 'withdrawn', 'invalidated') NOT NULL,
  accepted_by_owner_uid VARCHAR(40) NOT NULL,
  reason VARCHAR(500) NOT NULL DEFAULT '',
  version BIGINT NOT NULL DEFAULT 0,
  decided_at DATETIME DEFAULT NULL,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  invalidated_at DATETIME DEFAULT NULL,
  UNIQUE KEY uk_memory_acceptance_offer_revision (offer_id, offer_revision),
  KEY idx_memory_acceptance_robot (robot_id, status),
  CONSTRAINT fk_memory_acceptance_offer FOREIGN KEY (offer_id, offer_revision) REFERENCES memory_capability_offer(id, revision),
  CONSTRAINT fk_memory_acceptance_robot FOREIGN KEY (robot_id) REFERENCES robot(robot_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS memory_enablement_attempt (
  id VARCHAR(40) PRIMARY KEY,
  robot_id VARCHAR(40) NOT NULL,
  initiated_by_uid VARCHAR(40) NOT NULL,
  state ENUM('draft', 'probing', 'ready', 'committed', 'failed', 'expired', 'cancelled') NOT NULL DEFAULT 'draft',
  profile_version BIGINT NOT NULL,
  credential_epoch BIGINT NOT NULL,
  authorization_epoch BIGINT NOT NULL,
  policy_version_id VARCHAR(40) NOT NULL,
  dependency_versions_json JSON NOT NULL,
  result_reason_code VARCHAR(80) NOT NULL DEFAULT '',
  probe_steps_json JSON NOT NULL,
  probe_expires_at DATETIME DEFAULT NULL,
  idempotency_key VARCHAR(120) NOT NULL,
  version BIGINT NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uk_memory_enablement_idempotency (robot_id, idempotency_key),
  KEY idx_memory_enablement_robot (robot_id, state),
  CONSTRAINT fk_memory_enablement_robot FOREIGN KEY (robot_id) REFERENCES robot(robot_id),
  CONSTRAINT fk_memory_enablement_policy FOREIGN KEY (policy_version_id) REFERENCES memory_policy_version(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- +migrate Down
DROP TABLE IF EXISTS memory_enablement_attempt;
DROP TABLE IF EXISTS memory_offer_acceptance;
DROP TABLE IF EXISTS memory_capability_offer;

ALTER TABLE memory_grant
  DROP INDEX idx_memory_grant_source_acceptance,
  DROP INDEX idx_memory_grant_source_offer,
  DROP COLUMN grant_set_version,
  DROP COLUMN source_acceptance_id,
  DROP COLUMN source_offer_id,
  DROP COLUMN source_kind;

ALTER TABLE memory_bank DROP COLUMN created_by_uid;
ALTER TABLE agent_memory_profile DROP COLUMN authorization_epoch;
