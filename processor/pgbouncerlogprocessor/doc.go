// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package pgbouncerlogprocessor parses PgBouncer log messages and extracts
// structured fields like database, user, client IP, and connection info.
//
// PgBouncer log format:
//
//	2025-12-31 01:46:17.407 UTC [1] LOG C-0x7f828e6b34c0: postgres/postgres@172.18.0.60:39630 login attempt: ...
//
// Fields extracted:
//   - Timestamp -> log record timestamp
//   - PID -> process.pid
//   - Level (LOG/DEBUG/WARNING/ERROR/FATAL) -> severity_text/severity_number
//   - Connection ID -> pgbouncer.connection_id
//   - Database -> db.name
//   - User -> db.user
//   - Client IP -> client.address
//   - Client Port -> client.port
//   - Message -> body
package pgbouncerlogprocessor // import "github.com/vladistan/otelcol-custom/processor/pgbouncerlogprocessor"
