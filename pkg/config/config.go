// Package config 提供 viper 配置加载，支持 YAML 文件 + 环境变量覆盖。
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config 是应用配置的顶层结构。
type Config struct {
	Server    ServerConfig    `mapstructure:"server"`
	JWT       JWTConfig       `mapstructure:"jwt"`
	Storage   StorageConfig   `mapstructure:"storage"`
	Redis     RedisConfig     `mapstructure:"redis"`
	MySQL     MySQLConfig     `mapstructure:"mysql"`
	Features  FeaturesConfig  `mapstructure:"features"`
	Reconcile ReconcileConfig `mapstructure:"reconcile"`
	RateLimit RateLimitConfig `mapstructure:"rate_limit"`
	OSS       OSSConfig       `mapstructure:"oss"`
}

type ServerConfig struct {
	Addr         string        `mapstructure:"addr"`
	Mode         string        `mapstructure:"mode"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
}

type JWTConfig struct {
	Secret string        `mapstructure:"secret"`
	TTL    time.Duration `mapstructure:"ttl"`
}

type StorageConfig struct {
	UploadDir     string `mapstructure:"upload_dir"`
	MaxUploadSize int64  `mapstructure:"max_upload_size"`
}

type RedisConfig struct {
	Addr string `mapstructure:"addr"`
}

type MySQLConfig struct {
	DSN          string `mapstructure:"dsn"`
	MaxOpenConns int    `mapstructure:"max_open_conns"`
	MaxIdleConns int    `mapstructure:"max_idle_conns"`
}

type FeaturesConfig struct {
	Seed              bool `mapstructure:"seed"`
	MQEnabled         bool `mapstructure:"mq_enabled"`
	TranscodeEnabled  bool `mapstructure:"transcode_enabled"`
	ReconcileEnabled  bool `mapstructure:"reconcile_enabled"`
	RecommendEnabled  bool `mapstructure:"recommend_enabled"`
}

type ReconcileConfig struct {
	Threshold int64         `mapstructure:"threshold"`
	Interval  time.Duration `mapstructure:"interval"`
}

// RateLimitRule 单个接口的限流规则。
type RateLimitRule struct {
	QPS   int `mapstructure:"qps"`
	Burst int `mapstructure:"burst"`
}

type RateLimitConfig struct {
	Login    RateLimitRule `mapstructure:"login"`
	Upload   RateLimitRule `mapstructure:"upload"`
	Video    RateLimitRule `mapstructure:"video"`
	Feed     RateLimitRule `mapstructure:"feed"`
	User     RateLimitRule `mapstructure:"user"`
	Fallback RateLimitRule `mapstructure:"fallback"`
}

type OSSConfig struct {
	Endpoint  string `mapstructure:"endpoint"`
	AccessKey string `mapstructure:"access_key"`
	SecretKey string `mapstructure:"secret_key"`
	Bucket    string `mapstructure:"bucket"`
}

// Load 加载配置文件，环境变量自动覆盖。
// 优先级: 环境变量 > config.yaml > 默认值
func Load(configFile string) (*Config, error) {
	v := viper.New()

	// 默认值
	setDefaults(v)

	if configFile != "" {
		v.SetConfigFile(configFile)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(".")
	}

	// 环境变量映射: server.addr → SERVER_ADDR
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("read config: %w", err)
		}
		// 配置文件不存在时使用默认值 + 环境变量
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	return &cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.addr", ":8080")
	v.SetDefault("server.mode", "release")
	v.SetDefault("server.read_timeout", "15s")
	v.SetDefault("server.write_timeout", "30s")
	v.SetDefault("jwt.secret", "dev-secret-change-in-production")
	v.SetDefault("jwt.ttl", "24h")
	v.SetDefault("storage.upload_dir", "./data/uploads")
	v.SetDefault("storage.max_upload_size", 500)
	v.SetDefault("redis.addr", "")
	v.SetDefault("mysql.dsn", "")
	v.SetDefault("mysql.max_open_conns", 25)
	v.SetDefault("mysql.max_idle_conns", 5)
	v.SetDefault("features.seed", true)
	v.SetDefault("features.mq_enabled", true)
	v.SetDefault("features.transcode_enabled", true)
	v.SetDefault("features.reconcile_enabled", true)
	v.SetDefault("features.recommend_enabled", true)
	v.SetDefault("reconcile.threshold", 5)
	v.SetDefault("reconcile.interval", "5m")
	v.SetDefault("rate_limit.login.qps", 5)
	v.SetDefault("rate_limit.login.burst", 10)
	v.SetDefault("rate_limit.upload.qps", 10)
	v.SetDefault("rate_limit.upload.burst", 15)
	v.SetDefault("rate_limit.video.qps", 100)
	v.SetDefault("rate_limit.video.burst", 200)
	v.SetDefault("rate_limit.feed.qps", 50)
	v.SetDefault("rate_limit.feed.burst", 100)
	v.SetDefault("rate_limit.user.qps", 20)
	v.SetDefault("rate_limit.user.burst", 50)
	v.SetDefault("rate_limit.fallback.qps", 500)
	v.SetDefault("rate_limit.fallback.burst", 1000)
}

// RedisEnabled 判断是否启用 Redis。
func (c *Config) RedisEnabled() bool { return c.Redis.Addr != "" }

// MysqlEnabled 判断是否启用 MySQL。
func (c *Config) MysqlEnabled() bool { return c.MySQL.DSN != "" }

// OSSEnabled 判断是否启用 OSS。
func (c *Config) OSSEnabled() bool { return c.OSS.Endpoint != "" }

// RateLimitRules 返回 PathLimiter 所需的规则 map。
func (c *Config) RateLimitRules() map[string][2]int {
	return map[string][2]int{
		"/api/login":  {c.RateLimit.Login.QPS, c.RateLimit.Login.Burst},
		"/api/upload": {c.RateLimit.Upload.QPS, c.RateLimit.Upload.Burst},
		"/api/videos": {c.RateLimit.Video.QPS, c.RateLimit.Video.Burst},
		"/api/feed":   {c.RateLimit.Feed.QPS, c.RateLimit.Feed.Burst},
		"/api/rec":    {c.RateLimit.Feed.QPS, c.RateLimit.Feed.Burst},
		"/api/users":  {c.RateLimit.User.QPS, c.RateLimit.User.Burst},
	}
}
