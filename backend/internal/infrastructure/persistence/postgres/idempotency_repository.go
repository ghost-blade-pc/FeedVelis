package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	idempotencyApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
)

type IdempotencyRepository struct{ pool *pgxpool.Pool }

func NewIdempotencyRepository(pool *pgxpool.Pool) *IdempotencyRepository {
	return &IdempotencyRepository{pool: pool}
}

func (r *IdempotencyRepository) Begin(ctx context.Context, identity idempotencyApp.Identity, digest [32]byte, now, expiresAt time.Time) (idempotencyApp.Record, bool, error) {
	connection := querier(ctx, r.pool)
	for attempts := 0; attempts < 2; attempts++ {
		command, err := connection.Exec(ctx, `INSERT INTO velis.idempotency_operations
(actor_user_id,operation,idempotency_key,request_digest,status,created_at,expires_at)
VALUES ($1,$2,$3,$4,'pending',$5,$6) ON CONFLICT DO NOTHING`, identity.ActorUserID,
			identity.Operation, identity.Key, digest[:], now, expiresAt)
		if err != nil {
			return idempotencyApp.Record{}, false, err
		}
		inserted := command.RowsAffected() == 1
		record, err := scanIdempotency(connection.QueryRow(ctx, `SELECT request_digest,status,
result_version,result_payload,created_at,completed_at,expires_at
FROM velis.idempotency_operations WHERE actor_user_id=$1 AND operation=$2 AND idempotency_key=$3
FOR UPDATE`, identity.ActorUserID, identity.Operation, identity.Key), identity)
		if err != nil {
			return idempotencyApp.Record{}, false, err
		}
		if !inserted && !record.ExpiresAt.After(now) {
			if _, err := connection.Exec(ctx, `DELETE FROM velis.idempotency_operations
WHERE actor_user_id=$1 AND operation=$2 AND idempotency_key=$3`, identity.ActorUserID, identity.Operation, identity.Key); err != nil {
				return idempotencyApp.Record{}, false, err
			}
			continue
		}
		if !bytes.Equal(record.Digest[:], digest[:]) {
			return idempotencyApp.Record{}, false, idempotencyApp.ErrKeyReused
		}
		return record, inserted, nil
	}
	return idempotencyApp.Record{}, false, errors.New("无法重建过期幂等操作")
}

func (r *IdempotencyRepository) Succeed(ctx context.Context, identity idempotencyApp.Identity, resultVersion int, result json.RawMessage, resourceType, resourceID string, completedAt time.Time) error {
	command, err := querier(ctx, r.pool).Exec(ctx, `UPDATE velis.idempotency_operations SET status='succeeded',
result_version=$4,result_payload=$5,resource_type=NULLIF($6,''),resource_id=NULLIF($7,''),completed_at=$8
WHERE actor_user_id=$1 AND operation=$2 AND idempotency_key=$3 AND status='pending'`,
		identity.ActorUserID, identity.Operation, identity.Key, resultVersion, result, resourceType, resourceID, completedAt)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return idempotencyApp.ErrPending
	}
	return nil
}

func scanIdempotency(row pgx.Row, identity idempotencyApp.Identity) (idempotencyApp.Record, error) {
	var record idempotencyApp.Record
	var digest []byte
	var resultVersion *int
	record.Identity = identity
	err := row.Scan(&digest, &record.Status, &resultVersion, &record.Result, &record.CreatedAt, &record.CompletedAt, &record.ExpiresAt)
	if err != nil {
		return idempotencyApp.Record{}, err
	}
	copy(record.Digest[:], digest)
	if resultVersion != nil {
		record.ResultVersion = *resultVersion
	}
	return record, nil
}
