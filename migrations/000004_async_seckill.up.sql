ALTER TABLE seckill_voucher
 ADD COLUMN async_state TINYINT UNSIGNED NOT NULL DEFAULT 0,
 ADD COLUMN async_generation CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
 ADD COLUMN async_capacity INT UNSIGNED NOT NULL DEFAULT 0,
 ADD CONSTRAINT chk_async_state CHECK (async_state IN (0,1,2)),
 ADD INDEX idx_async_voucher (async_state,voucher_id);

CREATE TABLE seckill_result (
 voucher_id BIGINT UNSIGNED NOT NULL,
 event_id VARCHAR(41) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 generation CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 user_id BIGINT UNSIGNED NOT NULL,
 order_id BIGINT UNSIGNED NULL,
 status VARCHAR(16) NOT NULL,
 reason VARCHAR(64) NOT NULL DEFAULT '',
 accepted_at DATETIME(3) NOT NULL,
 created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
 PRIMARY KEY (voucher_id,event_id),
 UNIQUE KEY uk_seckill_user (voucher_id,generation,user_id),
 CONSTRAINT fk_result_voucher FOREIGN KEY (voucher_id) REFERENCES voucher(id),
 CONSTRAINT chk_result_status CHECK (status IN ('created','failed'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
