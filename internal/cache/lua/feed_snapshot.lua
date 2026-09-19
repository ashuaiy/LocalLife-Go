-- Capture membership, order and cardinality together. Empty snapshots still have metadata.
local count = redis.call('ZUNIONSTORE', KEYS[2], 1, KEYS[1])
redis.call('EXPIRE', KEYS[2], ARGV[1])
redis.call('SET', KEYS[3], count, 'EX', ARGV[1])
return count
