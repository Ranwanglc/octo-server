-- +migrate Up
ALTER TABLE `conversation` ADD COLUMN `space_id` VARCHAR(40) DEFAULT '' COMMENT 'Space ID，空字符串表示个人空间';
CREATE INDEX `idx_conversation_space` ON `conversation`(`uid`, `space_id`);
ALTER TABLE `group` ADD COLUMN `space_id` VARCHAR(40) DEFAULT '' COMMENT 'Space ID';
