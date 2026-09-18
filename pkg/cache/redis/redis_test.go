package redis

import (
	"testing"
	"time"
)

func TestNewRedisConnectionReturnsErrorWhenRedisIsUnavailable(t *testing.T) {
	client, err := NewRedisConnection(ConnectionInfo{
		Addr:        "127.0.0.1:1",
		DB:          0,
		MaxRetries:  0,
		DialTimeout: 10 * time.Millisecond,
		Timeout:     10 * time.Millisecond,
	})
	if err == nil {
		Close(client)
		t.Fatal("expected connection error")
	}
	if client != nil {
		t.Fatal("client must be nil after failed ping")
	}
}
