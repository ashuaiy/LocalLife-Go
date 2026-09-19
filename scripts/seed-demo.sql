-- Optional local demo data. Run after migrations; re-running does not overwrite existing shops.
SET NAMES utf8mb4;
START TRANSACTION;

INSERT INTO shop_type (name, icon, sort_order)
SELECT '示例·餐饮', '', 10
WHERE NOT EXISTS (SELECT 1 FROM shop_type WHERE name = '示例·餐饮');

INSERT INTO shop_type (name, icon, sort_order)
SELECT '示例·咖啡', '', 20
WHERE NOT EXISTS (SELECT 1 FROM shop_type WHERE name = '示例·咖啡');

SET @demo_food_type = (SELECT id FROM shop_type WHERE name = '示例·餐饮');
SET @demo_coffee_type = (SELECT id FROM shop_type WHERE name = '示例·咖啡');

INSERT INTO shop (type_id, name, address, longitude, latitude)
SELECT @demo_food_type, '示例·街角面馆', '上海市示例路 1 号（虚构商户）', 121.4737012, 31.2304001
WHERE NOT EXISTS (SELECT 1 FROM shop WHERE type_id = @demo_food_type AND name = '示例·街角面馆');

INSERT INTO shop (type_id, name, address, longitude, latitude)
SELECT @demo_food_type, '示例·家常菜馆', '上海市示例路 2 号（虚构商户）', 121.4785000, 31.2320000
WHERE NOT EXISTS (SELECT 1 FROM shop WHERE type_id = @demo_food_type AND name = '示例·家常菜馆');

INSERT INTO shop (type_id, name, address, longitude, latitude)
SELECT @demo_coffee_type, '示例·河畔咖啡', '上海市示例路 3 号（虚构商户）', 121.4830000, 31.2350000
WHERE NOT EXISTS (SELECT 1 FROM shop WHERE type_id = @demo_coffee_type AND name = '示例·河畔咖啡');

COMMIT;
