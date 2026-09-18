package redis

import (
	"context"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

type ConnectionInfo struct {
	Addr        string
	Password    string
	DB          int
	MaxRetries  int
	DialTimeout time.Duration
	Timeout     time.Duration
}

type Client = goredis.Client

func NewRedisConnection(info ConnectionInfo) (*Client, error) {
	opts := &goredis.Options{
		Addr:         info.Addr,
		Password:     info.Password,
		DB:           info.DB,
		MaxRetries:   info.MaxRetries,
		DialTimeout:  info.DialTimeout,
		ReadTimeout:  info.Timeout,
		WriteTimeout: info.Timeout,
	}

	rdb := goredis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), info.Timeout)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, err
	}

	return rdb, nil
}

func Close(c *Client) {
	if c == nil {
		return
	}
	_ = c.Close()
}
