package cmd

// These tests swap the process-wide slog default (the check records the
// warnings validation logs) and set environment variables, so none of
// them runs in parallel.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/internal/configcheck"
)

// checkResult is one run of `ssoosshd config check` under test.
type checkResult struct {
	path   string
	stdout string
	err    error
}

// runConfigCheck writes configYAML to a temporary file and runs
// `ssoosshd -c <file> config check <args>` against it.
func runConfigCheck(t *testing.T, configYAML string, args ...string) checkResult {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ssoosshd.yaml")
	if err := os.WriteFile(path, []byte(configYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return runConfigCheckAt(t, path, args...)
}

// runConfigCheckAt runs `ssoosshd -c <path> config check <args>`.
func runConfigCheckAt(t *testing.T, path string, args ...string) checkResult {
	t.Helper()
	root := &RootCommand{commands: []simplecobra.Commander{newConfigCommand()}}
	exec, err := simplecobra.New(root)
	if err != nil {
		t.Fatalf("build the command tree: %v", err)
	}
	var stdout, stderr bytes.Buffer
	root.CobraCommand().SetOut(&stdout)
	root.CobraCommand().SetErr(&stderr)

	_, err = exec.Execute(context.Background(), append([]string{"-c", path, "config", "check"}, args...))
	return checkResult{path: path, stdout: stdout.String(), err: err}
}

// jsonReport decodes a --json run's output.
func jsonReport(t *testing.T, r checkResult) configcheck.Report {
	t.Helper()
	var report configcheck.Report
	if err := json.Unmarshal([]byte(r.stdout), &report); err != nil {
		t.Fatalf("output is not a JSON report: %v\n%s", err, r.stdout)
	}
	return report
}

// httpsOnly is the smallest config that draws no warning at all.
const httpsOnly = "http:\n  public_url: https://ssh.example.com\n"

// TestConfigCheck_ShouldPassWhenTheConfigurationIsValid covers exit 0.
func TestConfigCheck_ShouldPassWhenTheConfigurationIsValid(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{name: "should report valid when nothing is wrong", yaml: httpsOnly, want: "0 errors, 0 warnings: the configuration is valid"},
		{name: "should still pass when there are only warnings", yaml: "", want: "0 errors, 1 warning: the configuration is valid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := runConfigCheck(t, tt.yaml)
			if r.err != nil {
				t.Fatalf("config check failed: %v\n%s", r.err, r.stdout)
			}
			if !strings.Contains(r.stdout, tt.want) {
				t.Errorf("output %q lacks %q", r.stdout, tt.want)
			}
		})
	}
}

// TestConfigCheck_ShouldReportAnUnknownKey covers the unknown key: a
// warning naming the file and the closest key, an error under --strict.
func TestConfigCheck_ShouldReportAnUnknownKey(t *testing.T) {
	yaml := httpsOnly + "ldap:\n  schmea: lldap\n"
	tests := []struct {
		name     string
		args     []string
		wantFail bool
		severity configcheck.Severity
	}{
		{name: "should warn when the key is unknown", severity: configcheck.SeverityWarning},
		{name: "should fail when the key is unknown under --strict", args: []string{"--strict"}, wantFail: true, severity: configcheck.SeverityError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := runConfigCheck(t, yaml, append(tt.args, "--json")...)
			if failed := r.err != nil; failed != tt.wantFail {
				t.Fatalf("err = %v, want failure %v", r.err, tt.wantFail)
			}
			report := jsonReport(t, r)
			if len(report.Findings) != 1 {
				t.Fatalf("findings = %+v, want one", report.Findings)
			}
			want := configcheck.Finding{
				Severity: tt.severity, Kind: configcheck.KindUnknownKey, Key: "ldap.schmea", File: r.path,
				Suggestion: "ldap.schema", Message: report.Findings[0].Message,
			}
			if report.Findings[0] != want {
				t.Errorf("finding = %+v, want %+v", report.Findings[0], want)
			}
		})
	}

	t.Run("should name the file and the suggestion in text when the key is unknown", func(t *testing.T) {
		r := runConfigCheck(t, yaml)
		want := "unknown key ldap.schmea in " + r.path + " has no effect; did you mean ldap.schema?"
		if !strings.Contains(r.stdout, want) {
			t.Errorf("output %q lacks %q", r.stdout, want)
		}
	})
}

