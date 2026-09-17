-- Code and cooldown share one hash slot. Publishing a code and its resend limit is atomic.
if redis.call('EXISTS', KEYS[2]) == 1 then
    return 0
end
redis.call('HSET', KEYS[1], 'digest', ARGV[1], 'attempts', ARGV[4])
redis.call('PEXPIRE', KEYS[1], ARGV[2])
redis.call('SET', KEYS[2], '1', 'PX', ARGV[3])
return 1
