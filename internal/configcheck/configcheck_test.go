package configcheck

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// errSentinel is a wrapped error errors.Is must still find through Join.
var errSentinel = errors.New("sentinel")

// TestJoin_ShouldDropNilsAndReadLikeErrorsJoin covers Join's contract.
func TestJoin_ShouldDropNilsAndReadLikeErrorsJoin(t *testing.T) {
	a, b := errors.New("a"), fmt.Errorf("b: %w", errSentinel)
	tests := []struct {
		name     string
		errs     []error
		wantNil  bool
		wantText string
	}{
		{name: "should return nil when given nothing", wantNil: true},
		{name: "should return nil when every error is nil", errs: []error{nil, nil}, wantNil: true},
		{name: "should read as errors.Join when given two errors", errs: []error{a, nil, b}, wantText: errors.Join(a, b).Error()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Join(tt.errs...)
			if tt.wantNil {
				if err != nil {
					t.Fatalf("Join() = %v, want nil", err)
				}
				return
			}
			if err.Error() != tt.wantText {
				t.Errorf("Join().Error() = %q, want %q", err.Error(), tt.wantText)
			}
		})
	}

	t.Run("should let errors.Is see a joined error", func(t *testing.T) {
		if !errors.Is(Join(a, b), errSentinel) {
			t.Error("errors.Is did not find the sentinel through Join")
		}
	})
}

// TestSplit_ShouldListOnlyJoinedErrors covers splitting a Join found
// anywhere in the chain, and nothing else.
func TestSplit_ShouldListOnlyJoinedErrors(t *testing.T) {
	a, b := errors.New("a"), errors.New("b")
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "should return nothing when err is nil", err: nil, want: 0},
		{name: "should return err alone when it holds no Join", err: a, want: 1},
		{name: "should not split an error wrapping two with fmt.Errorf", err: fmt.Errorf("%w: %w", a, b), want: 1},
		{name: "should list each error when err is a Join", err: Join(a, b), want: 2},
		{name: "should find a Join under a wrap", err: fmt.Errorf("file: %w", Join(a, b)), want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := len(Split(tt.err)); got != tt.want {
				t.Errorf("len(Split()) = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestReport_Add_ShouldKeepCountsAndValidity covers the counters and the
// exit decision.
func TestReport_Add_ShouldKeepCountsAndValidity(t *testing.T) {
	tests := []struct {
		name       string
		severities []Severity
		wantValid  bool
		wantErrors int
		wantWarns  int
	}{
		{name: "should be valid when there is no finding", wantValid: true},
		{name: "should stay valid when there are only warnings", severities: []Severity{SeverityWarning, SeverityWarning}, wantValid: true, wantWarns: 2},
		{name: "should be invalid when there is an error", severities: []Severity{SeverityWarning, SeverityError}, wantErrors: 1, wantWarns: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New("f.yaml")
			for _, s := range tt.severities {
				r.Add(Finding{Severity: s, Kind: KindValidation, Message: "m"})
			}
			if r.Valid != tt.wantValid || r.Errors != tt.wantErrors || r.Warnings != tt.wantWarns {
				t.Errorf("valid=%v errors=%d warnings=%d, want %v %d %d", r.Valid, r.Errors, r.Warnings, tt.wantValid, tt.wantErrors, tt.wantWarns)
			}
			if gotErr := r.Err(); (gotErr == nil) != tt.wantValid {
				t.Errorf("Err() = %v with valid %v", gotErr, tt.wantValid)
			}
		})
	}

	t.Run("should wrap ErrInvalid when invalid", func(t *testing.T) {
		r := New("")
		r.Add(Finding{Severity: SeverityError})
		if !errors.Is(r.Err(), ErrInvalid) {
			t.Errorf("Err() = %v, want ErrInvalid", r.Err())
		}
	})
}

// TestReport_WriteJSON_ShouldPrintTheDocumentedShape covers the fields CI
// jobs read, including an empty findings list rather than null.
func TestReport_WriteJSON_ShouldPrintTheDocumentedShape(t *testing.T) {
	tests := []struct {
		name    string
		report  func() *Report
		wantKey []string
		noKey   []string
	}{
		{
			name:    "should print an empty findings list when there is nothing to report",
			report:  func() *Report { return New("f.yaml") },
			wantKey: []string{"file", "valid", "errors", "warnings", "findings"},
			noKey:   []string{"sources"},
		},
		{
			name: "should print sources when there are some",
			report: func() *Report {
				r := New("f.yaml")
				r.Sources = []Source{{Key: "k", Value: "v", Origin: "file"}}
				return r
			},
			wantKey: []string{"sources"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := tt.report().WriteJSON(&out); err != nil {
				t.Fatalf("WriteJSON: %v", err)
			}
			var doc map[string]json.RawMessage
			if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
				t.Fatalf("not JSON: %v\n%s", err, out.String())
			}
			for _, k := range tt.wantKey {
				if _, ok := doc[k]; !ok {
					t.Errorf("missing %q in %s", k, out.String())
				}
			}
			for _, k := range tt.noKey {
				if _, ok := doc[k]; ok {
					t.Errorf("unexpected %q in %s", k, out.String())
				}
			}
			if f, ok := doc["findings"]; ok && string(f) == "null" {
				t.Error("findings is null, want []")
			}
		})
	}
}

// TestReport_WriteText_ShouldPrintOneLinePerFinding covers the text an
// operator reads.
func TestReport_WriteText_ShouldPrintOneLinePerFinding(t *testing.T) {
	tests := []struct {
		name    string
		finding Finding
		want    string
	}{
		{
			name:    "should name the file and suggestion when a key is unknown",
			finding: Finding{Severity: SeverityWarning, Kind: KindUnknownKey, Key: "ldap.schmea", File: "/etc/s.yaml", Suggestion: "ldap.schema"},
			want:    "warning  unknown key ldap.schmea in /etc/s.yaml has no effect; did you mean ldap.schema?\n",
		},
		{
			name:    "should prefix the section when one is known",
			finding: Finding{Severity: SeverityError, Kind: KindValidation, Section: "http", Message: "bad url"},
			want:    "error    http: bad url\n",
		},
		{
			name:    "should fold a message that spans lines when it is printed",
			finding: Finding{Severity: SeverityError, Kind: KindLoad, Message: "decoding failed:\n\nbad key"},
			want:    "error    decoding failed: bad key\n",
		},
		{
			name:    "should name the file when the message does not",
			finding: Finding{Severity: SeverityError, Kind: KindLoad, File: "/etc/k", Message: "two lines expected"},
			want:    "error    two lines expected (/etc/k)\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New("")
			r.Add(tt.finding)
			var out bytes.Buffer
			if err := r.WriteText(&out); err != nil {
				t.Fatalf("WriteText: %v", err)
			}
			if !strings.Contains(out.String(), tt.want) {
				t.Errorf("output %q does not contain %q", out.String(), tt.want)
			}
		})
	}

	t.Run("should list sources with their origin when there are some", func(t *testing.T) {
		r := New("f.yaml")
		r.Sources = []Source{{Key: "http.port", Value: "8080", Origin: "default"}}
		var out bytes.Buffer
		if err := r.WriteText(&out); err != nil {
			t.Fatalf("WriteText: %v", err)
		}
		if !strings.Contains(out.String(), "http.port  default  8080") {
			t.Errorf("output %q lacks the source line", out.String())
		}
	})

	t.Run("should report a failed write when the writer fails", func(t *testing.T) {
		if err := New("f.yaml").WriteText(failingWriter{}); err == nil {
			t.Error("WriteText returned no error for a failing writer")
		}
	})
}

