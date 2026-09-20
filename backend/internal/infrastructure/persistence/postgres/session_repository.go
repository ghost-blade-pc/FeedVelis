package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
)

type SessionRepository struct{ pool *pgxpool.Pool }

func NewSessionRepository(pool *pgxpool.Pool) *SessionRepository {
	return &SessionRepository{pool: pool}
}

const sessionColumns = `id, user_id, created_at, expires_at, last_refreshed_at, revoked_at, revoke_reason, version`

func (r *SessionRepository) Create(ctx context.Context, session accountDomain.Session) error {
	_, err := querier(ctx, r.pool).Exec(ctx, `INSERT INTO velis.auth_sessions
(id, user_id, created_at, expires_at, last_refreshed_at, version)
VALUES ($1, $2, $3, $4, $5, 1)`,
		pgUUID(session.ID), pgUUID(session.UserID), session.CreatedAt, session.ExpiresAt, session.LastRefreshedAt)
	return err
}

func (r *SessionRepository) GetByID(ctx context.Context, id accountDomain.UUID) (accountDomain.Session, error) {
	session, err := scanSession(querier(ctx, r.pool).QueryRow(ctx,
		`SELECT `+sessionColumns+` FROM velis.auth_sessions WHERE id = $1`, pgUUID(id)))
	if errors.Is(err, pgx.ErrNoRows) {
		return accountDomain.Session{}, accountDomain.ErrSessionInvalid
	}
	return session, err
}

// Revoke 幂等：重复撤销或会话已过期都不返回错误。
func (r *SessionRepository) Revoke(ctx context.Context, id accountDomain.UUID, reason accountDomain.RevokeReason, now time.Time) error {
	_, err := querier(ctx, r.pool).Exec(ctx, `UPDATE velis.auth_sessions
SET revoked_at = $2, revoke_reason = $3, version = version + 1
WHERE id = $1 AND revoked_at IS NULL`, pgUUID(id), now, reason)
	return err
}

