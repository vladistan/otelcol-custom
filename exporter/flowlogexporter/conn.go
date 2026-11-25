// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package flowlogexporter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// batch is the part of a native insert batch the exporter uses.
type batch interface {
	Append(v ...any) error
	Send() error
	Close() error
}

// conn is the part of a ClickHouse connection the exporter uses; tests replace it.
type conn interface {
	PrepareBatch(ctx context.Context, query string) (batch, error)
	Close() error
}

type nativeConn struct{ c driver.Conn }

func (n *nativeConn) PrepareBatch(ctx context.Context, query string) (batch, error) {
	b, err := n.c.PrepareBatch(ctx, query)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (n *nativeConn) Close() error { return n.c.Close() }

// dial opens the native connection and pings the server.
func dial(ctx context.Context, cfg *Config) (conn, error) {
	c, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{cfg.Endpoint},
		Auth: clickhouse.Auth{
			Database: cfg.Database,
			Username: cfg.Username,
			Password: string(cfg.Password),
		},
		DialTimeout: 10 * time.Second,
		Compression: &clickhouse.Compression{Method: clickhouse.CompressionLZ4},
	})
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	if err := c.Ping(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("ping: %w", err), c.Close())
	}
	return &nativeConn{c: c}, nil
}

// schemaCodes are the ClickHouse error codes of a statement the table cannot take: no such table,
// no such column, unknown identifier, a type mismatch or a value that cannot convert.
var schemaCodes = map[int32]bool{16: true, 47: true, 53: true, 60: true, 70: true, 349: true}

// errAppend marks an error of Append, which is a type mismatch between a row and the table.
type errAppend struct{ err error }

func (e errAppend) Error() string { return "append: " + e.err.Error() }
func (e errAppend) Unwrap() error { return e.err }

// isConnError reports whether the error did not come from the server or from Append: a dead,
// reset or timed-out connection. A ClickHouse exception, code 202 included, keeps the connection.
func isConnError(err error) bool {
	var app errAppend
	var ex *clickhouse.Exception
	return !errors.As(err, &app) && !errors.As(err, &ex)
}

// isSchemaError reports whether the error says the rows can never fit the table.
func isSchemaError(err error) bool {
	var app errAppend
	if errors.As(err, &app) {
		return true
	}
	var ex *clickhouse.Exception
	return errors.As(err, &ex) && schemaCodes[ex.Code]
}
