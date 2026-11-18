# SQL Server on the Windows edge

The Windows edge build can read one SQL Server instance on its own host. The sample is `test/configs/windows-sqlserver.yaml`. It covers one host and uses the contrib `sqlserver` receiver at v0.164.1, `windowsperfcounters` and `filelog`. The readers of the data are the agents that use the ClickHouse tool. The target is SQL Server 2025 Express (major version 17); nothing from this instance is collected before 0.3.17.

## What is collected

- Metrics from the `sqlserver` receiver, including waits (`sqlserver.os.wait.duration`).
- The performance counters `Batch Requests/sec` (the query rate) and `Logins/sec`, read by `windowsperfcounters` under the object prefix of the named instance, `MSSQL$<instance>:`. The counters are configuration only and use no custom code.
- The SQL Server error log, one record per entry, read by `filelog` as UTF-16LE with a multiline start pattern. The directory must be readable by LocalSystem. The source reads the live file `ERRORLOG` only. SQL Server renames it to `ERRORLOG.1` and so on when it starts a new log, and the archives (`ERRORLOG.1` and later) are not read. A new file begins with a byte order mark and the lines end in CRLF; a processor removes both and the newline that ends the last line, so a record is the text of its entry.
- Slow queries, marked as present: the `db.server.top_query` and `db.server.query_sample` events carry the query hash, the plan hash and the time, row and read figures.

Query text is off. The two events also carry `db.query.text`, `sqlserver.query_plan`, `sqlserver.context_info` (text that the application sets) and `user.name`, so the sample does not delete them by name. A `transform` processor in the pipeline of the query events keeps only these attributes, which are the query and plan hashes, the figures of the query and the session, and the database name: it drops every other log attribute, empties the body and drops the scope attributes, and it keeps only the resource attributes the receiver declares. An attribute that a later release adds is dropped until someone adds it to the list. The events `db.server.query_plan` and `db.server.procedure_definition` stay off. The query events have their own pipeline and their own exporter, apart from every other log source, because query text can hold user data if the drop is ever removed. Retention of these two streams is set by the owners of the infrastructure that stores them.

The two query collections have development stability upstream, as have most of the metrics. In v0.164.1 they are switched on by `events`, and `top_query_collection` and `query_sample_collection` only tune them; an `enabled` key under either is rejected.

## The SQL login

Upstream documents no Windows (integrated) authentication, so the receiver needs a SQL login. The login that this host uses is read-only. It holds `CONNECT SQL`, `VIEW SERVER STATE` (`VIEW SERVER PERFORMANCE STATE` on SQL Server 2022 and later), `VIEW ANY DATABASE` and `VIEW ANY DEFINITION`, sees 7 of 7 databases, and needs no new grant. Grant nothing beyond these four.

## The password

The password is read from a file with `${file:PATH}` inside the `datasource` string.

- The file is readable only by Administrators and SYSTEM.
- There is no password in the service environment: the registry key of the service environment is readable by every local user.
- The file is static and the collector reads it once, when the configuration is resolved; there is no rotation step.
- The file holds the password alone. A trailing newline is fine: the driver trims it, and so it trims a leading or trailing space, which a password cannot use here. Keep out `;`, quotes and a space followed by `#`, because the value goes into a connection string and is parsed as YAML first.
- With the file missing or unreadable, configuration resolution fails and the collector does not start. Write the path with forward slashes, such as `C:/ProgramData/otelcol-edge/secrets/sqlserver-password`.

The Windows edge manifest declares the five providers of the entrypoint: `env`, `file`, `http`, `https` and `yaml`, so `${env:NAME}` and `${file:PATH}` resolve in the config and `yaml:` and `https:` config URIs still work. A literal `password` in the `sqlserver` section, or `password=` with a literal value in the `datasource`, fails the check in `test/sqlserver`.

## Encryption

The instance has ForceEncryption off and a self-signed certificate. A connection from `server`, `port`, `username` and `password` cannot set the certificate trust, so the sample uses `datasource` with `encrypt=true;TrustServerCertificate=true`: the session is encrypted and the certificate is trusted. Use `encrypt=disable` to turn encryption off instead.

## Not verified

The sample is checked by `just test-sqlserver`: it loads the config with the real receiver code, resolves the password from a fixture file, drops the query text from a fixture record and reads a UTF-16LE error log fixture. It has not run against SQL Server 2025 Express yet.
