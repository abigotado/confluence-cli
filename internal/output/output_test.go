package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/abigotado/confluence-cli/internal/errx"
)

type record struct {
	key, summary, description string
	priority                  int
}

func (i record) Fields() []Field {
	return []Field{
		{Name: "key", Value: i.key, Raw: i.key},
		{Name: "summary", Value: i.summary, Raw: i.summary},
		{Name: "priority", Raw: i.priority},
		{Name: "description", Value: i.description, Raw: i.description, OnRequest: true},
	}
}

func writer(format Format, fields []string) (*Writer, *bytes.Buffer, *bytes.Buffer) {
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	return &Writer{Format: format, Fields: fields, Out: out, Err: stderr}, out, stderr
}

type boundedWriter struct {
	bytes.Buffer
	limit int
	err   error
	calls int
}

func (writer *boundedWriter) Write(payload []byte) (int, error) {
	writer.calls++
	limit := writer.limit
	if limit < 0 || limit > len(payload) {
		limit = len(payload)
	}
	_, _ = writer.Buffer.Write(payload[:limit])
	return limit, writer.err
}

func decodeEnvelope(t *testing.T, out *bytes.Buffer) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	return env
}

func decodeEnvelopeUseNumber(t *testing.T, out *bytes.Buffer) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
	decoder.UseNumber()
	var env map[string]any
	if err := decoder.Decode(&env); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	return env
}

func TestSuccessEnvelopeAndCompactProjection(t *testing.T) {
	w, out, stderr := writer(FormatJSON, nil)
	w.WithContext("work", "https://example.atlassian.net")
	if err := w.Success(record{key: "123", summary: "Ship it", description: "long", priority: 2}); err != nil {
		t.Fatalf("Success: %v", err)
	}
	env := decodeEnvelope(t, out)
	if env["ok"] != true || env["v"] != float64(1) {
		t.Errorf("contract fields = %v", env)
	}
	data := env["data"].(map[string]any)
	if _, ok := data["description"]; ok {
		t.Error("on-request description leaked into compact output")
	}
	if data["priority"] != float64(2) {
		t.Errorf("priority lost its numeric type: %v", data["priority"])
	}
	meta := env["meta"].(map[string]any)
	if meta["profile"] != "work" || meta["site"] != "https://example.atlassian.net" {
		t.Errorf("invocation context = %v", meta)
	}
	if _, ok := meta["count"]; ok {
		t.Error("single object pretends to be a collection")
	}
	if stderr.Len() != 0 {
		t.Errorf("JSON success wrote stderr: %q", stderr.String())
	}
	if strings.Count(out.String(), "\n") != 1 {
		t.Errorf("JSON envelope should be one compact line: %q", out.String())
	}
}

func TestCollectionAndPaginationMetadata(t *testing.T) {
	tests := []struct {
		name       string
		emit       func(*Writer) error
		wantCount  float64
		wantTrunc  bool
		wantCursor string
	}{
		{"empty complete collection", func(w *Writer) error { return w.Success([]record{}) }, 0, false, ""},
		{"page with cursor", func(w *Writer) error { return w.SuccessPage([]record{{key: "123"}}, true, "opaque") }, 1, true, "opaque"},
		{"last page", func(w *Writer) error { return w.SuccessPage([]record{{key: "123"}}, false, "") }, 1, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, out, _ := writer(FormatJSON, nil)
			if err := tt.emit(w); err != nil {
				t.Fatalf("emit: %v", err)
			}
			meta := decodeEnvelope(t, out)["meta"].(map[string]any)
			if meta["count"] != tt.wantCount || meta["truncated"] != tt.wantTrunc {
				t.Errorf("meta = %v", meta)
			}
			if got, _ := meta["next_cursor"].(string); got != tt.wantCursor {
				t.Errorf("next_cursor = %q, want %q", got, tt.wantCursor)
			}
		})
	}
}

