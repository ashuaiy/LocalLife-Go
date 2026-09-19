if redis.call('TYPE', KEYS[1]).ok ~= 'hash' or redis.call('TYPE', KEYS[2]).ok ~= 'stream' then
    return {'dependency_failure'}
end
local id = redis.call('HGET', KEYS[1], 'buyer:' .. ARGV[2])
if not id then return {'not_found'} end
if not string.match(id, '^%d+%-%d+$') then return {'dependency_failure'} end
local entries = redis.pcall('XRANGE', KEYS[2], id, id)
if entries.err or #entries ~= 1 then return {'dependency_failure'} end
local fields = entries[1][2]
if #fields ~= 8 then return {'dependency_failure'} end
local data = {}
for i = 1, #fields, 2 do
    if data[fields[i]] then return {'dependency_failure'} end
    data[fields[i]] = fields[i+1]
end
if data.voucher_id ~= ARGV[1] or data.user_id ~= ARGV[2]
    or data.generation ~= redis.call('HGET', KEYS[1], 'generation') or not data.accepted_ms then
    return {'dependency_failure'}
end
return {'accepted', id, data.generation, data.accepted_ms}
