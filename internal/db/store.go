package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hdmain/goincus/internal/config"
	"github.com/hdmain/goincus/internal/models"
)

// Store wraps a PostgreSQL connection pool.
type Store struct {
	pool *pgxpool.Pool
}

// Connect opens a pool to the local goincus database.
func Connect(ctx context.Context, cfg config.DatabaseConfig) (*Store, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MinConns = cfg.MinConns

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("connect database: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return &Store{pool: pool}, nil
}

// Close releases the pool.
func (s *Store) Close() {
	s.pool.Close()
}

// Ping checks database connectivity.
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// Migrate applies SQL files from dir in lexicographic order.
func (s *Store) Migrate(ctx context.Context, dir string) error {
	if _, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`); err != nil {
		return fmt.Errorf("ensure migrations table: %w", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		version := entry.Name()

		var exists bool
		err := s.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`, version,
		).Scan(&exists)
		if err != nil {
			return fmt.Errorf("check migration %s: %w", version, err)
		}
		if exists {
			continue
		}

		body, err := os.ReadFile(filepath.Join(dir, version))
		if err != nil {
			return fmt.Errorf("read migration %s: %w", version, err)
		}

		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", version, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations(version) VALUES ($1)`, version,
		); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration %s: %w", version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

// CreateInstance inserts a pending instance row.
func (s *Store) CreateInstance(ctx context.Context, inst *models.Instance) error {
	const q = `
		INSERT INTO instances (
			id, name, incus_name, image, status, cpu_cores, memory_mb, storage_gb, processes, error_message, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`
	_, err := s.pool.Exec(ctx, q,
		inst.ID, inst.Name, inst.IncusName, inst.Image, inst.Status,
		inst.CPUCores, inst.MemoryMB, inst.StorageGB, inst.Processes,
		inst.ErrorMessage, inst.CreatedAt, inst.UpdatedAt,
	)
	return err
}

// UpdateInstanceStatus updates status and optional error message.
func (s *Store) UpdateInstanceStatus(ctx context.Context, id uuid.UUID, status models.InstanceStatus, errMsg string) error {
	const q = `
		UPDATE instances
		SET status = $2, error_message = $3, updated_at = NOW()
		WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, id, status, errMsg)
	return err
}

// GetInstance returns an instance by ID including port mappings.
func (s *Store) GetInstance(ctx context.Context, id uuid.UUID) (*models.Instance, error) {
	const q = `
		SELECT id, name, incus_name, image, status, cpu_cores, memory_mb, storage_gb, processes,
		       error_message, created_at, updated_at
		FROM instances WHERE id = $1 AND status <> 'deleted'`
	inst := &models.Instance{}
	err := s.pool.QueryRow(ctx, q, id).Scan(
		&inst.ID, &inst.Name, &inst.IncusName, &inst.Image, &inst.Status,
		&inst.CPUCores, &inst.MemoryMB, &inst.StorageGB, &inst.Processes,
		&inst.ErrorMessage, &inst.CreatedAt, &inst.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}

	ports, err := s.ListPorts(ctx, id)
	if err != nil {
		return nil, err
	}
	inst.Ports = ports
	return inst, nil
}

// GetInstanceByName looks up a non-deleted instance by name.
func (s *Store) GetInstanceByName(ctx context.Context, name string) (*models.Instance, error) {
	const q = `
		SELECT id, name, incus_name, image, status, cpu_cores, memory_mb, storage_gb, processes,
		       error_message, created_at, updated_at
		FROM instances WHERE name = $1 AND status <> 'deleted'`
	inst := &models.Instance{}
	err := s.pool.QueryRow(ctx, q, name).Scan(
		&inst.ID, &inst.Name, &inst.IncusName, &inst.Image, &inst.Status,
		&inst.CPUCores, &inst.MemoryMB, &inst.StorageGB, &inst.Processes,
		&inst.ErrorMessage, &inst.CreatedAt, &inst.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	ports, err := s.ListPorts(ctx, inst.ID)
	if err != nil {
		return nil, err
	}
	inst.Ports = ports
	return inst, nil
}

// ListInstances returns all non-deleted instances.
func (s *Store) ListInstances(ctx context.Context) ([]models.Instance, error) {
	const q = `
		SELECT id, name, incus_name, image, status, cpu_cores, memory_mb, storage_gb, processes,
		       error_message, created_at, updated_at
		FROM instances WHERE status <> 'deleted'
		ORDER BY created_at DESC`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Instance
	for rows.Next() {
		var inst models.Instance
		if err := rows.Scan(
			&inst.ID, &inst.Name, &inst.IncusName, &inst.Image, &inst.Status,
			&inst.CPUCores, &inst.MemoryMB, &inst.StorageGB, &inst.Processes,
			&inst.ErrorMessage, &inst.CreatedAt, &inst.UpdatedAt,
		); err != nil {
			return nil, err
		}
		ports, err := s.ListPorts(ctx, inst.ID)
		if err != nil {
			return nil, err
		}
		inst.Ports = ports
		out = append(out, inst)
	}
	return out, rows.Err()
}

// AddPort inserts a port mapping row.
func (s *Store) AddPort(ctx context.Context, p *models.PortMapping) error {
	const q = `
		INSERT INTO port_mappings (id, instance_id, protocol, host_port, internal_port, device_name, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`
	_, err := s.pool.Exec(ctx, q,
		p.ID, p.InstanceID, p.Protocol, p.HostPort, p.InternalPort, p.DeviceName, p.CreatedAt,
	)
	return err
}

// ListPorts returns mappings for an instance.
func (s *Store) ListPorts(ctx context.Context, instanceID uuid.UUID) ([]models.PortMapping, error) {
	const q = `
		SELECT id, instance_id, protocol, host_port, internal_port, device_name, created_at
		FROM port_mappings WHERE instance_id = $1 ORDER BY host_port`
	rows, err := s.pool.Query(ctx, q, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.PortMapping
	for rows.Next() {
		var p models.PortMapping
		if err := rows.Scan(&p.ID, &p.InstanceID, &p.Protocol, &p.HostPort, &p.InternalPort, &p.DeviceName, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if out == nil {
		out = []models.PortMapping{}
	}
	return out, rows.Err()
}

// GetPort returns a single mapping.
func (s *Store) GetPort(ctx context.Context, instanceID, portID uuid.UUID) (*models.PortMapping, error) {
	const q = `
		SELECT id, instance_id, protocol, host_port, internal_port, device_name, created_at
		FROM port_mappings WHERE id = $1 AND instance_id = $2`
	p := &models.PortMapping{}
	err := s.pool.QueryRow(ctx, q, portID, instanceID).Scan(
		&p.ID, &p.InstanceID, &p.Protocol, &p.HostPort, &p.InternalPort, &p.DeviceName, &p.CreatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return p, nil
}

// DeletePort removes a mapping row.
func (s *Store) DeletePort(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM port_mappings WHERE id = $1`, id)
	return err
}

// ListUsedHostPorts returns all allocated host ports from the database.
func (s *Store) ListUsedHostPorts(ctx context.Context) ([]int, error) {
	rows, err := s.pool.Query(ctx, `SELECT host_port FROM port_mappings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ports []int
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		ports = append(ports, p)
	}
	return ports, rows.Err()
}

// SoftDeleteInstance marks an instance deleted.
func (s *Store) SoftDeleteInstance(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE instances SET status = 'deleted', updated_at = NOW() WHERE id = $1`, id)
	return err
}