func TestV1MetadataWireTypesAndFailureOmission(t *testing.T) {
	w, out, _ := writer(FormatJSON, nil)
	w.WithContext("work", "https://example.atlassian.net")
	if err := w.SuccessPage([]record{{key: "123"}}, true, "opaque"); err != nil {
		t.Fatalf("SuccessPage: %v", err)
	}
	env := decodeEnvelopeUseNumber(t, out)
	meta, ok := env["meta"].(map[string]any)
	if !ok || meta == nil {
		t.Fatalf("meta = %#v, want non-null object", env["meta"])
	}
	count, ok := meta["count"].(json.Number)
	if !ok || strings.ContainsAny(count.String(), ".eE") {
		t.Fatalf("meta.count = %#v, want integral JSON number", meta["count"])
	}
	parsedCount, err := strconv.ParseInt(count.String(), 10, 64)
	if err != nil || parsedCount < 0 {
		t.Fatalf("meta.count = %q, want non-negative integer: %v", count, err)
	}
	if _, ok := meta["truncated"].(bool); !ok {
		t.Fatalf("meta.truncated = %#v, want boolean", meta["truncated"])
	}
	for _, field := range []string{"next_cursor", "profile", "site"} {
		if value, ok := meta[field].(string); !ok || value == "" {
			t.Errorf("meta.%s = %#v, want emitted non-null string", field, meta[field])
		}
	}

	failure, failureOut, _ := writer(FormatJSON, nil)
	failure.Failure(errx.Usage("bad"))
	if _, present := decodeEnvelopeUseNumber(t, failureOut)["meta"]; present {
		t.Fatal("v1 failure envelope unexpectedly emitted meta")
	}
}

func TestProjectionValidationAndShape(t *testing.T) {
	tests := []struct {
		name    string
		format  Format
		fields  []string
		data    any
		wantErr bool
	}{
		{"select on request field", FormatJSON, []string{"key", "description"}, record{key: "123", description: "body"}, false},
		{"unknown JSON field", FormatJSON, []string{"bogus"}, record{}, true},
		{"unknown text field", FormatText, []string{"bogus"}, record{}, true},
		{"fields with raw", FormatRaw, []string{"key"}, record{}, true},
		{"fields on static payload", FormatJSON, []string{"key"}, struct{ A int }{1}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, out, _ := writer(tt.format, tt.fields)
			err := w.Success(tt.data)
			if tt.wantErr {
				if errx.ExitCode(err) != errx.CodeUsage {
					t.Fatalf("error = %v, code = %d", err, errx.ExitCode(err))
				}
				return
			}
			if err != nil {
				t.Fatalf("Success: %v", err)
			}
			env := decodeEnvelope(t, out)
			data := env["data"].(map[string]any)
			if len(data) != 2 || data["description"] != "body" {
				t.Errorf("projection = %v", data)
			}
		})
	}
}

func TestValidateChecksProjectionWithoutWriting(t *testing.T) {
	tests := []struct {
		name    string
		format  Format
		fields  []string
		data    any
		wantErr bool
	}{
		{name: "known field", format: FormatJSON, fields: []string{"key"}, data: record{}},
		{name: "unknown field", format: FormatText, fields: []string{"bogus"}, data: record{}, wantErr: true},
		{name: "raw projection", format: FormatRaw, fields: []string{"key"}, data: record{}, wantErr: true},
		{name: "static projection", format: FormatJSON, fields: []string{"key"}, data: map[string]any{}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, out, stderr := writer(tt.format, tt.fields)
			err := w.Validate(tt.data)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr=%v", err, tt.wantErr)
			}
			if out.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("Validate wrote output: stdout=%q stderr=%q", out.String(), stderr.String())
			}
		})
	}
}

