-- Both keys belong to one activity hash slot. Never reset an existing key,
-- including a partial snapshot that requires operator-led recovery.
if redis.call('EXISTS', KEYS[1], KEYS[2]) ~= 0 then return 'conflict' end
if not redis.acl_check_cmd('XGROUP', 'CREATE', KEYS[2], 'orders', '0', 'MKSTREAM')
    or not redis.acl_check_cmd('HSET', KEYS[1], 'version', '1')
    or not redis.acl_check_cmd('DEL', KEYS[2]) then
    return 'dependency_failure'
end
local group = redis.pcall('XGROUP', 'CREATE', KEYS[2], 'orders', '0', 'MKSTREAM')
if type(group) == 'table' and group.err then return 'dependency_failure' end
local state = redis.pcall('HSET', KEYS[1],
    'version', '1', 'generation', ARGV[1], 'stock', ARGV[2], 'capacity', ARGV[2],
    'begin_ms', ARGV[3], 'end_ms', ARGV[4])
if type(state) == 'table' and state.err then
    redis.call('DEL', KEYS[2])
    return 'dependency_failure'
end
return 'ok'
