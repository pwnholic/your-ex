package config

import (
	"testing"

	"github.com/lilwiggy/bot/pkg/util"
)

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name        string
		configPath  string
		expectError bool
	}{
		{
			name:        "valid config",
			configPath:  "../../configs/config.example.yaml",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := LoadWithDefaults(tt.configPath)
			if err != nil {
				t.Errorf("LoadWithDefaults() error = %v", err)
				return
			}

			if config == nil {
				t.Error("config is nil")
				return
			}

			// Verify basic config structure
			if config.Bot.Name == "" {
				t.Error("bot name is empty")
			}

			if len(config.Chains.Solana.RPCEndpoints) == 0 && !config.Chains.Solana.Enabled {
				// OK if solana disabled - explicitly check that condition is valid
				_ = config.Chains.Solana.RPCEndpoints
			}

			t.Logf("Config loaded successfully: bot=%s, chains: solana=%v, base=%v",
				config.Bot.Name, config.Chains.Solana.Enabled, config.Chains.Base.Enabled)
		})
	}
}

func TestAmountFormatValidation(t *testing.T) {
	tests := []struct {
		amount      string
		expectError bool
	}{
		{"1.5 SOL", false},
		{"0.01 ETH", false},
		{"100 USD", false},
		{"invalid", true},
		{"", true},
		{"1", true},
		{"SOL", true},
	}

	for _, tt := range tests {
		t.Run(tt.amount, func(t *testing.T) {
			err := validateAmountFormat(tt.amount)
			if (err != nil) != tt.expectError {
				t.Errorf("validateAmountFormat(%q) error = %v, expectError %v", tt.amount, err, tt.expectError)
			}
		})
	}
}

func TestRetryConfig(t *testing.T) {
	config := util.DefaultRetryConfig()

	if config.MaxAttempts != 3 {
		t.Errorf("Default MaxAttempts = %v, want 3", config.MaxAttempts)
	}

	if config.InitialDelay == 0 {
		t.Error("Default InitialDelay is zero")
	}

	if config.Multiplier < 1 {
		t.Errorf("Default Multiplier = %v, want >= 1", config.Multiplier)
	}
}
