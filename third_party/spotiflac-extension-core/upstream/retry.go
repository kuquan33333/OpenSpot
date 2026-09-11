package gobackend

import (
	"context"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

const DownloadTimeout = 24 * time.Hour

type RetryConfig struct {
	MaxRetries    int
	InitialDelay  time.Duration
	MaxDelay      time.Duration
	BackoffFactor float64
}

func sleepRetry(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

var jitterFloat = rand.Float64

func calculateNextDelay(currentDelay time.Duration, config RetryConfig) time.Duration {
	nextDelay := time.Duration(float64(currentDelay) * config.BackoffFactor)
	capped := min(nextDelay, config.MaxDelay)
	if capped <= config.InitialDelay {
		return capped
	}
	span := capped - config.InitialDelay
	return config.InitialDelay + time.Duration(jitterFloat()*float64(span))
}

const maxRetryAfterDelay = 2 * time.Minute

func getRetryAfterDuration(resp *http.Response) time.Duration {
	retryAfter := resp.Header.Get("Retry-After")
	if retryAfter == "" {
		return 0
	}

	if seconds, err := strconv.Atoi(retryAfter); err == nil {
		return min(time.Duration(seconds)*time.Second, maxRetryAfterDelay)
	}

	if t, err := http.ParseTime(retryAfter); err == nil {
		duration := time.Until(t)
		if duration > 0 {
			return min(duration, maxRetryAfterDelay)
		}
	}

	return 0
}