func TestSuccessStagesEveryFormatIntoOneWrite(t *testing.T) {
	tests := []struct {
		name   string
		format Format
		data   any
	}{
		{name: "json", format: FormatJSON, data: []record{{key: "123"}, {key: "456"}}},
		{name: "raw", format: FormatRaw, data: map[string]any{"key": "123"}},
		{name: "text", format: FormatText, data: []record{{key: "123"}, {key: "456"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out := &boundedWriter{limit: -1}
			stderr := &bytes.Buffer{}
			w := &Writer{Format: test.format, Out: out, Err: stderr}
			if err := w.Success(test.data); err != nil {
				t.Fatalf("Success: %v", err)
			}
			if out.calls != 1 {
				t.Fatalf("stdout writes=%d, want 1", out.calls)
			}
			if out.Len() == 0 || stderr.Len() != 0 {
				t.Fatalf("stdout=%q stderr=%q", out.String(), stderr.String())
			}
		})
	}
}

func TestFailureAfterSuccessEmissionErrorDoesNotWriteSecondEnvelope(t *testing.T) {
	tests := []struct {
		name      string
		format    Format
		limit     int
		err       error
		operation string
	}{
		{name: "zero", format: FormatJSON, limit: 0, err: errors.New("closed output"), operation: "pages.create"},
		{name: "partial", format: FormatRaw, limit: 2, err: errors.New("interrupted output"), operation: "pages.update"},
		{name: "short", format: FormatText, limit: 2, operation: "pages.create"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out := &boundedWriter{limit: test.limit, err: test.err}
			stderr := &bytes.Buffer{}
			w := &Writer{Format: test.format, Out: out, Err: stderr}
			err := w.Success(record{key: "123", summary: "Created"})
			if err == nil {
				t.Fatal("Success error=nil, want emission failure")
			}
			beforeFailure := out.String()
			code := w.Failure(errx.WriteAppliedLocalFailure(test.operation).Wrap(err))
			if code != errx.CodeInternal {
				t.Fatalf("Failure code=%d, want %d", code, errx.CodeInternal)
			}
			if out.calls != 1 {
				t.Fatalf("stdout writes=%d, want 1", out.calls)
			}
			if out.String() != beforeFailure {
				t.Fatalf("Failure appended stdout: before=%q after=%q", beforeFailure, out.String())
			}
			wantStderr := "error: WRITE_APPLIED_LOCAL_FAILURE: " + test.operation + " applied, but local finalization failed\n" +
				"hint: the Confluence write applied; do not retry it; report the local failure\n"
			if stderr.String() != wantStderr {
				t.Errorf("stderr=%q, want exact diagnostic %q", stderr.String(), wantStderr)
			}
		})
	}
}

func TestConcreteSliceAndObjectShapesAreStable(t *testing.T) {
	tests := []struct {
		name      string
		data      any
		wantArray bool
	}{
		{"object", record{key: "123"}, false},
		{"concrete slice", []record{{key: "123"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, out, _ := writer(FormatJSON, []string{"key"})
			if err := w.Success(tt.data); err != nil {
				t.Fatalf("Success: %v", err)
			}
			var env struct {
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(out.Bytes(), &env); err != nil {
				t.Fatal(err)
			}
			isArray := strings.HasPrefix(strings.TrimSpace(string(env.Data)), "[")
			if isArray != tt.wantArray {
				t.Errorf("data = %s, wantArray=%v", env.Data, tt.wantArray)
			}
		})
	}
}

func TestFailureEnvelopeAndExitStatus(t *testing.T) {
	candidates := []errx.Candidate{{ID: "10000", Name: "Work", Kind: "project"}}
	tests := []struct {
		name       string
		err        error
		wantStatus errx.Code
		wantReason string
		wantDetail string
	}{
		{"ambiguous", errx.Ambiguous("project", "W", candidates), errx.CodeAmbiguous, "AMBIGUOUS_PROJECT", "candidates"},
		{"not found", errx.NotFound("project", "Wrok", candidates), errx.CodeNotFound, "NOT_FOUND_PROJECT", "did_you_mean"},
		{"permission", errx.Permission("SCOPE_DENIED", "missing scope"), errx.CodePermission, "SCOPE_DENIED", ""},
		{"conflict", errx.Conflict("STALE_PAGE", "changed"), errx.CodeConflict, "STALE_PAGE", ""},
		{"retryable", errx.Retryable("RATE_LIMITED", time.Second, "slow"), errx.CodeRetryable, "RATE_LIMITED", "retry_after"},
		{"raw error", errors.New("boom"), errx.CodeInternal, "INTERNAL", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, out, stderr := writer(FormatJSON, nil)
			if got := w.Failure(tt.err); got != tt.wantStatus {
				t.Errorf("status = %d, want %d", got, tt.wantStatus)
			}
			env := decodeEnvelope(t, out)
			body := env["error"].(map[string]any)
			if body["code"] != tt.wantReason || env["hint"] == "" {
				t.Errorf("failure = %v", env)
			}
			if tt.wantDetail != "" {
				if _, ok := body[tt.wantDetail]; !ok {
					t.Errorf("error body missing %s: %v", tt.wantDetail, body)
				}
			}
			if stderr.Len() != 0 {
				t.Errorf("JSON failure wrote stderr: %q", stderr.String())
			}
		})
	}
}

func TestJSONEnvelopeKeySetsArePinned(t *testing.T) {
	tests := []struct {
		name string
		emit func(*Writer) error
		want []string
	}{
		{"object", func(w *Writer) error { return w.Success(record{}) }, []string{"data", "ok", "v"}},
		{"collection", func(w *Writer) error { return w.Success([]record{}) }, []string{"data", "meta", "ok", "v"}},
		{"failure", func(w *Writer) error { w.Failure(errx.Usage("bad")); return nil }, []string{"error", "hint", "ok", "v"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, out, _ := writer(FormatJSON, nil)
			if err := tt.emit(w); err != nil {
				t.Fatal(err)
			}
			env := decodeEnvelope(t, out)
			got := make([]string, 0, len(env))
			for key := range env {
				got = append(got, key)
			}
			sort.Strings(got)
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("keys = %v, want %v; assess an envelope version bump", got, tt.want)
			}
		})
	}
}

