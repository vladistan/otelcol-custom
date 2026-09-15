// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package cronlogprocessor

import (
	"testing"
)

func TestIsCronLog(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		expected bool
	}{
		{
			name:     "CMD with simple command",
			body:     "(root) CMD (/usr/libexec/atrun)",
			expected: true,
		},
		{
			name:     "CMD with run-parts",
			body:     "(root) CMD (cd / && run-parts --report /etc/cron.hourly)",
			expected: true,
		},
		{
			name:     "CMD with flock and redirect",
			body:     "(root) CMD ((/usr/local/bin/flock -n -E 0 -o /tmp/filter_update_tables.lock /usr/local/opnsense/scripts/filter/update_tables.py --quick) > /dev/null)",
			expected: true,
		},
		{
			name:     "PAM session opened",
			body:     "pam_unix(cron:session): session opened for user root(uid=0) by (uid=0)",
			expected: true,
		},
		{
			name:     "PAM session closed",
			body:     "pam_unix(cron:session): session closed for user root",
			expected: true,
		},
		{
			name:     "Non-cron message",
			body:     "some random log message",
			expected: false,
		},
		{
			name:     "Empty message",
			body:     "",
			expected: false,
		},
		{
			name:     "Similar but not cron",
			body:     "user CMD something",
			expected: false,
		},
		{
			name:     "CMD embedded in RFC5424 syslog message",
			body:     "1 2025-12-31T10:05:00.013372-05:00 storage1.homelab.home.example.com /usr/sbin/cron 19467 - - (root) CMD (/usr/libexec/atrun)",
			expected: true,
		},
		{
			name:     "run-parts starting",
			body:     "(/etc/cron.hourly) starting 0anacron",
			expected: true,
		},
		{
			name:     "run-parts finished",
			body:     "(/etc/cron.hourly) finished 0anacron",
			expected: true,
		},
		{
			name:     "run-parts daily starting",
			body:     "(/etc/cron.daily) starting logrotate",
			expected: true,
		},
		{
			name:     "CMDEND simple",
			body:     "(root) CMDEND (run-parts /etc/cron.hourly)",
			expected: true,
		},
		{
			name:     "CMDEND with full path",
			body:     "(root) CMDEND (/usr/bin/php /var/www/artisan schedule:run)",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsCronLog(tt.body)
			if result != tt.expected {
				t.Errorf("IsCronLog(%q) = %v, want %v", tt.body, result, tt.expected)
			}
		})
	}
}

