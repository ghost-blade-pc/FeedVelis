package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"time"
)

// RecommendConfig 限制推荐首查与冻结游标的资源消耗。
type RecommendConfig struct {
	FirstQueryRaw      string        `yaml:"first_query_timeout"`
	FirstQueryTimeout  time.Duration `yaml:"-"`
	BM25Candidates     int           `yaml:"bm25_candidates"`
	KNNCandidates      int           `yaml:"knn_candidates"`
	CursorTTLRaw       string        `yaml:"cursor_ttl"`
	CursorTTL          time.Duration `yaml:"-"`
	CursorKey          SecretBytes   `yaml:"-"`
	CursorKeyEphemeral bool          `yaml:"-"`
}

func applyRecommendEnvironment(cfg *Config) error {
	r := &cfg.Recommend
	setString(&r.FirstQueryRaw, "VELIS_RECOMMEND_FIRST_QUERY_TIMEOUT")
	setString(&r.CursorTTLRaw, "VELIS_RECOMMEND_CURSOR_TTL")
	if err := setInt(&r.BM25Candidates, "VELIS_RECOMMEND_BM25_CANDIDATES"); err != nil {
		return err
	}
	if err := setInt(&r.KNNCandidates, "VELIS_RECOMMEND_KNN_CANDIDATES"); err != nil {
		return err
	}
	if encoded, ok := os.LookupEnv("VELIS_RECOMMEND_CURSOR_KEY"); ok && strings.TrimSpace(encoded) != "" {
		decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || len(decoded) < 32 {
			return errors.New("环境变量 VELIS_RECOMMEND_CURSOR_KEY 必须是 Base64 且解码后至少 32 字节")
		}
		r.CursorKey = SecretBytes(decoded)
	}
	return nil
}

func validateRecommendConfig(r *RecommendConfig, environment string) error {
	var err error
	if r.FirstQueryTimeout, err = durationWithin("recommend.first_query_timeout", r.FirstQueryRaw, 500*time.Millisecond, 30*time.Second); err != nil {
		return err
	}
	if r.CursorTTL, err = durationWithin("recommend.cursor_ttl", r.CursorTTLRaw, 30*time.Second, 10*time.Minute); err != nil {
		return err
	}
	if r.BM25Candidates < 1 || r.BM25Candidates > 100 || r.KNNCandidates < 1 || r.KNNCandidates > 100 {
		return errors.New("recommend 每路候选必须介于 1 和 100")
	}
	if !r.CursorKey.IsSet() {
		if environment != "development" {
			// Worker、迁移与管理 CLI 不需要推荐游标；API 装配时再要求共享密钥。
			return nil
		}
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return errors.New("生成临时推荐游标密钥失败")
		}
		r.CursorKey = SecretBytes(key)
		r.CursorKeyEphemeral = true
	}
	return nil
}
