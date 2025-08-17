package worker

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

type ResultsHandler struct {
	redisClient *redis.Client
}

const updateResultsLuaScript = `
local counterKeyPrefix = KEYS[1]
local amountKeyPrefix = KEYS[2]
local timestamp = tonumber(ARGV[1])
local amountValue = tonumber(ARGV[2])
local incrementVal = tonumber(ARGV[3])

local counterKey = counterKeyPrefix .. ":" .. timestamp
local amountKey = amountKeyPrefix .. ":" .. timestamp

local newAmount = redis.call('INCRBYFLOAT', amountKey, amountValue)
local newCount = redis.call('INCRBY', counterKey, incrementVal)

return {newAmount, newCount}
`

func NewResultsHandler(rc *redis.Client) *ResultsHandler {
	return &ResultsHandler{
		redisClient: rc,
	}
}

func (rh *ResultsHandler) updateResults(ctx context.Context, CounterKeyPrefix string, amountKeyPrefix string, timestamp int64, timeSeriesValue float64) error {
	if err := rh.redisClient.Eval(ctx, updateResultsLuaScript,
		[]string{CounterKeyPrefix, amountKeyPrefix},
		timestamp,
		timeSeriesValue,
		1, // incrementa 1 no contador
	).Err(); err != nil {
		return err
	}

	return nil
}

func (rh *ResultsHandler) ResultsCountByRangePipe(pipe redis.Pipeliner, ctx context.Context, counterKeyPrefix string, start, end int64) *redis.SliceCmd {
	keys := rh.getBucketKeysInRange(counterKeyPrefix, start, end)

	return pipe.MGet(ctx, keys...)
}

// Calcula todas as chaves entre start e end
func (rh *ResultsHandler) getBucketKeysInRange(counterKeyPrefix string, start, end int64) []string {
	keys := []string{}

	for bucket := start; bucket <= end; bucket++ {
		keys = append(keys, fmt.Sprintf("%s:%d", counterKeyPrefix, bucket))
	}

	return keys
}
