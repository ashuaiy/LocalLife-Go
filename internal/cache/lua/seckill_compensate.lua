-- Final SQL failure must be committed BEFORE calling this script. Keep buyer
-- deduplication and the event, so retries cannot reserve again or lose their receipt.
if redis.call('TYPE', KEYS[1]).ok ~= 'hash' then return 0 end
local data = redis.call('HMGET', KEYS[1], 'generation', 'stock', 'capacity', 'buyer:' .. ARGV[1], 'compensated:' .. ARGV[2])
if data[1] ~= ARGV[3] or data[4] ~= ARGV[2] then return 0 end
if data[5] == '1' then return 1 end
local stock, capacity = tonumber(data[2]), tonumber(data[3])
if not stock or not capacity or stock < 0 or stock >= capacity or capacity > 9007199254740991 or stock ~= math.floor(stock) or capacity ~= math.floor(capacity) then return 0 end
redis.call('HSET', KEYS[1], 'stock', string.format('%.0f', stock + 1), 'compensated:' .. ARGV[2], '1')
return 1
