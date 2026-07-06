package usage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisRuntimeJob struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	EnqueuedAt time.Time       `json:"enqueued_at"`
}

func AcquireRedisRuntimeLock(ctx context.Context, name, owner string, ttl time.Duration) (bool, error) {
	client := redisClient
	if client == nil {
		return false, errors.New("redis runtime lock: redis is not initialized")
	}
	name = strings.TrimSpace(name)
	owner = strings.TrimSpace(owner)
	if name == "" || owner == "" {
		return false, errors.New("redis runtime lock: name and owner are required")
	}
	if ttl <= 0 {
		return false, errors.New("redis runtime lock: ttl must be positive")
	}
	status, err := client.SetArgs(ctx, "cliproxy:lock:"+name, owner, redis.SetArgs{
		Mode: "NX",
		TTL:  ttl,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	return status == "OK", err
}

func ReleaseRedisRuntimeLock(ctx context.Context, name, owner string) (bool, error) {
	client := redisClient
	if client == nil {
		return false, errors.New("redis runtime lock: redis is not initialized")
	}
	name = strings.TrimSpace(name)
	owner = strings.TrimSpace(owner)
	if name == "" || owner == "" {
		return false, errors.New("redis runtime lock: name and owner are required")
	}
	const compareDelete = `
if redis.call("get", KEYS[1]) == ARGV[1] then
  return redis.call("del", KEYS[1])
end
return 0
`
	deleted, err := client.Eval(ctx, compareDelete, []string{"cliproxy:lock:" + name}, owner).Int()
	return deleted == 1, err
}

func EnqueueRedisRuntimeJob(ctx context.Context, queue string, job RedisRuntimeJob) error {
	client := redisClient
	if client == nil {
		return errors.New("redis runtime queue: redis is not initialized")
	}
	queue = strings.TrimSpace(queue)
	if queue == "" {
		return errors.New("redis runtime queue: queue is required")
	}
	if strings.TrimSpace(job.ID) == "" || strings.TrimSpace(job.Kind) == "" {
		return errors.New("redis runtime queue: job id and kind are required")
	}
	if job.EnqueuedAt.IsZero() {
		job.EnqueuedAt = time.Now().UTC()
	}
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	return client.RPush(ctx, "cliproxy:queue:"+queue, data).Err()
}

func DequeueRedisRuntimeJob(ctx context.Context, queue string, timeout time.Duration) (*RedisRuntimeJob, error) {
	client := redisClient
	if client == nil {
		return nil, errors.New("redis runtime queue: redis is not initialized")
	}
	queue = strings.TrimSpace(queue)
	if queue == "" {
		return nil, errors.New("redis runtime queue: queue is required")
	}
	result, err := client.BLPop(ctx, timeout, "cliproxy:queue:"+queue).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	if len(result) != 2 {
		return nil, errors.New("redis runtime queue: malformed pop result")
	}
	var job RedisRuntimeJob
	if err := json.Unmarshal([]byte(result[1]), &job); err != nil {
		return nil, err
	}
	return &job, nil
}
