DROP TABLE seckill_result;
ALTER TABLE seckill_voucher DROP INDEX idx_async_voucher, DROP CHECK chk_async_state,
 DROP COLUMN async_state, DROP COLUMN async_generation, DROP COLUMN async_capacity;
