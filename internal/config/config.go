// Package config provides configuration management for cccli
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config holds all configuration for cccli
type Config struct {
	// Chain configuration
	ChainID string `mapstructure:"chain_id"`
	HomeDir string `mapstructure:"home_dir"`
	Denom   string `mapstructure:"denom"`

	// Miner configuration
	HeartbeatInterval int    `mapstructure:"heartbeat_interval"`
	PollInterval      int    `mapstructure:"poll_interval"`
	HeartbeatMode     string `mapstructure:"heartbeat_mode"`
	// LLM configuration
	LLMProvider   string `mapstructure:"llm_provider"` // "openai" (default) or "anthropic"
	LLMAPIBaseURL string `mapstructure:"llm_api_base_url"`
	LLMAPIKey     string `mapstructure:"llm_api_key"`
	LLMModel      string `mapstructure:"llm_model"`
	LLMMaxTokens  int    `mapstructure:"llm_max_tokens"` // max tokens for LLM response (default 4096)
	LLMThinking   bool   `mapstructure:"llm_thinking"`   // enable thinking/reasoning mode (default false)

	// Transaction configuration
	Gas      uint64 `mapstructure:"gas"`
	GasPrice string `mapstructure:"gas_price"`

	// REST API configuration
	RESTURL string `mapstructure:"rest_url"`

	// Human wallet (auto-sweep)
	SweepTo        string `mapstructure:"sweep_to"`        // 人类钱包地址（EVM 0x... 或 Cosmos cc1...）
	SweepThreshold int    `mapstructure:"sweep_threshold"` // 触发转账的余额阈值（CC 单位），默认 64
	SweepKeep      int    `mapstructure:"sweep_keep"`      // 转账后保留的余额（CC 单位），默认 32
}

// DefaultConfig returns a Config with default values
func DefaultConfig() *Config {
	homeDir := os.Getenv("HOME")
	if homeDir == "" {
		homeDir = os.Getenv("USERPROFILE") // Windows
	}

	return &Config{
		ChainID: "ccbc-1",
		HomeDir: filepath.Join(homeDir, ".cc_bc"),
		Denom:   "acc",

		HeartbeatInterval: 120,
		PollInterval:      30,
		HeartbeatMode:     "auto",
		LLMAPIBaseURL:     "http://localhost:4000/v1",
		LLMAPIKey:         "",
		LLMModel:          "gpt-4",
		LLMMaxTokens:      4096,

		Gas:      200000,
		GasPrice: "100000000",

		RESTURL: "http://localhost:1317",

		SweepTo:        "",
		SweepThreshold: 64,
		SweepKeep:      32,
	}
}

// Load loads configuration from file and environment variables
func Load(cfgFile string) (*Config, error) {
	cfg := DefaultConfig()

	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		// Only search in home directory; searching "." can accidentally
		// pick up the cccli binary or other non-YAML files.
		viper.SetConfigFile(filepath.Join(cfg.HomeDir, "cccli.yaml"))
	}

	// Environment variables
	viper.SetEnvPrefix("CCCLI")
	viper.AutomaticEnv()

	// Bind specific env vars
	_ = viper.BindEnv("chain_id", "CHAIN_ID")
	_ = viper.BindEnv("home_dir", "HOME_DIR")
	_ = viper.BindEnv("denom", "DENOM")
	_ = viper.BindEnv("llm_provider", "LLM_PROVIDER")
	_ = viper.BindEnv("llm_api_base_url", "LLM_API_BASE_URL")
	_ = viper.BindEnv("llm_api_key", "LLM_API_KEY")
	_ = viper.BindEnv("llm_model", "LLM_MODEL")
	_ = viper.BindEnv("llm_thinking", "LLM_THINKING")
	_ = viper.BindEnv("rest_url", "REST_API_URL")
	_ = viper.BindEnv("sweep_to", "SWEEP_TO")
	_ = viper.BindEnv("sweep_threshold", "SWEEP_THRESHOLD")
	_ = viper.BindEnv("sweep_keep", "SWEEP_KEEP")

	// Read config file if exists
	if err := viper.ReadInConfig(); err != nil {
		var configFileNotFoundError viper.ConfigFileNotFoundError
		if !errors.As(err, &configFileNotFoundError) {
			// Also tolerate missing directory (e.g. first run before config init)
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("error reading config file: %w", err)
			}
		}
	}

	if err := viper.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	return cfg, nil
}

// Save saves configuration to file, creating the parent directory if needed.
func (c *Config) Save(path string) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("create config directory: %w", err)
		}
	}
	viper.Set("chain_id", c.ChainID)
	viper.Set("home_dir", c.HomeDir)
	viper.Set("denom", c.Denom)
	viper.Set("heartbeat_interval", c.HeartbeatInterval)
	viper.Set("poll_interval", c.PollInterval)
	viper.Set("heartbeat_mode", c.HeartbeatMode)
	viper.Set("llm_provider", c.LLMProvider)
	viper.Set("llm_api_base_url", c.LLMAPIBaseURL)
	// LLM API key is NOT saved to config file for security.
	// Temporarily clear it before writing, then restore so runtime is unaffected.
	savedKey := viper.GetString("llm_api_key")
	viper.Set("llm_api_key", "")
	viper.Set("llm_model", c.LLMModel)
	viper.Set("llm_max_tokens", c.LLMMaxTokens)
	viper.Set("llm_thinking", c.LLMThinking)
	viper.Set("gas", c.Gas)
	viper.Set("gas_price", c.GasPrice)
	viper.Set("rest_url", c.RESTURL)
	viper.Set("sweep_to", c.SweepTo)
	viper.Set("sweep_threshold", c.SweepThreshold)
	viper.Set("sweep_keep", c.SweepKeep)

	err := viper.WriteConfigAs(path)
	// Restore the runtime value so the Config struct remains usable after Save.
	viper.Set("llm_api_key", savedKey)
	return err
}