// TestConfigCheck_ShouldReportErrorsFromEverySection covers the check not
// stopping at the first problem.
func TestConfigCheck_ShouldReportErrorsFromEverySection(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.pem")
	templates := t.TempDir()
	if err := os.WriteFile(filepath.Join(templates, "no-such-kind.txt.tmpl"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	mailOn := "mail:\n  enabled: true\n  from: ssoossh@example.com\n  smtp:\n    host: localhost\n    tls: \"off\"\n    auth: none\n"
	tests := []struct {
		name string
		yaml string
		want []string
	}{
		{
			name: "should report both when two sections are invalid",
			yaml: "http:\n  public_url: https://ssh.example.com/sub\ncert_options:\n  client_timeout: 0s\n",
			want: []string{"http", "cert_options"},
		},
		{
			name: "should report the TLS pair when its files are missing",
			yaml: httpsOnly + "  tls:\n    certificate_file: " + missing + "\n    private_key_file: " + missing + "\n",
			want: []string{"http"},
		},
		{
			name: "should report a mail template the notification handler would refuse",
			yaml: httpsOnly + mailOn + "  template_dir: " + templates + "\n",
			want: []string{"mail"},
		},
		{
			name: "should not check a section further when its own validation failed",
			yaml: httpsOnly + "cert_options:\n  client_timeout: 0s\n  user:\n    key_id_template: \"{{.Username\"\n",
			want: []string{"cert_options"},
		},
		{
			name: "should report a lifetime policy the certificate service would refuse",
			yaml: httpsOnly + "cert_options:\n  user:\n    lifetime_policy:\n      tiers:\n        - when: {group: admins}\n          max_duration: 1h\n",
			want: []string{"cert_options"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := runConfigCheck(t, tt.yaml, "--json")
			if !errors.Is(r.err, configcheck.ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", r.err)
			}
			var got []string
			for _, f := range jsonReport(t, r).Findings {
				if f.Severity == configcheck.SeverityError {
					got = append(got, f.Section)
				}
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("error sections = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestConfigCheck_ShouldPrintTheJSONShape covers the machine-readable
// report's top-level fields.
func TestConfigCheck_ShouldPrintTheJSONShape(t *testing.T) {
	r := runConfigCheck(t, "cert_options:\n  client_timeout: 0s\n", "--json")
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, r.stdout)
	}
	want := map[string]string{"file": strconv.Quote(r.path), "valid": "false", "errors": "1", "warnings": "1"}
	for key, value := range want {
		if string(doc[key]) != value {
			t.Errorf("%s = %s, want %s", key, doc[key], value)
		}
	}
	if _, ok := doc["sources"]; ok {
		t.Error("sources present without --show-sources")
	}
}

// TestConfigCheck_ShouldReadTheEnvironmentAsServeDoes covers the one
// environment input on the validation path: SSH_AUTH_SOCK stands in for an
// unset ssh_key_agent.socket, in the check exactly as in a start.
func TestConfigCheck_ShouldReadTheEnvironmentAsServeDoes(t *testing.T) {
	yaml := httpsOnly + "ssh_key_agent:\n  key_fingerprint: SHA256:WdJsHl3uxM2S7QwPQKBXaHAW8bKNToZMbzrcGq4DZvA\n"
	tests := []struct {
		name     string
		sock     string
		wantFail bool
	}{
		{name: "should pass when SSH_AUTH_SOCK names the agent", sock: "/run/ssoossh/agent.sock"},
		{name: "should fail when neither the socket nor SSH_AUTH_SOCK is set", sock: "", wantFail: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SSH_AUTH_SOCK", tt.sock)
			r := runConfigCheck(t, yaml)
			if failed := r.err != nil; failed != tt.wantFail {
				t.Errorf("err = %v, want failure %v\n%s", r.err, tt.wantFail, r.stdout)
			}
		})
	}
}

// TestConfigCheck_ShouldShowWhereEachValueCameFrom covers --show-sources:
// the preset layer, and secrets redacted as on the admin page.
func TestConfigCheck_ShouldShowWhereEachValueCameFrom(t *testing.T) {
	secrets := []string{"a-cookie-secret-value", "an-oidc-client-secret", "a-bind-password"}
	yaml := `http:
  public_url: https://ssh.example.com
  cookie_key: ` + secrets[0] + `
authentication:
  client_secret: ` + secrets[1] + `
ldap:
  enabled: true
  schema: active-directory
  url: ldaps://ldap.example.com
  base_dn: dc=example,dc=com
  user_filter: (sAMAccountName={{.Username}})
  bind_password: ` + secrets[2] + `
`
	r := runConfigCheck(t, yaml, "--show-sources", "--json")
	if r.err != nil {
		t.Fatalf("config check failed: %v\n%s", r.err, r.stdout)
	}
	origins := map[string]configcheck.Source{}
	for _, s := range jsonReport(t, r).Sources {
		origins[s.Key] = s
	}

	tests := []struct {
		name string
		key  string
		want string
	}{
		{name: "should say preset when the schema preset set the value", key: "ldap.id_attribute", want: "preset"},
		{name: "should say file when the file set the value", key: "ldap.schema", want: "file"},
		{name: "should say default when nothing above defaults set the value", key: "http.port", want: "default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := origins[tt.key].Origin; got != tt.want {
				t.Errorf("%s origin = %q, want %q", tt.key, got, tt.want)
			}
		})
	}

	for _, format := range [][]string{{"--show-sources"}, {"--show-sources", "--json"}} {
		t.Run("should redact every secret when printing "+strings.Join(format, " "), func(t *testing.T) {
			out := runConfigCheck(t, yaml, format...).stdout
			for _, secret := range secrets {
				if strings.Contains(out, secret) {
					t.Errorf("output discloses %q", secret)
				}
			}
			if !strings.Contains(out, "[redacted]") {
				t.Error("output shows no [redacted] value")
			}
		})
	}
}

// TestConfigCheck_ShouldReportTheWarningsAStartLogs covers warnings that
// validation logs as it runs, recorded rather than printed to the log, and
// the process logger being put back afterwards.
func TestConfigCheck_ShouldReportTheWarningsAStartLogs(t *testing.T) {
	before := slog.Default()
	yaml := httpsOnly + `ldap:
  enabled: true
  url: ldaps://ldap.example.com
  base_dn: dc=example,dc=com
  user_filter: (uid={{.Username}})
  tls_insecure_skip_verify: true
`
	r := runConfigCheck(t, yaml)
	if r.err != nil {
		t.Fatalf("config check failed: %v\n%s", r.err, r.stdout)
	}
	if !strings.Contains(r.stdout, "warning  ldap.tls_insecure_skip_verify is enabled") {
		t.Errorf("output %q lacks the insecure LDAP warning", r.stdout)
	}
	if slog.Default() != before {
		t.Error("the process logger was not restored")
	}
}

// TestConfigCheck_ShouldFailWhenTheFileCannotBeLoaded covers a missing
// file: one load finding and a non-zero exit.
func TestConfigCheck_ShouldFailWhenTheFileCannotBeLoaded(t *testing.T) {
	r := runConfigCheckAt(t, filepath.Join(t.TempDir(), "absent.yaml"), "--json")
	if !errors.Is(r.err, configcheck.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", r.err)
	}
	findings := jsonReport(t, r).Findings
	if len(findings) != 1 || findings[0].Kind != configcheck.KindLoad {
		t.Errorf("findings = %+v, want one load finding", findings)
	}
}

// TestConfigCheck_ShouldContactNothingWhenEveryServiceIsUnreachable covers
// "never contacts LDAP, the database, NATS, the CA or the network": every
// endpoint is in TEST-NET-1 (RFC 5737), where a connection attempt hangs
// until it times out, so a check that tried any of them could not finish
// quickly, and a check that needed one could not pass.
func TestConfigCheck_ShouldContactNothingWhenEveryServiceIsUnreachable(t *testing.T) {
	dir := t.TempDir()
	certs := map[string]string{}
	for _, name := range []string{"cert", "key", "ca"} {
		path := filepath.Join(dir, name+".pem")
		if err := os.WriteFile(path, []byte("not read by the check"), 0o600); err != nil {
			t.Fatal(err)
		}
		certs[name] = path
	}
	yaml := `http:
  public_url: https://ssh.example.com
  cookie_key: a-shared-cookie-key
multi_instance: true
db:
  provider: postgres
  connection_string: "host=192.0.2.1 port=5432 user=ssoossh dbname=ssoossh connect_timeout=30"
authentication:
  provider_url: https://192.0.2.1/realms/ssoossh
  client_id: ssoossh
  client_secret: a-client-secret
ldap:
  enabled: true
  url: ldaps://192.0.2.1:636
  base_dn: dc=example,dc=com
  user_filter: (uid={{.Username}})
pubsub:
  backend: nats
  nats:
    url: nats://192.0.2.1:4222
    cert_file: ` + certs["cert"] + `
    key_file: ` + certs["key"] + `
    ca_file: ` + certs["ca"] + `
ssh_key_agent:
  socket: ` + filepath.Join(dir, "no-agent.sock") + `
mail:
  enabled: true
  from: ssoossh@example.com
  smtp:
    host: 192.0.2.1
    tls: required
    auth: none
`
	start := time.Now()
	r := runConfigCheck(t, yaml, "--json")
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("config check took %v: it must not wait on any connection", elapsed)
	}
	if r.err != nil {
		t.Fatalf("config check failed: %v\n%s", r.err, r.stdout)
	}
}

// TestConfigCheck_ShouldNotImportAnythingThatConnects is the static half
// of the test above: the command's own file imports no package that opens
// a database, a broker, a directory or a CA key source.
func TestConfigCheck_ShouldNotImportAnythingThatConnects(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "configcheck.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse configcheck.go: %v", err)
	}
	forbidden := []string{
		"github.com/mnestor/ssoossh/server/bootstrap",
		"github.com/mnestor/ssoossh/server/pubsub",
		"github.com/mnestor/ssoossh/server/signer",
		"gorm.io/",
		"github.com/go-ldap/",
		"github.com/nats-io/",
		"net/http",
	}
	for _, spec := range file.Imports {
		path, _ := strconv.Unquote(spec.Path.Value)
		for _, bad := range forbidden {
			if strings.HasPrefix(path, bad) {
				t.Errorf("configcheck.go imports %s", path)
			}
		}
	}
}
