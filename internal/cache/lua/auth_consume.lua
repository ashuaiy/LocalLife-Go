local digest = redis.call('HGET', KEYS[1], 'digest')
if not digest then
    return 0
end
if digest ~= ARGV[1] then
    local remaining = redis.call('HINCRBY', KEYS[1], 'attempts', -1)
    if remaining <= 0 then
        redis.call('DEL', KEYS[1])
    end
    return 0
end
redis.call('DEL', KEYS[1])
return 1