func TestParseCronLog(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		expected *CronLogInfo
	}{
		{
			name: "CMD with simple command",
			body: "(root) CMD (/usr/libexec/atrun)",
			expected: &CronLogInfo{
				Type:    "cmd",
				User:    "root",
				Command: "/usr/libexec/atrun",
				Message: "cron: root /usr/libexec/atrun",
			},
		},
		{
			name: "CMD with run-parts",
			body: "(root) CMD (cd / && run-parts --report /etc/cron.hourly)",
			expected: &CronLogInfo{
				Type:    "cmd",
				User:    "root",
				Command: "cd / && run-parts --report /etc/cron.hourly",
				Message: "cron: root cd / && run-parts --report /etc/cron.hourly",
			},
		},
		{
			name: "CMD with flock wrapper",
			body: "(root) CMD ((/usr/local/bin/flock -n -E 0 -o /tmp/filter_update_tables.lock /usr/local/opnsense/scripts/filter/update_tables.py --quick) > /dev/null)",
			expected: &CronLogInfo{
				Type:    "cmd",
				User:    "root",
				Command: "(/usr/local/bin/flock -n -E 0 -o /tmp/filter_update_tables.lock /usr/local/opnsense/scripts/filter/update_tables.py --quick) > /dev/null",
				Message: "cron: root /usr/local/opnsense/scripts/filter/update_tables.py --quick",
			},
		},
		{
			name: "PAM session opened",
			body: "pam_unix(cron:session): session opened for user root(uid=0) by (uid=0)",
			expected: &CronLogInfo{
				Type:    "session_open",
				User:    "root",
				Message: "cron session opened: root",
			},
		},
		{
			name: "PAM session closed",
			body: "pam_unix(cron:session): session closed for user root",
			expected: &CronLogInfo{
				Type:    "session_close",
				User:    "root",
				Message: "cron session closed: root",
			},
		},
		{
			name: "CMD with non-root user",
			body: "(www-data) CMD (/usr/bin/php /var/www/artisan schedule:run)",
			expected: &CronLogInfo{
				Type:    "cmd",
				User:    "www-data",
				Command: "/usr/bin/php /var/www/artisan schedule:run",
				Message: "cron: www-data /usr/bin/php /var/www/artisan schedule:run",
			},
		},
		{
			name:     "Non-cron message",
			body:     "some random log message",
			expected: nil,
		},
		{
			name:     "Empty message",
			body:     "",
			expected: nil,
		},
		{
			name: "CMD embedded in RFC5424 syslog message",
			body: "1 2025-12-31T10:05:00.013372-05:00 storage1.homelab.home.example.com /usr/sbin/cron 19467 - - (root) CMD (/usr/libexec/atrun)",
			expected: &CronLogInfo{
				Type:    "cmd",
				User:    "root",
				Command: "/usr/libexec/atrun",
				Message: "cron: root /usr/libexec/atrun",
			},
		},
		{
			name: "run-parts starting",
			body: "(/etc/cron.hourly) starting 0anacron",
			expected: &CronLogInfo{
				Type:    "run_parts_start",
				Command: "0anacron",
				Message: "cron: /etc/cron.hourly starting 0anacron",
			},
		},
		{
			name: "run-parts finished",
			body: "(/etc/cron.hourly) finished 0anacron",
			expected: &CronLogInfo{
				Type:    "run_parts_finish",
				Command: "0anacron",
				Message: "cron: /etc/cron.hourly finished 0anacron",
			},
		},
		{
			name: "run-parts daily starting",
			body: "(/etc/cron.daily) starting logrotate",
			expected: &CronLogInfo{
				Type:    "run_parts_start",
				Command: "logrotate",
				Message: "cron: /etc/cron.daily starting logrotate",
			},
		},
		{
			name: "CMDEND with run-parts",
			body: "(root) CMDEND (run-parts /etc/cron.hourly)",
			expected: &CronLogInfo{
				Type:    "cmd_end",
				User:    "root",
				Command: "run-parts /etc/cron.hourly",
				Message: "cron finished: root run-parts /etc/cron.hourly",
			},
		},
		{
			name: "CMDEND with non-root user",
			body: "(www-data) CMDEND (/usr/bin/php /var/www/artisan schedule:run)",
			expected: &CronLogInfo{
				Type:    "cmd_end",
				User:    "www-data",
				Command: "/usr/bin/php /var/www/artisan schedule:run",
				Message: "cron finished: www-data /usr/bin/php /var/www/artisan schedule:run",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseCronLog(tt.body)

			if tt.expected == nil {
				if result != nil {
					t.Errorf("ParseCronLog(%q) = %+v, want nil", tt.body, result)
				}
				return
			}

			if result == nil {
				t.Errorf("ParseCronLog(%q) = nil, want %+v", tt.body, tt.expected)
				return
			}

			if result.Type != tt.expected.Type {
				t.Errorf("Type = %q, want %q", result.Type, tt.expected.Type)
			}
			if result.User != tt.expected.User {
				t.Errorf("User = %q, want %q", result.User, tt.expected.User)
			}
			if result.Command != tt.expected.Command {
				t.Errorf("Command = %q, want %q", result.Command, tt.expected.Command)
			}
			if result.Message != tt.expected.Message {
				t.Errorf("Message = %q, want %q", result.Message, tt.expected.Message)
			}
		})
	}
}

func TestCleanCommand(t *testing.T) {
	tests := []struct {
		name     string
		cmd      string
		expected string
	}{
		{
			name:     "Simple command",
			cmd:      "/usr/libexec/atrun",
			expected: "/usr/libexec/atrun",
		},
		{
			name:     "Command with > /dev/null",
			cmd:      "/path/to/script > /dev/null",
			expected: "/path/to/script",
		},
		{
			name:     "Command with 2>&1 > /dev/null",
			cmd:      "/path/to/script 2>&1 > /dev/null",
			expected: "/path/to/script",
		},
		{
			name:     "flock wrapper",
			cmd:      "/usr/local/bin/flock -n -E 0 -o /tmp/filter_update_tables.lock /usr/local/opnsense/scripts/filter/update_tables.py --quick",
			expected: "/usr/local/opnsense/scripts/filter/update_tables.py --quick",
		},
		{
			name:     "Wrapped in parens with redirect",
			cmd:      "(/usr/local/bin/flock -n -E 0 -o /tmp/test.lock /path/to/script) > /dev/null",
			expected: "/path/to/script",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cleanCommand(tt.cmd)
			if result != tt.expected {
				t.Errorf("cleanCommand(%q) = %q, want %q", tt.cmd, result, tt.expected)
			}
		})
	}
}
