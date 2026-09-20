package bootstrap

import (
	"fmt"
	"log/slog"
	"net"
	"time"

	accountApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/account"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/clock"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/security"
	"github.com/jackc/pgx/v5/pgxpool"
)

// accessTokenLeeway 是 JWT 校验的时钟容差，按规格固定为 30 秒。
const accessTokenLeeway = 30 * time.Second

// buildAuthService 装配认证与会话用例；仅在 auth.enabled 为真时调用，缺少密钥会直接失败。
// 散列实现外包一层耗时观测，使密码散列耗时成为结构化日志字段。
func buildAuthService(cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger) (*accountApp.Service, error) {
	hasher, err := security.NewPasswordHasher(cfg.Auth.Argon2Params(), cfg.Auth.Argon2.Concurrency)
	if err != nil {
		return nil, err
	}
	timedHasher := security.NewTimedPasswordHasher(hasher, logger)
	active, err := cfg.Auth.ActiveSigningKey()
	if err != nil {
		return nil, err
	}
	previous, err := cfg.Auth.PreviousSigningKey()
	if err != nil {
		return nil, err
	}
	signer, err := security.NewJWTSigner(cfg.Auth.JWTIssuer, cfg.Auth.JWTAudience, active, previous, accessTokenLeeway, nil)
	if err != nil {
		return nil, err
	}
	throttleSecret, err := cfg.Auth.ThrottleKeyBytes()
	if err != nil {
		return nil, err
	}
	throttleKeys, err := security.NewThrottleKeys(throttleSecret)
	if err != nil {
		return nil, err
	}
	return accountApp.NewService(accountApp.Deps{
		Users:        postgres.NewAccountRepository(pool),
		Sessions:     postgres.NewSessionRepository(pool),
		Tokens:       postgres.NewRefreshTokenRepository(pool),
		Throttle:     postgres.NewThrottleRepository(pool),
		Hasher:       timedHasher,
		Signer:       signer,
		Blocklist:    security.NewCommonPasswordBlocklist(),
		ThrottleKeys: throttleKeys,
		Secrets:      security.NewTokenSecrets(),
		Tx:           postgres.NewTxManager(pool),
		Clock:        clock.System{},

		Policy:              cfg.Auth.Policy(),
		AccessTTL:           cfg.Auth.AccessTTL,
		SessionTTL:          cfg.Auth.SessionTTL,
		RegistrationEnabled: cfg.Auth.RegistrationEnabled,
	})
}

// buildAdminService 装配本地管理用例；不依赖认证运行时配置，可在 auth.enabled=false 时先初始化管理员。
func buildAdminService(cfg config.Config, pool *pgxpool.Pool) (*accountApp.AdminService, error) {
	hasher, err := security.NewPasswordHasher(cfg.Auth.Argon2Params(), cfg.Auth.Argon2.Concurrency)
	if err != nil {
		return nil, err
	}
	return accountApp.NewAdminService(accountApp.AdminDeps{
		Users:     postgres.NewAccountRepository(pool),
		Sessions:  postgres.NewSessionRepository(pool),
		Audits:    postgres.NewAuditRepository(pool),
		Locks:     postgres.NewAccountTxLocks(),
		Hasher:    hasher,
		Blocklist: security.NewCommonPasswordBlocklist(),
		Tx:        postgres.NewTxManager(pool),
		Clock:     clock.System{},
	})
}

// parseTrustedProxies 解析可信代理网段；空列表表示不信任任何转发头。
func parseTrustedProxies(cidrs []string) ([]*net.IPNet, error) {
	networks := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("可信代理网段 %q 无效: %w", cidr, err)
		}
		networks = append(networks, network)
	}
	return networks, nil
}
