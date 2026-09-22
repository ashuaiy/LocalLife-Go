if redis.call('TYPE', KEYS[1]).ok ~= 'hash' or redis.call('TYPE', KEYS[2]).ok ~= 'stream' then return 0 end
local data = redis.call('HMGET', KEYS[1], 'version', 'generation', 'capacity', 'begin_ms', 'end_ms', 'stock')
if data[1] ~= '1' or data[2] ~= ARGV[1] or data[3] ~= ARGV[2] or data[4] ~= ARGV[3] or data[5] ~= ARGV[4] then return 0 end
local stock = data[6] and tonumber(data[6])
if not stock or stock < 0 or stock > tonumber(ARGV[2]) or stock ~= math.floor(stock) then return 0 end
if ARGV[5] == '1' and (data[6] ~= ARGV[2] or redis.call('XLEN', KEYS[2]) ~= 0 or redis.call('HLEN', KEYS[1]) ~= 6) then return 0 end
local groups = redis.call('XINFO', 'GROUPS', KEYS[2])
for _, group in ipairs(groups) do
    for i = 1, #group, 2 do
        if group[i] == 'name' and group[i + 1] == 'orders' then return 1 end
    end
end
return 0
