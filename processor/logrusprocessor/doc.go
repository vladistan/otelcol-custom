// Package logrusprocessor implements a processor that parses Go logrus-formatted
// log lines into structured fields.
//
// Supported formats:
//   - JSON: {"level":"info","msg":"Server started","time":"...","port":8080}
//   - Text: time="..." level=info msg="Server started" port=8080
//
// The processor auto-detects the format and maps logrus fields to OTel semantic conventions.
package logrusprocessor // import "github.com/vladistan/otelcol-custom/processor/logrusprocessor"

// TODO: Implement in Phase 3.5
