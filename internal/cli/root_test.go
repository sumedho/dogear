package cli

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sumedho/dogear/internal/dogear"
)

func TestFormatSource(t *testing.T) {
	got := formatSource(dogear.SourceRef{
		Label:       "[2]",
		Title:       "Yamaha DX7 Manual",
		HeadingPath: "MIDI CONFIG",
		PageNumber:  sql.NullInt64{Int64: 58, Valid: true},
		StartLine:   100,
		EndLine:     120,
	})
	want := "[2] | Yamaha DX7 Manual | p.58 | MIDI CONFIG | lines 100-120"
	if got != want {
		t.Fatalf("formatSource() = %q, want %q", got, want)
	}
}

func TestCLIJSONLifecycleAndContext(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "dogear.db")
	md := filepath.Join(dir, "manual.md")
	content := `## CLI Manual

<!-- page: 11 -->
## MIDI CONFIG

Turn local control off in MIDI config.
`
	if err := os.WriteFile(md, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	runCLI(t, "init", "--db", dbPath)
	runCLI(t, "import", md, "--db", dbPath, "--id", "cli-manual", "--tags", "test")

	listOut := runCLI(t, "list", "--db", dbPath, "--json")
	var list []map[string]any
	if err := json.Unmarshal([]byte(listOut), &list); err != nil {
		t.Fatalf("list json: %v\n%s", err, listOut)
	}
	if len(list) != 1 || list[0]["id"] != "cli-manual" {
		t.Fatalf("unexpected list json: %#v", list)
	}

	infoOut := runCLI(t, "info", "cli-manual", "--db", dbPath, "--json")
	var info map[string]any
	if err := json.Unmarshal([]byte(infoOut), &info); err != nil {
		t.Fatalf("info json: %v\n%s", err, infoOut)
	}
	if info["page_count"].(float64) != 1 {
		t.Fatalf("unexpected info json: %#v", info)
	}

	searchOut := runCLI(t, "search", "local control", "--db", dbPath, "--json")
	var search []map[string]any
	if err := json.Unmarshal([]byte(searchOut), &search); err != nil {
		t.Fatalf("search json: %v\n%s", err, searchOut)
	}
	if len(search) == 0 || search[0]["page_number"].(float64) != 11 {
		t.Fatalf("unexpected search json: %#v", search)
	}
	if search[0]["start_line"].(float64) == 0 || search[0]["end_line"].(float64) == 0 {
		t.Fatalf("expected search line range: %#v", search[0])
	}

	contextOut := runCLI(t, "context", "local control", "--db", dbPath, "--json")
	var context map[string]any
	if err := json.Unmarshal([]byte(contextOut), &context); err != nil {
		t.Fatalf("context json: %v\n%s", err, contextOut)
	}
	blocks := context["blocks"].([]any)
	first := blocks[0].(map[string]any)
	source := first["source"].(map[string]any)
	if source["label"] != "[1]" || !strings.Contains(first["text"].(string), "local control") {
		t.Fatalf("unexpected context json: %#v", context)
	}

	debugOut := runCLI(t, "context", "local control", "--db", dbPath, "--json", "--debug")
	var debugContext map[string]any
	if err := json.Unmarshal([]byte(debugOut), &debugContext); err != nil {
		t.Fatalf("debug context json: %v\n%s", err, debugOut)
	}
	debugBlocks := debugContext["blocks"].([]any)
	debugSource := debugBlocks[0].(map[string]any)["source"].(map[string]any)
	if debugSource["debug"] == nil {
		t.Fatalf("expected debug metadata: %#v", debugSource)
	}

	searchDebug := runCLI(t, "search", "local control", "--db", dbPath, "--debug")
	if !strings.Contains(searchDebug, "debug: raw") || !strings.Contains(searchDebug, "quality") {
		t.Fatalf("expected search debug output:\n%s", searchDebug)
	}

	promptOut := runCLI(t, "context", "local control", "--db", dbPath, "--format", "prompt")
	if !strings.Contains(promptOut, "Question: local control") || !strings.Contains(promptOut, "[1] | CLI Manual | p.11 | MIDI CONFIG | lines") {
		t.Fatalf("unexpected prompt context:\n%s", promptOut)
	}

	runCLI(t, "remove", "cli-manual", "--db", dbPath)
	listOut = runCLI(t, "list", "--db", dbPath, "--json")
	if err := json.Unmarshal([]byte(listOut), &list); err != nil {
		t.Fatalf("list json after remove: %v\n%s", err, listOut)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list after remove, got %#v", list)
	}
}