func TestTextOutputIsOneLinePerEntity(t *testing.T) {
	w, out, stderr := writer(FormatText, []string{"key", "description", "priority"})
	rows := []record{
		{key: "123", description: "first\nsecond", priority: 1},
		{key: "456", priority: 2},
	}
	if err := w.Success(rows); err != nil {
		t.Fatalf("Success: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "first second") {
		t.Errorf("text output = %q", out.String())
	}
	for index, line := range lines {
		if len(strings.Split(line, "  ")) != 3 {
			t.Errorf("line %d lost a projected column: %q", index, line)
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("text success wrote stderr: %q", stderr.String())
	}
}

func TestTextOutputEscapesTerminalControlsButJSONPreservesData(t *testing.T) {
	const hostile = "safe\x1b]52;c;clipboard\a\u0085\u202Eend"
	textWriter, textOut, _ := writer(FormatText, nil)
	if err := textWriter.Success(record{summary: hostile}); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"\x1b", "\a", "\u0085", "\u202E"} {
		if strings.Contains(textOut.String(), forbidden) {
			t.Fatalf("text output contains terminal control %q: %q", forbidden, textOut.String())
		}
	}
	for _, escaped := range []string{"\\u{1B}", "\\u{7}", "\\u{85}", "\\u{202E}"} {
		if !strings.Contains(textOut.String(), escaped) {
			t.Fatalf("text output missing escaped marker %q: %q", escaped, textOut.String())
		}
	}

	jsonWriter, jsonOut, _ := writer(FormatJSON, []string{"summary"})
	if err := jsonWriter.Success(record{summary: hostile}); err != nil {
		t.Fatal(err)
	}
	data := decodeEnvelope(t, jsonOut)["data"].(map[string]any)
	if data["summary"] != hostile {
		t.Fatalf("JSON changed untrusted data: %#v", data["summary"])
	}
}

func TestTextFailureUsesOnlyStderr(t *testing.T) {
	w, out, stderr := writer(FormatText, nil)
	w.Failure(errx.Ambiguous("project", "W", []errx.Candidate{{ID: "1", Name: "Work"}}))
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
	for _, want := range []string{"error:", "hint:", "Work"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr missing %q: %q", want, stderr.String())
		}
	}
}

func TestParseFormat(t *testing.T) {
	tests := []struct {
		value   string
		want    Format
		wantErr bool
	}{
		{"text", FormatText, false}, {"json", FormatJSON, false}, {"raw", FormatRaw, false},
		{"", "", true}, {"JSON", "", true}, {"yaml", "", true},
	}
	for _, tt := range tests {
		t.Run("value="+tt.value, func(t *testing.T) {
			got, err := ParseFormat(tt.value)
			if tt.wantErr {
				if errx.ExitCode(err) != errx.CodeUsage {
					t.Errorf("error = %v", err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("ParseFormat = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestDefaultFormatForRegularAndClosedFiles(t *testing.T) {
	file, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	if got := DefaultFormat(file); got != FormatJSON {
		t.Errorf("regular file = %q", got)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if got := DefaultFormat(file); got != FormatJSON {
		t.Errorf("closed file = %q", got)
	}
}

func TestUnsupportedWriterFormatFailsInternal(t *testing.T) {
	w, _, _ := writer(Format("xml"), nil)
	if err := w.Success(record{}); errx.ExitCode(err) != errx.CodeInternal {
		t.Errorf("error = %v, code = %d", err, errx.ExitCode(err))
	}
}
