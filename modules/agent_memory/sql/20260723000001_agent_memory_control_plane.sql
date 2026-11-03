-- +migrate Up
CREATE TABLE IF NOT EXISTS memory_policy_version (
  id VARCHAR(40) PRIMARY KEY,
  version VARCHAR(30) NOT NULL,
  status ENUM('draft', 'active', 'retired') NOT NULL DEFAULT 'draft',
  document_json JSON NOT NULL,
  document_hash CHAR(64) NOT NULL,
  created_by_uid VARCHAR(40) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  activated_at DATETIME DEFAULT NULL,
  UNIQUE KEY uk_memory_policy_version_version (version),
  KEY idx_memory_policy_version_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS agent_memory_profile (
  robot_id VARCHAR(40) PRIMARY KEY,
  enabled TINYINT(1) NOT NULL DEFAULT 0,
  include_own_tentative TINYINT(1) NOT NULL DEFAULT 0,
  trust_tier ENUM('low', 'normal', 'high') NOT NULL DEFAULT 'normal',
  policy_version_id VARCHAR(40) NOT NULL,
  version BIGINT NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  disabled_at DATETIME DEFAULT NULL,
  CONSTRAINT fk_agent_memory_profile_robot FOREIGN KEY (robot_id) REFERENCES robot(robot_id),
  CONSTRAINT fk_agent_memory_profile_policy FOREIGN KEY (policy_version_id) REFERENCES memory_policy_version(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS memory_bank (
  id VARCHAR(40) PRIMARY KEY,
  hindsight_bank_id VARCHAR(120) DEFAULT NULL,
  display_name VARCHAR(120) NOT NULL,
  bank_type ENUM('private', 'tentative', 'shared', 'verified', 'quarantine') NOT NULL,
  scope_type ENUM('agent', 'space', 'organization') NOT NULL,
  scope_id VARCHAR(40) NOT NULL,
  owner_uid VARCHAR(40) NOT NULL,
  sensitivity ENUM('internal', 'confidential', 'restricted') NOT NULL DEFAULT 'internal',
  retention_class VARCHAR(40) NOT NULL,
  policy_version_id VARCHAR(40) NOT NULL,
  status ENUM('provisioning', 'active', 'disabled', 'archived', 'migration_unmapped') NOT NULL DEFAULT 'provisioning',
  legacy TINYINT(1) NOT NULL DEFAULT 0,
  version BIGINT NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  disabled_at DATETIME DEFAULT NULL,
  UNIQUE KEY uk_memory_bank_hindsight (hindsight_bank_id),
  KEY idx_memory_bank_scope (scope_type, scope_id, status),
  CONSTRAINT fk_memory_bank_policy FOREIGN KEY (policy_version_id) REFERENCES memory_policy_version(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS memory_bank_provisioning_intent (
  id VARCHAR(40) PRIMARY KEY,
  bank_id VARCHAR(40) NOT NULL,
  operation ENUM('provision', 'disable') NOT NULL,
  canonical_key VARCHAR(255) NOT NULL,
  bank_version BIGINT NOT NULL,
  payload_json JSON NOT NULL,
  status ENUM('pending', 'processing', 'completed', 'failed') NOT NULL DEFAULT 'pending',
  attempt_count BIGINT NOT NULL DEFAULT 0,
  last_error VARCHAR(500) DEFAULT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  completed_at DATETIME DEFAULT NULL,
  UNIQUE KEY uk_memory_bank_intent_canonical (canonical_key),
  UNIQUE KEY uk_memory_bank_intent_lifecycle (bank_id, operation, bank_version),
  KEY idx_memory_bank_intent_status (status, created_at),
  CONSTRAINT fk_memory_bank_intent_bank FOREIGN KEY (bank_id) REFERENCES memory_bank(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS memory_grant (
  id VARCHAR(40) PRIMARY KEY,
  principal_type ENUM('agent', 'space_role', 'user') NOT NULL,
  principal_id VARCHAR(40) NOT NULL,
  bank_id VARCHAR(40) NOT NULL,
  capability ENUM('recall', 'propose', 'curate', 'admin') NOT NULL,
  effect ENUM('allow', 'deny') NOT NULL,
  valid_from DATETIME DEFAULT NULL,
  valid_until DATETIME DEFAULT NULL,
  issued_by_uid VARCHAR(40) NOT NULL,
  reason VARCHAR(500) NOT NULL DEFAULT '',
  policy_version_id VARCHAR(40) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  revoked_at DATETIME DEFAULT NULL,
  KEY idx_memory_grant_principal (principal_type, principal_id, revoked_at),
  KEY idx_memory_grant_bank (bank_id, capability, effect, revoked_at),
  CONSTRAINT fk_memory_grant_bank FOREIGN KEY (bank_id) REFERENCES memory_bank(id),
  CONSTRAINT fk_memory_grant_policy FOREIGN KEY (policy_version_id) REFERENCES memory_policy_version(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS governance_run (
  id VARCHAR(40) PRIMARY KEY,
  mode ENUM('dry_run', 'apply') NOT NULL,
  scope_type VARCHAR(30) NOT NULL,
  scope_id VARCHAR(40) NOT NULL,
  policy_version_id VARCHAR(40) NOT NULL,
  status ENUM('queued', 'running', 'completed', 'failed', 'rolled_back') NOT NULL DEFAULT 'queued',
  idempotency_key VARCHAR(120) NOT NULL,
  rollback_idempotency_key VARCHAR(120) DEFAULT NULL,
  proposed_count BIGINT NOT NULL DEFAULT 0,
  applied_count BIGINT NOT NULL DEFAULT 0,
  failed_count BIGINT NOT NULL DEFAULT 0,
  version BIGINT NOT NULL DEFAULT 0,
  started_at DATETIME DEFAULT NULL,
  finished_at DATETIME DEFAULT NULL,
  created_by_uid VARCHAR(40) NOT NULL,
  UNIQUE KEY uk_governance_run_idempotency (idempotency_key),
  UNIQUE KEY uk_governance_rollback_idempotency (rollback_idempotency_key),
  KEY idx_governance_run_scope (scope_type, scope_id, status),
  CONSTRAINT fk_governance_run_policy FOREIGN KEY (policy_version_id) REFERENCES memory_policy_version(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS knowledge_item (
  id VARCHAR(40) PRIMARY KEY,
  bank_id VARCHAR(40) NOT NULL,
  hindsight_memory_id VARCHAR(120) DEFAULT NULL,
  idempotency_key VARCHAR(120) NOT NULL,
  canonical_key CHAR(64) NOT NULL,
  state ENUM('candidate', 'persisting', 'tentative', 'corroborated', 'verified', 'disputed', 'stale', 'superseded', 'revoked', 'deleted') NOT NULL,
  persistence_status ENUM('persisting', 'stored', 'retryable_failed', 'nonretryable_failed', 'reconciliation_required', 'compensated') NOT NULL DEFAULT 'persisting',
  lifecycle_state ENUM('tentative', 'corroborated', 'verified') NOT NULL,
  fact_type ENUM('world', 'experience', 'opinion', 'preference', 'procedure') NOT NULL,
  authenticated_source_agent_uid VARCHAR(40) NOT NULL,
  owner_uid VARCHAR(40) NOT NULL,
  space_id VARCHAR(40) DEFAULT NULL,
  credential_epoch BIGINT NOT NULL,
  source_instance_id VARCHAR(80) DEFAULT NULL,
  assertion_kind ENUM('firsthand', 'relay', 'derived') NOT NULL,
  origin_fingerprint CHAR(64) NOT NULL,
  confidence DECIMAL(4,3) NOT NULL,
  sensitivity ENUM('internal', 'confidential', 'restricted') NOT NULL DEFAULT 'internal',
  valid_from DATETIME DEFAULT NULL,
  valid_until DATETIME DEFAULT NULL,
  review_after DATETIME DEFAULT NULL,
  policy_version_id VARCHAR(40) NOT NULL,
  gate_run_id VARCHAR(40) DEFAULT NULL,
  content_hash CHAR(64) NOT NULL,
  failure_reason_code VARCHAR(80) NOT NULL DEFAULT '',
  version BIGINT NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uk_knowledge_item_bank_idempotency (bank_id, idempotency_key),
  UNIQUE KEY uk_knowledge_item_bank_canonical (bank_id, canonical_key),
  UNIQUE KEY uk_knowledge_item_hindsight (hindsight_memory_id),
  KEY idx_knowledge_item_state (bank_id, state, review_after),
  CONSTRAINT fk_knowledge_item_bank FOREIGN KEY (bank_id) REFERENCES memory_bank(id),
  CONSTRAINT fk_knowledge_item_policy FOREIGN KEY (policy_version_id) REFERENCES memory_policy_version(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS knowledge_evidence (
  id VARCHAR(40) PRIMARY KEY,
  knowledge_item_id VARCHAR(40) NOT NULL,
  evidence_item_id VARCHAR(40) DEFAULT NULL,
  source_agent_uid VARCHAR(40) NOT NULL,
  origin_fingerprint CHAR(64) NOT NULL,
  relation ENUM('supports', 'contradicts', 'duplicates', 'supersedes', 'derived_from') NOT NULL,
  independent TINYINT(1) NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY uk_knowledge_evidence_origin (knowledge_item_id, origin_fingerprint, relation),
  KEY idx_knowledge_evidence_item (evidence_item_id),
  CONSTRAINT fk_knowledge_evidence_knowledge FOREIGN KEY (knowledge_item_id) REFERENCES knowledge_item(id),
  CONSTRAINT fk_knowledge_evidence_item FOREIGN KEY (evidence_item_id) REFERENCES knowledge_item(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS knowledge_transition (
  id VARCHAR(40) PRIMARY KEY,
  knowledge_item_id VARCHAR(40) NOT NULL,
  from_state VARCHAR(30) NOT NULL,
  to_state VARCHAR(30) NOT NULL,
  actor_type ENUM('agent', 'user', 'governance_job', 'migration') NOT NULL,
  actor_id VARCHAR(40) NOT NULL,
  reason_code VARCHAR(80) NOT NULL,
  reason_redacted VARCHAR(500) NOT NULL DEFAULT '',
  policy_version_id VARCHAR(40) NOT NULL,
  governance_run_id VARCHAR(40) DEFAULT NULL,
  rollback_of VARCHAR(40) DEFAULT NULL,
  idempotency_key VARCHAR(120) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY uk_knowledge_transition_idempotency (idempotency_key),
  KEY idx_knowledge_transition_item (knowledge_item_id, created_at),
  CONSTRAINT fk_knowledge_transition_item FOREIGN KEY (knowledge_item_id) REFERENCES knowledge_item(id),
  CONSTRAINT fk_knowledge_transition_policy FOREIGN KEY (policy_version_id) REFERENCES memory_policy_version(id),
  CONSTRAINT fk_knowledge_transition_run FOREIGN KEY (governance_run_id) REFERENCES governance_run(id),
  CONSTRAINT fk_knowledge_transition_rollback FOREIGN KEY (rollback_of) REFERENCES knowledge_transition(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS memory_audit_event (
  id VARCHAR(40) PRIMARY KEY,
  event_type VARCHAR(80) NOT NULL,
  principal_agent_uid VARCHAR(40) DEFAULT NULL,
  principal_user_uid VARCHAR(40) DEFAULT NULL,
  space_id VARCHAR(40) DEFAULT NULL,
  resource_type VARCHAR(40) NOT NULL,
  resource_id VARCHAR(120) NOT NULL,
  capability VARCHAR(30) DEFAULT NULL,
  decision VARCHAR(30) NOT NULL,
  reason_code VARCHAR(80) NOT NULL,
  request_id VARCHAR(80) DEFAULT NULL,
  credential_jti_hash CHAR(64) DEFAULT NULL,
  policy_version_id VARCHAR(40) DEFAULT NULL,
  content_hash CHAR(64) DEFAULT NULL,
  redacted_snippet VARCHAR(500) DEFAULT NULL,
  metadata_json JSON DEFAULT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_memory_audit_principal (principal_agent_uid, created_at),
  KEY idx_memory_audit_space (space_id, created_at),
  KEY idx_memory_audit_resource (resource_type, resource_id, created_at),
  CONSTRAINT fk_memory_audit_policy FOREIGN KEY (policy_version_id) REFERENCES memory_policy_version(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS legacy_bank_mapping (
  hindsight_bank_id VARCHAR(120) PRIMARY KEY,
  bank_id VARCHAR(40) DEFAULT NULL,
  classification ENUM('known_personal', 'known_shared', 'known_tentative', 'unknown') NOT NULL,
  owner_agent_uid VARCHAR(40) DEFAULT NULL,
  memory_count BIGINT NOT NULL DEFAULT 0,
  inventory_hash CHAR(64) NOT NULL,
  status ENUM('inventoried', 'mapped', 'restricted', 'reconciled') NOT NULL DEFAULT 'inventoried',
  inventoried_at DATETIME NOT NULL,
  reconciled_at DATETIME DEFAULT NULL,
  KEY idx_legacy_bank_mapping_status (status),
  CONSTRAINT fk_legacy_bank_mapping_bank FOREIGN KEY (bank_id) REFERENCES memory_bank(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- +migrate Down
DROP TABLE IF EXISTS legacy_bank_mapping;
DROP TABLE IF EXISTS memory_audit_event;
DROP TABLE IF EXISTS knowledge_transition;
DROP TABLE IF EXISTS knowledge_evidence;
DROP TABLE IF EXISTS knowledge_item;
DROP TABLE IF EXISTS governance_run;
DROP TABLE IF EXISTS memory_grant;
DROP TABLE IF EXISTS memory_bank_provisioning_intent;
DROP TABLE IF EXISTS memory_bank;
DROP TABLE IF EXISTS agent_memory_profile;
DROP TABLE IF EXISTS memory_policy_version;