func TestCLIAskDryRunAndProvider(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "dogear.db")
	md := filepath.Join(dir, "manual.md")
	content := `## Ask Manual

<!-- page: 22 -->
## LOCAL CONTROL

Turn local control off from the MIDI settings page.
`
	if err := os.WriteFile(md, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	runCLI(t, "init", "--db", dbPath)
	runCLI(t, "import", md, "--db", dbPath, "--id", "ask-manual")

	dryRun := runCLI(t, "ask", "How do I turn off local control?", "--db", dbPath, "--dry-run")
	var dry map[string]any
	if err := json.Unmarshal([]byte(dryRun), &dry); err != nil {
		t.Fatalf("dry-run json: %v\n%s", err, dryRun)
	}
	if dry["url"] != "http://localhost:11434/v1/chat/completions" {
		t.Fatalf("unexpected dry-run url: %#v", dry)
	}
	body := dry["body"].(map[string]any)
	if body["model"] != "<model>" {
		t.Fatalf("dry-run model = %#v", body["model"])
	}

	missing := runCLIFailure(t, "ask", "How do I turn off local control?", "--db", dbPath)
	if !strings.Contains(missing, "model is required") {
		t.Fatalf("expected missing model error, got %q", missing)
	}

	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		var request struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Set local control off \"}}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"in MIDI settings [1].\"}}]}\n\ndata: [DONE]\n\n"))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Set local control off in MIDI settings [1]."}}]}`))
	}))
	defer server.Close()

	answer := runCLI(t, "ask", "How do I turn off local control?", "--db", dbPath, "--base-url", server.URL+"/v1", "--model", "local-model")
	if !strings.Contains(answer, "Set local control off in MIDI settings [1].") || !strings.Contains(answer, "Sources:") || !strings.Contains(answer, "[1] | Ask Manual | p.22") {
		t.Fatalf("unexpected answer:\n%s", answer)
	}
	if auth != "" {
		t.Fatalf("local auth header = %q, want empty", auth)
	}

	jsonAnswer := runCLI(t, "ask", "How do I turn off local control?", "--db", dbPath, "--base-url", server.URL+"/v1/chat/completions", "--model", "online-model", "--api-key", "secret", "--json")
	if auth != "Bearer secret" {
		t.Fatalf("keyed auth header = %q", auth)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(jsonAnswer), &parsed); err != nil {
		t.Fatalf("ask json: %v\n%s", err, jsonAnswer)
	}
	if parsed["model"] != "online-model" || !strings.Contains(parsed["answer"].(string), "[1]") {
		t.Fatalf("unexpected ask json: %#v", parsed)
	}
}

func TestCLIAskUsesConfigFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "dogear.db")
	configPath := filepath.Join(dir, "config.toml")
	md := filepath.Join(dir, "manual.md")
	content := `## Config Manual

## MIDI

Configure MIDI sync from this section.
`
	if err := os.WriteFile(md, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var seenModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		seenModel = request["model"].(string)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Use MIDI sync [1]."}}]}`))
	}))
	defer server.Close()

	if err := os.WriteFile(configPath, []byte(`[provider]
base_url = "`+server.URL+`/v1"
model = "config-model"
timeout = "5s"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	runCLI(t, "init", "--db", dbPath, "--config", configPath)
	runCLI(t, "import", md, "--db", dbPath, "--config", configPath, "--id", "config-manual")
	answer := runCLI(t, "ask", "How do I configure MIDI sync?", "--db", dbPath, "--config", configPath)
	if seenModel != "config-model" || !strings.Contains(answer, "Use MIDI sync [1].") {
		t.Fatalf("config ask failed; model=%q answer=%s", seenModel, answer)
	}

	runCLI(t, "ask", "How do I configure MIDI sync?", "--db", dbPath, "--config", configPath, "--model", "flag-model")
	if seenModel != "flag-model" {
		t.Fatalf("flag model did not override config: %q", seenModel)
	}
}

func runCLI(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd := newRootCommand(&out, &errOut)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dogear %s failed: %v\nstderr: %s\nstdout: %s", strings.Join(args, " "), err, errOut.String(), out.String())
	}
	return out.String()
}

func runCLIFailure(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd := newRootCommand(&out, &errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("dogear %s succeeded unexpectedly\nstdout: %s", strings.Join(args, " "), out.String())
	}
	return err.Error()
}

func TestLoggingFlagsDoNotPolluteJSONOutput(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "dogear.db")
	runCLI(t, "init", "--db", dbPath)
	var out bytes.Buffer
	var errOut bytes.Buffer
	command := newRootCommand(&out, &errOut)
	command.SetArgs([]string{"list", "--db", dbPath, "--json", "--log-format", "json", "--log-level", "debug"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var documents []any
	if err := json.Unmarshal(out.Bytes(), &documents); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("unexpected diagnostics: %s", errOut.String())
	}
}

func TestLoggingFlagsRejectInvalidValues(t *testing.T) {
	for _, args := range [][]string{{"list", "--log-level", "trace"}, {"list", "--log-format", "yaml"}} {
		var out bytes.Buffer
		var errOut bytes.Buffer
		command := newRootCommand(&out, &errOut)
		command.SetArgs(args)
		if err := command.Execute(); err == nil {
			t.Fatalf("dogear %v succeeded unexpectedly", args)
		}
	}
}
