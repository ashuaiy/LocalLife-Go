if tonumber(ARGV[1]) == 0 then
    redis.call('DEL', KEYS[1])
else
    if redis.call('ZCARD', KEYS[2]) ~= tonumber(ARGV[1]) then
        return redis.error_reply('FEED_BUILD_INCOMPLETE')
    end
    redis.call('RENAME', KEYS[2], KEYS[1])
    redis.call('PERSIST', KEYS[1])
end
return 1