// failingWriter fails every write.
type failingWriter struct{}

// Write implements io.Writer.
func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

// TestRecorder_ShouldKeepWarningsAsLines covers the handler `ssoosshd
// config check` swaps in for the process logger.
func TestRecorder_ShouldKeepWarningsAsLines(t *testing.T) {
	tests := []struct {
		name string
		log  func(*slog.Logger)
		want []string
	}{
		{name: "should drop a record when it is below warning", log: func(l *slog.Logger) { l.Info("quiet") }},
		{name: "should keep a warning with its attributes", log: func(l *slog.Logger) { l.Warn("w", "k", "v") }, want: []string{"w k=v"}},
		{name: "should keep an error when one is logged", log: func(l *slog.Logger) { l.Error("e") }, want: []string{"e"}},
		{
			name: "should prefix keys with the group when one is open",
			log:  func(l *slog.Logger) { l.With("a", 1).WithGroup("g").With("b", 2).Warn("w", "c", 3) },
			want: []string{"w a=1 g.b=2 g.c=3"},
		},
		{
			name: "should nest groups when two are open",
			log:  func(l *slog.Logger) { l.WithGroup("g").WithGroup("h").Warn("w", "c", 3) },
			want: []string{"w g.h.c=3"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRecorder()
			tt.log(slog.New(r))
			got := r.Lines()
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("Lines() = %q, want %q", got, tt.want)
			}
		})
	}
}
