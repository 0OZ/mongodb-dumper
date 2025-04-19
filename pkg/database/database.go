package database

import (
	"context"
	"database/sql"
	"fmt"
	"qp-connector/internal/config"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

func Connect(config config.DatabaseConfig) (*sql.DB, error) {
	// Include important timeout parameters in DSN
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&charset=utf8mb4&collation=utf8mb4_unicode_ci&timeout=10s&readTimeout=60s&writeTimeout=30s&net_read_timeout=60&net_write_timeout=60",
		config.Username,
		config.Password,
		config.Host,
		config.Port,
		config.Database,
	)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}

	// Improved connection pool settings
	db.SetMaxOpenConns(50)                 // Increase max connections
	db.SetMaxIdleConns(10)                 // Keep more connections ready
	db.SetConnMaxLifetime(5 * time.Minute) // Recycle connections
	db.SetConnMaxIdleTime(3 * time.Minute) // Don't keep idle connections too long

	// Verify connection with timeout context
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err = db.PingContext(ctx); err != nil {
		return nil, err
	}

	return db, nil
}
