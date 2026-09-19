-- Optional local voucher data. Run migrations and seed-demo.sql first.
-- Sequential re-runs do not reset existing stock or extend the activity window.
SET NAMES utf8mb4;
START TRANSACTION;

SET @voucher_demo_shop = (
    SELECT MIN(s.id) FROM shop s
    JOIN shop_type t ON t.id = s.type_id
    WHERE t.name = '示例·餐饮' AND s.name = '示例·街角面馆'
);

INSERT INTO voucher (shop_id, title, pay_value, actual_value)
SELECT @voucher_demo_shop, '示例·面馆代金券', 1900, 3000
WHERE NOT EXISTS (
    SELECT 1 FROM voucher WHERE shop_id = @voucher_demo_shop AND title = '示例·面馆代金券'
);

INSERT INTO voucher (shop_id, title, pay_value, actual_value)
SELECT @voucher_demo_shop, '示例·双人套餐秒杀', 9900, 15000
WHERE NOT EXISTS (
    SELECT 1 FROM voucher WHERE shop_id = @voucher_demo_shop AND title = '示例·双人套餐秒杀'
);

SET @voucher_demo_seckill = (
    SELECT MIN(id) FROM voucher WHERE shop_id = @voucher_demo_shop AND title = '示例·双人套餐秒杀'
);

INSERT INTO seckill_voucher (voucher_id, stock, begin_time, end_time)
SELECT @voucher_demo_seckill, 20, UTC_TIMESTAMP(3) - INTERVAL 1 DAY, UTC_TIMESTAMP(3) + INTERVAL 7 DAY
WHERE NOT EXISTS (SELECT 1 FROM seckill_voucher WHERE voucher_id = @voucher_demo_seckill);

COMMIT;
