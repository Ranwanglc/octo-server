-- +migrate Up

-- Flow 定义
CREATE TABLE `flows` (
    `id`          VARCHAR(40)  NOT NULL,
    `space_id`    VARCHAR(40)  NOT NULL DEFAULT '',
    `name`        VARCHAR(255) NOT NULL DEFAULT '',
    `description` MEDIUMTEXT,
    `definition`  JSON         NOT NULL,
    `version`     INT          NOT NULL DEFAULT 1,
    `status`      VARCHAR(20)  NOT NULL DEFAULT 'draft', -- draft | active | archived
    `created_by`  VARCHAR(40)  NOT NULL DEFAULT '',
    `created_at`  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    `updated_at`  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (`id`),
    INDEX `idx_flows_space` (`space_id`),
    INDEX `idx_flows_status` (`status`)
);

-- Flow 版本历史
CREATE TABLE `flow_versions` (
    `id`          VARCHAR(40)  NOT NULL,
    `flow_id`     VARCHAR(40)  NOT NULL DEFAULT '',
    `version`     INT          NOT NULL DEFAULT 0,
    `definition`  JSON         NOT NULL,
    `changelog`   MEDIUMTEXT,
    `created_by`  VARCHAR(40)  NOT NULL DEFAULT '',
    `created_at`  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (`id`),
    INDEX `idx_flow_versions_flow` (`flow_id`, `version`)
);

-- 触发器注册（运行时查询）
CREATE TABLE `flow_triggers` (
    `id`           VARCHAR(40)  NOT NULL,
    `flow_id`      VARCHAR(40)  NOT NULL DEFAULT '',
    `type`         VARCHAR(20)  NOT NULL DEFAULT '', -- webhook | cron | manual
    `config`       JSON         NOT NULL,
    `webhook_path` VARCHAR(255) NOT NULL DEFAULT '', -- webhook 类型的 URL path
    `status`       VARCHAR(20)  NOT NULL DEFAULT 'active',
    `created_at`   TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    `updated_at`   TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_flow_triggers_webhook_path` (`webhook_path`),
    INDEX `idx_flow_triggers_flow` (`flow_id`),
    INDEX `idx_flow_triggers_type` (`type`)
);

-- 执行实例
CREATE TABLE `flow_executions` (
    `id`          VARCHAR(40)  NOT NULL,
    `flow_id`     VARCHAR(40)  NOT NULL DEFAULT '',
    `trigger_id`  VARCHAR(40)  NOT NULL DEFAULT '',
    `status`      VARCHAR(20)  NOT NULL DEFAULT 'pending', -- pending | running | waiting | success | failed | cancelled
    `context`     JSON         NOT NULL,
    `scope_key`   VARCHAR(255) NOT NULL DEFAULT '', -- 并发控制 key
    `started_at`  TIMESTAMP    NULL DEFAULT NULL,
    `finished_at` TIMESTAMP    NULL DEFAULT NULL,
    `error`       MEDIUMTEXT,
    `created_at`  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    `updated_at`  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (`id`),
    INDEX `idx_flow_executions_flow` (`flow_id`),
    INDEX `idx_flow_executions_status` (`status`),
    INDEX `idx_flow_executions_scope` (`flow_id`, `scope_key`, `status`),
    INDEX `idx_flow_executions_created` (`created_at`)
);

-- 节点执行记录
CREATE TABLE `flow_node_executions` (
    `id`            VARCHAR(40)  NOT NULL,
    `execution_id`  VARCHAR(40)  NOT NULL DEFAULT '',
    `node_id`       VARCHAR(100) NOT NULL DEFAULT '',
    `node_type`     VARCHAR(30)  NOT NULL DEFAULT '',
    `status`        VARCHAR(20)  NOT NULL DEFAULT '',
    `input`         JSON,
    `output`        JSON,
    `error`         MEDIUMTEXT,
    `started_at`    TIMESTAMP    NULL DEFAULT NULL,
    `finished_at`   TIMESTAMP    NULL DEFAULT NULL,
    `created_at`    TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (`id`),
    INDEX `idx_flow_node_exec_execution` (`execution_id`),
    INDEX `idx_flow_node_exec_node` (`execution_id`, `node_id`)
);
