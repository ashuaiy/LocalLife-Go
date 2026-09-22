-- Atomic admission does not mean a committed MySQL order. IDs remain strings.
if redis.call('TYPE', KEYS[1]).ok ~= 'hash' or redis.call('TYPE', KEYS[2]).ok ~= 'stream' then
    return {'dependency_failure'}
end
local data = redis.call('HMGET', KEYS[1], 'version', 'generation', 'stock', 'capacity', 'begin_ms', 'end_ms')
if ARGV[3] and data[2] ~= ARGV[3] then return {'dependency_failure'} end
if ARGV[3] then
    local found = false
    for _, group in ipairs(redis.call('XINFO', 'GROUPS', KEYS[2])) do
        for i = 1, #group, 2 do
            if group[i] == 'name' and group[i + 1] == 'orders' then found = true end
        end
    end
    if not found then return {'dependency_failure'} end
end
local function integer(raw)
    if not raw or not string.match(raw, '^%d+$') then return nil end
    local n = tonumber(raw)
    if not n or n > 9007199254740991 then return nil end
    return n
end
local stock, capacity, begin_ms, end_ms = integer(data[3]), integer(data[4]), integer(data[5]), integer(data[6])
if data[1] ~= '1' or not data[2] or #data[2] ~= 32 or not string.match(data[2], '^[0-9a-f]+$')
    or not stock or not capacity or stock > capacity or not begin_ms or not end_ms or end_ms <= begin_ms then
    return {'dependency_failure'}
end
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
if now < begin_ms then return {'activity_not_started'} end
if now >= end_ms then return {'activity_ended'} end
local buyer = 'buyer:' .. ARGV[2]
if redis.call('HEXISTS', KEYS[1], buyer) == 1 then return {'already_purchased'} end
if stock == 0 then return {'sold_out'} end
local stamp = string.format('%.0f', now)
-- Validate permissions before the first mutation. XADD precedes HSET so a
-- rejected enqueue (wrong type, exhausted IDs, ACL, etc.) cannot consume stock.
if not redis.acl_check_cmd('XADD', KEYS[2], '*', 'voucher_id', ARGV[1], 'user_id', ARGV[2], 'generation', data[2], 'accepted_ms', stamp)
    or not redis.acl_check_cmd('HSET', KEYS[1], 'stock', '0', buyer, '0-0')
    or not redis.acl_check_cmd('XDEL', KEYS[2], '0-0') then
    return {'dependency_failure'}
end
local event = redis.pcall('XADD', KEYS[2], '*', 'voucher_id', ARGV[1], 'user_id', ARGV[2], 'generation', data[2], 'accepted_ms', stamp)
if type(event) == 'table' and event.err then return {'dependency_failure'} end
local written = redis.pcall('HSET', KEYS[1], 'stock', string.format('%.0f', stock - 1), buyer, event)
if type(written) == 'table' and written.err then
    redis.call('XDEL', KEYS[2], event)
    return {'dependency_failure'}
end
return {'accepted', event, data[2], stamp}