func (r *SessionRepository) RevokeAllForUser(ctx context.Context, userID accountDomain.UUID, reason accountDomain.RevokeReason, now time.Time) (int64, error) {
	tag, err := querier(ctx, r.pool).Exec(ctx, `UPDATE velis.auth_sessions
SET revoked_at = $2, revoke_reason = $3, version = version + 1
WHERE user_id = $1 AND revoked_at IS NULL`, pgUUID(userID), now, reason)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

type RefreshTokenRepository struct {
	pool *pgxpool.Pool
	tx   *TxManager
}

func NewRefreshTokenRepository(pool *pgxpool.Pool) *RefreshTokenRepository {
	return &RefreshTokenRepository{pool: pool, tx: NewTxManager(pool)}
}

// FindByDigest 按摘要定位令牌，不消费也不加锁；未知摘要返回 ErrRefreshUnknown。
func (r *RefreshTokenRepository) FindByDigest(ctx context.Context, digest [32]byte) (accountDomain.RefreshToken, error) {
	var (
		token       accountDomain.RefreshToken
		rawDigest   []byte
		id, sid     pgtype.UUID
		successorID pgtype.UUID
	)
	err := querier(ctx, r.pool).QueryRow(ctx, `SELECT id, session_id, digest, issued_at, expires_at, consumed_at, successor_id
FROM velis.refresh_tokens WHERE digest = $1`, digest[:]).
		Scan(&id, &sid, &rawDigest, &token.IssuedAt, &token.ExpiresAt, &token.ConsumedAt, &successorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return accountDomain.RefreshToken{}, accountDomain.ErrRefreshUnknown
	}
	if err != nil {
		return accountDomain.RefreshToken{}, err
	}
	token.ID = fromPGUUID(id)
	token.SessionID = fromPGUUID(sid)
	copy(token.Digest[:], rawDigest)
	if successorID.Valid {
		successor := fromPGUUID(successorID)
		token.SuccessorID = &successor
	}
	return token, nil
}

func (r *RefreshTokenRepository) Create(ctx context.Context, token accountDomain.RefreshToken) error {
	_, err := querier(ctx, r.pool).Exec(ctx, `INSERT INTO velis.refresh_tokens
(id, session_id, digest, issued_at, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		pgUUID(token.ID), pgUUID(token.SessionID), token.Digest[:], token.IssuedAt, token.ExpiresAt)
	return err
}

// Rotate 在同一事务内先锁会话、再锁令牌，消费旧令牌并插入后继令牌。
// 已消费令牌返回 ErrRefreshReplay 并撤销当前会话；未知摘要返回 ErrRefreshUnknown，不影响其他会话。
func (r *RefreshTokenRepository) Rotate(ctx context.Context, digest [32]byte, next accountDomain.RefreshToken, now time.Time) (accountDomain.Session, error) {
	var (
		session accountDomain.Session
		outcome error
	)
	err := r.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		var sessionID pgtype.UUID
		err := querier(ctx, r.pool).QueryRow(ctx,
			`SELECT session_id FROM velis.refresh_tokens WHERE digest = $1`, digest[:]).Scan(&sessionID)
		if errors.Is(err, pgx.ErrNoRows) {
			outcome = accountDomain.ErrRefreshUnknown
			return nil
		}
		if err != nil {
			return err
		}
		session, err = scanSession(querier(ctx, r.pool).QueryRow(ctx,
			`SELECT `+sessionColumns+` FROM velis.auth_sessions WHERE id = $1 FOR UPDATE`, sessionID))
		if errors.Is(err, pgx.ErrNoRows) {
			outcome = accountDomain.ErrRefreshUnknown
			return nil
		}
		if err != nil {
			return err
		}
		if !session.IsActive(now) {
			outcome = accountDomain.ErrSessionInvalid
			return nil
		}

		var consumedAt *time.Time
		var expiresAt time.Time
		if err := querier(ctx, r.pool).QueryRow(ctx,
			`SELECT consumed_at, expires_at FROM velis.refresh_tokens WHERE digest = $1 FOR UPDATE`, digest[:]).
			Scan(&consumedAt, &expiresAt); err != nil {
			return err
		}
		if consumedAt != nil {
			if _, err := querier(ctx, r.pool).Exec(ctx, `UPDATE velis.auth_sessions
SET revoked_at = $2, revoke_reason = $3, version = version + 1
WHERE id = $1 AND revoked_at IS NULL`, pgUUID(session.ID), now, accountDomain.RevokeReasonReplay); err != nil {
				return err
			}
			outcome = accountDomain.ErrRefreshReplay
			return nil
		}
		if !now.Before(expiresAt) {
			outcome = accountDomain.ErrSessionInvalid
			return nil
		}

		if _, err := querier(ctx, r.pool).Exec(ctx, `UPDATE velis.refresh_tokens
SET consumed_at = $2, successor_id = $3 WHERE digest = $1`, digest[:], now, pgUUID(next.ID)); err != nil {
			return err
		}
		// 后继令牌的会话与有效期由被锁定的会话决定，调用方无需预知。
		next.SessionID = session.ID
		next.ExpiresAt = session.ExpiresAt
		if next.IssuedAt.IsZero() {
			next.IssuedAt = now
		}
		if _, err := querier(ctx, r.pool).Exec(ctx, `INSERT INTO velis.refresh_tokens
(id, session_id, digest, issued_at, expires_at) VALUES ($1, $2, $3, $4, $5)`,
			pgUUID(next.ID), pgUUID(next.SessionID), next.Digest[:], next.IssuedAt, next.ExpiresAt); err != nil {
			return err
		}
		refreshed, err := scanSession(querier(ctx, r.pool).QueryRow(ctx, `UPDATE velis.auth_sessions
SET last_refreshed_at = $2, version = version + 1 WHERE id = $1 RETURNING `+sessionColumns,
			pgUUID(session.ID), now))
		if err != nil {
			return err
		}
		session = refreshed
		return nil
	})
	if err != nil {
		return accountDomain.Session{}, err
	}
	return session, outcome
}

func scanSession(row scanner) (accountDomain.Session, error) {
	var (
		session      accountDomain.Session
		id, userID   pgtype.UUID
		revokeReason *string
	)
	err := row.Scan(&id, &userID, &session.CreatedAt, &session.ExpiresAt, &session.LastRefreshedAt,
		&session.RevokedAt, &revokeReason, &session.Version)
	if err != nil {
		return accountDomain.Session{}, err
	}
	session.ID = fromPGUUID(id)
	session.UserID = fromPGUUID(userID)
	if revokeReason != nil {
		reason := accountDomain.RevokeReason(*revokeReason)
		session.RevokeReason = &reason
	}
	return session, nil
}
