package database

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"at.ourproject/vfeeg-backend/model"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

var db struct {
	sync.Once
	Database
}

type sqlDatabase struct {
	db *sqlx.DB
	// statsDone stops the pool-stats logger; closed by CloseDB.
	statsDone chan struct{}
}

type Database interface {
	Select(ctx context.Context, dest interface{}, query string, args ...interface{}) error
	CloseDB() error
	MigrateDB() error
	EegRepository
	ParticipantRepository
	MeteringPointRepository
	NotificationRepository
	ExcelRepository
	TariffRepository
	MqttRepository
}

type MqttRepository interface {
	UpdateEegOnlineState(ctx context.Context, tenant string, online bool) error
	RegisterEeg(ctx context.Context, eeg *model.Eeg) error
}

func initDB(ctx context.Context) error {
	var err error

	sqlDB := sqlDatabase{}

	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s "+
		"password=%s dbname=%s sslmode=disable",
		viper.GetString("database.host"), viper.GetInt("database.port"), viper.GetString("database.user"),
		viper.GetString("database.password"), viper.GetString("database.dbname"))
	sqlDB.db, err = sqlx.ConnectContext(ctx, "postgres", psqlInfo)
	if err != nil {
		return err
	}

	maxConnections := viper.GetInt("database.maxOpenConns")
	if maxConnections == 0 {
		maxConnections = 25
	}
	maxIdleConnections := viper.GetInt("database.maxIdleConns")
	if maxIdleConnections == 0 {
		maxIdleConnections = 25
	}
	maxLifetimeConnections := viper.GetDuration("database.connMaxLifetime")
	if maxLifetimeConnections == 0 {
		maxLifetimeConnections = 5 * time.Minute
	}

	sqlDB.db.SetMaxOpenConns(maxConnections)
	sqlDB.db.SetMaxIdleConns(maxIdleConnections)
	sqlDB.db.SetConnMaxLifetime(maxLifetimeConnections)

	statsInterval := viper.GetDuration("database.statsLogInterval")
	if statsInterval == 0 {
		statsInterval = time.Minute
	}
	if statsInterval > 0 {
		sqlDB.statsDone = make(chan struct{})
		go sqlDB.logPoolStats(statsInterval)
	}

	db.Database = &sqlDB

	return nil
}

// logPoolStats periodically writes the connection pool counters to the log until
// CloseDB stops it. A leaking pool and a merely saturated one look alike from the
// outside, but not here: InUse rising monotonically towards MaxOpenConnections and
// never falling back means connections are not returned, whereas a growing
// WaitCount with fluctuating InUse is plain saturation.
func (db *sqlDatabase) logPoolStats(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-db.statsDone:
			return
		case <-ticker.C:
			s := db.db.Stats()
			entry := log.WithFields(log.Fields{
				"open":              s.OpenConnections,
				"inUse":             s.InUse,
				"idle":              s.Idle,
				"maxOpen":           s.MaxOpenConnections,
				"waitCount":         s.WaitCount,
				"waitDuration":      s.WaitDuration.String(),
				"maxIdleClosed":     s.MaxIdleClosed,
				"maxLifetimeClosed": s.MaxLifetimeClosed,
			})
			// Warn once the pool is nearly exhausted, so the onset is greppable.
			if s.MaxOpenConnections > 0 && s.InUse*5 >= s.MaxOpenConnections*4 {
				entry.Warn("db pool nearly exhausted")
				continue
			}
			entry.Info("db pool stats")
		}
	}
}

// GetDB returns the current DB.
func GetDB(ctx context.Context) (Database, error) {
	var err error
	db.Do(func() {
		err = initDB(ctx)
	})
	if err != nil {
		return nil, err //errors.Wrap(err, "failed to initialize DB")
	}
	if db.Database == nil {
		return nil, errors.New("database was not initialized")
	}

	return db.Database, nil
}

func (db *sqlDatabase) CloseDB() error {
	log.Info("Closing database connection")
	if db.statsDone != nil {
		close(db.statsDone)
		db.statsDone = nil
	}
	if db.db != nil {
		return db.db.Close()
	}
	return errors.New("database was not initialized")
}

func (db *sqlDatabase) Select(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
	return db.db.SelectContext(ctx, dest, query, args...)
}

func (db *sqlDatabase) UpdateEegOnlineState(ctx context.Context, tenant string, online bool) error {
	return db.UpdateOnlineState(ctx, tenant, online)
}

func (db *sqlDatabase) RegisterEeg(ctx context.Context, eeg *model.Eeg) error {
	return db.InsertEeg(ctx, eeg.RcNumber, eeg)
}

func (db *sqlDatabase) MigrateDB() error {
	log.Info("Start migration ...")

	dbDriver, err := postgres.WithInstance(db.db.DB, &postgres.Config{SchemaName: "base"})
	if err != nil {
		log.Fatal(err)
		return err
	}

	m, err := migrate.NewWithDatabaseInstance(
		"embed://",
		"postgres", dbDriver)
	if err != nil {
		log.Fatal(err)
		return err
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		log.Fatal(err)
		return err
	}
	return nil
}
