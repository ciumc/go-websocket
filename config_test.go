package websocket

import (
	"testing"
	"time"
)

// TestConfigValidate 测试配置验证
func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name:    "默认配置有效",
			config:  DefaultConfig(),
			wantErr: false,
		},
		{
			name: "WriteWait 无效",
			config: Config{
				WriteWait:      0,
				PongWait:       60 * time.Second,
				PingPeriod:     54 * time.Second,
				MaxMessageSize: 512,
				BufSize:        256,
			},
			wantErr: true,
		},
		{
			name: "PongWait 无效",
			config: Config{
				WriteWait:      10 * time.Second,
				PongWait:       0,
				PingPeriod:     54 * time.Second,
				MaxMessageSize: 512,
				BufSize:        256,
			},
			wantErr: true,
		},
		{
			name: "PingPeriod 大于等于 PongWait",
			config: Config{
				WriteWait:      10 * time.Second,
				PongWait:       60 * time.Second,
				PingPeriod:     60 * time.Second,
				MaxMessageSize: 512,
				BufSize:        256,
			},
			wantErr: true,
		},
		{
			name: "MaxMessageSize 无效",
			config: Config{
				WriteWait:      10 * time.Second,
				PongWait:       60 * time.Second,
				PingPeriod:     54 * time.Second,
				MaxMessageSize: 0,
				BufSize:        256,
			},
			wantErr: true,
		},
		{
			name: "BufSize 为零有效",
			config: Config{
				WriteWait:      10 * time.Second,
				PongWait:       60 * time.Second,
				PingPeriod:     54 * time.Second,
				MaxMessageSize: 512,
				BufSize:        0,
			},
			wantErr: false,
		},
		{
			name: "BufSize 负数无效",
			config: Config{
				WriteWait:      10 * time.Second,
				PongWait:       60 * time.Second,
				PingPeriod:     54 * time.Second,
				MaxMessageSize: 512,
				BufSize:        -1,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
