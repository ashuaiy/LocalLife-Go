-- Keys share a category hash tag. Publish a complete index and its readiness marker atomically.
if tonumber(ARGV[1]) == 0 then
    redis.call('DEL', KEYS[1])
else
    if redis.call('ZCARD', KEYS[2]) ~= tonumber(ARGV[1]) then
        return redis.error_reply('GEO_BUILD_INCOMPLETE')
    end
    -- RENAME fails before touching the live index if the temporary key expired or was evicted.
    redis.call('RENAME', KEYS[2], KEYS[1])
    redis.call('PERSIST', KEYS[1])
end
redis.call('SET', KEYS[3], ARGV[1])
return 1
