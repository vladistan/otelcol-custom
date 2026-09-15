// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package nginxlogprocessor parses Nginx access logs and extracts structured fields.
//
// Supported formats:
//   - Combined log format: $remote_addr - $remote_user [$time_local] "$request" $status $body_bytes_sent "$http_referer" "$http_user_agent"
//   - Combined with X-Forwarded-For: ... "$http_x_forwarded_for"
//
// Extracted attributes (OTEL semantic conventions):
//   - client.address: Remote client IP address
//   - url.path: Request path
//   - url.query: Query string (if present)
//   - http.request.method: HTTP method (GET, POST, etc.)
//   - http.response.status_code: HTTP status code
//   - http.response.body.bytes: Response body size
//   - http.request.header.referer: Referer header
//   - user_agent.original: User-Agent header
//   - network.forwarded_for: X-Forwarded-For header (if present)
//
// Resource attributes:
//   - service.name: Set to "nginx" if not already set
package nginxlogprocessor // import "github.com/vladistan/otelcol-custom/processor/nginxlogprocessor"
