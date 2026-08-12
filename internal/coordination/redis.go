package coordination

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	DecisionAllow = iota
	DecisionRateLimited
	DecisionQuotaExceeded
)

type Event struct {
	Type string `json:"type"`
}

type Client struct {
	redis *redis.Client
}

func New(rawURL string) (*Client, error) {
	options, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, errors.New("parse Redis configuration")
	}
	options.DialTimeout = 2 * time.Second
	options.ReadTimeout = 2 * time.Second
	options.WriteTimeout = 2 * time.Second
	return &Client{redis: redis.NewClient(options)}, nil
}

func (c *Client) Close() error { return c.redis.Close() }

func (c *Client) Publish(ctx context.Context, environment, eventType string) error {
	payload, err := json.Marshal(Event{Type: eventType})
	if err != nil {
		return err
	}
	return c.redis.Publish(ctx, eventChannel(environment), payload).Err()
}

func (c *Client) Events(ctx context.Context, environment string) <-chan Event {
	output := make(chan Event, 16)
	go func() {
		defer close(output)
		subscription := c.redis.Subscribe(ctx, eventChannel(environment))
		defer subscription.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case message, ok := <-subscription.Channel():
				if !ok {
					return
				}
				var event Event
				if json.Unmarshal([]byte(message.Payload), &event) == nil && (event.Type == "configuration" || event.Type == "credentials") {
					select {
					case output <- event:
					default:
					}
				}
			}
		}
	}()
	return output
}

var requestPolicyScript = redis.NewScript(`
local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local quota = tonumber(ARGV[3])
if rate > 0 then
  local count = redis.call('INCR', KEYS[1])
  if count == 1 then redis.call('PEXPIRE', KEYS[1], 1000) end
  if count > rate + burst then return 1 end
end
if quota > 0 then
  local count = redis.call('INCR', KEYS[2])
  if count == 1 then redis.call('PEXPIRE', KEYS[2], 60000) end
  if count > quota then return 2 end
end
return 0
`)

func (c *Client) AllowRequest(ctx context.Context, environment, listenerID, consumerID string, rate, burst, quota int) (int, error) {
	now := time.Now().UTC()
	base := fmt.Sprintf("shinmon:%s:%s:%s", environment, listenerID, consumerID)
	keys := []string{fmt.Sprintf("%s:rate:%d", base, now.Unix()), fmt.Sprintf("%s:quota:%s", base, now.Format("200601021504"))}
	decision, err := requestPolicyScript.Run(ctx, c.redis, keys, rate, burst, quota).Int()
	if err != nil {
		return DecisionAllow, errors.New("distributed request policy unavailable")
	}
	return decision, nil
}

func (c *Client) CircuitOpen(ctx context.Context, environment, upstreamID string) (bool, error) {
	result, err := c.redis.Exists(ctx, circuitOpenKey(environment, upstreamID)).Result()
	if err != nil {
		return false, errors.New("distributed circuit state unavailable")
	}
	return result != 0, nil
}

var circuitFailureScript = redis.NewScript(`
local failures = redis.call('INCR', KEYS[1])
if failures == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[2]) end
if failures >= tonumber(ARGV[1]) then
  redis.call('SET', KEYS[2], '1', 'PX', ARGV[2])
  redis.call('DEL', KEYS[1])
end
return failures
`)

func (c *Client) RecordUpstream(ctx context.Context, environment, upstreamID string, success bool, threshold int, openFor time.Duration) error {
	if success {
		return c.redis.Del(ctx, circuitFailureKey(environment, upstreamID)).Err()
	}
	return circuitFailureScript.Run(ctx, c.redis, []string{circuitFailureKey(environment, upstreamID), circuitOpenKey(environment, upstreamID)}, threshold, openFor.Milliseconds()).Err()
}

func eventChannel(environment string) string { return "shinmon:" + environment + ":events" }
func circuitFailureKey(environment, upstreamID string) string {
	return "shinmon:" + environment + ":circuit:" + upstreamID + ":failures"
}
func circuitOpenKey(environment, upstreamID string) string {
	return "shinmon:" + environment + ":circuit:" + upstreamID + ":open"
}
