package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abigotado/confluence-cli/internal/errx"
)

func TestGeneratedContractIsCurrent(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	want := renderMarkdown(errx.Describe())
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join(root, target))
			if err != nil {
				t.Fatalf("read generated contract: %v", err)
			}
			if string(content) != want {
				t.Errorf("%s is stale; run go generate ./...", target)
			}
		})
	}
}

func TestRenderContainsEveryExitCodeAndMetadataField(t *testing.T) {
	got := renderMarkdown(errx.Describe())
	for _, info := range errx.Codes() {
		if !strings.Contains(got, "`"+info.Name+"`") || !strings.Contains(got, info.NextMove) {
			t.Errorf("rendered contract omits code %d (%s)", info.Code, info.Name)
		}
	}
	for _, field := range []string{"count", "truncated", "next_cursor", "profile", "site"} {
		if !strings.Contains(got, `"`+field+`"`) {
			t.Errorf("rendered contract omits meta.%s", field)
		}
	}
	if !strings.Contains(got, generatedNotice) {
		t.Error("rendered contract lacks generated notice")
	}
}

func TestRenderContainsStrictV1GrammarBeforeWriteRecovery(t *testing.T) {
	got := strings.Join(strings.Fields(renderMarkdown(errx.Describe())), " ")
	ordered := []string{
		"A v1 envelope is exactly one complete top-level JSON object followed only by whitespace.",
		"It requires boolean `ok` and a JSON integer `v` whose value is exactly `1`",
		"If `ok` is `true`, `data` must be present and non-null; `error` and `hint` must be absent, and their presence is invalid even when `null`.",
		"If `ok` is `false`, `data` must be absent; `error` must be a present non-null object with string `code` and `message`, and `hint` must be a present string.",
		"Forbidden-key presence is invalid even when `null`.",
		"`meta` is optional; unknown additive fields are tolerated. They never repair an invalid known member.",
		"On either branch, `meta` may be absent; when present it must be a non-null object and may be empty.",
		"Every present known metadata member must be non-null and have its exact v1 type: `count` is a nonnegative JSON integer written without a fraction or exponent, `truncated` is boolean, and `next_cursor`, `profile`, and `site` are strings.",
		"Unknown additive metadata members are tolerated, but they never repair a missing, null, or wrongly typed known member.",
		"A malformed `meta` makes the entire envelope invalid.",
		"Any malformed or unsupported envelope makes stdout invalid.",
		"Valid v1 examples:",
	}
	assertOrderedPhrases(t, got, ordered)

	validStart := strings.Index(got, "Valid v1 examples:")
	invalidStart := strings.Index(got, "Invalid examples:")
	if validStart < 0 || invalidStart <= validStart {
		t.Fatalf("rendered contract fixture sections are missing or out of order: valid=%d invalid=%d", validStart, invalidStart)
	}
	validSection := got[validStart:invalidStart]
	for _, fixture := range []string{
		`{"ok":true,"v":1,"data":{},"meta":{},"future":true}`,
		`{"ok":false,"v":1,"error":{"code":"PROFILE_REQUIRED","message":"profile is required","future":true},"hint":"pass --profile","meta":{"count":0,"truncated":false,"next_cursor":"","profile":"work","site":"https://example.atlassian.net","future":null},"future":true}`,
	} {
		if !strings.Contains(validSection, fixture) {
			t.Errorf("rendered contract valid section lacks fixture %s", fixture)
		}
	}

	fallbackStart := strings.Index(got, "Only for a `confluence-cli pages create` or `confluence-cli pages update`")
	if fallbackStart <= invalidStart {
		t.Fatalf("rendered contract fallback does not follow invalid fixtures: invalid=%d fallback=%d", invalidStart, fallbackStart)
	}
	invalidSection := got[invalidStart:fallbackStart]
	for _, fixture := range []string{
		`{"ok":true,"v":2,"data":{}}`,
		`{"ok":true,"v":1.0,"data":{}}`,
		`{"ok":"true","v":1,"data":{}}`,
		`{"ok":true,"v":"1","data":{}}`,
		`{"ok":true,"v":1}`,
		`{"ok":true,"v":1,"data":null}`,
		`{"ok":true,"v":1,"data":{},"error":null}`,
		`{"ok":true,"v":1,"data":{},"hint":null}`,
		`{"ok":false,"v":1,"hint":"stop"}`,
		`{"ok":false,"v":1,"error":null,"hint":"stop"}`,
		`{"ok":false,"v":1,"error":{"code":1,"message":"failed"},"hint":"stop"}`,
		`{"ok":false,"v":1,"error":{"code":"FAILED","message":1},"hint":"stop"}`,
		`{"ok":false,"v":1,"error":{"code":"FAILED","message":"failed"},"hint":1}`,
		`{"ok":false,"v":1,"error":{"code":"FAILED","message":"failed"},"hint":"stop","data":null}`,
		`{"ok":true,"v":1,"data":{},"meta":null}`,
		`{"ok":true,"v":1,"data":{},"meta":[]}`,
		`{"ok":true,"v":1,"data":{},"meta":{"count":null}}`,
		`{"ok":true,"v":1,"data":{},"meta":{"count":-1}}`,
		`{"ok":true,"v":1,"data":{},"meta":{"count":1.0}}`,
		`{"ok":true,"v":1,"data":{},"meta":{"count":1e0}}`,
		`{"ok":true,"v":1,"data":{},"meta":{"count":"1"}}`,
		`{"ok":true,"v":1,"data":{},"meta":{"truncated":null}}`,
		`{"ok":true,"v":1,"data":{},"meta":{"truncated":"false"}}`,
		`{"ok":true,"v":1,"data":{},"meta":{"next_cursor":null}}`,
		`{"ok":true,"v":1,"data":{},"meta":{"next_cursor":1}}`,
		`{"ok":true,"v":1,"data":{},"meta":{"profile":null}}`,
		`{"ok":true,"v":1,"data":{},"meta":{"profile":false}}`,
		`{"ok":true,"v":1,"data":{},"meta":{"site":null}}`,
		`{"ok":true,"v":1,"data":{},"meta":{"site":[]}}`,
	} {
		if !strings.Contains(invalidSection, fixture) {
			t.Errorf("rendered contract invalid section lacks fixture %s", fixture)
		}
	}

	recovery := []string{
		"Only for a `confluence-cli pages create` or `confluence-cli pages update`",
		"invoked with both `--confirm-intent` and `--yes`, without `--dry-run`",
		"otherwise fails the complete v1 grammar above",
		"inspect at most the first 4096 bytes of stderr.",
		"only one of these exact complete, newline-terminated lines counts",
		"error: WRITE_APPLIED_LOCAL_FAILURE: pages.create applied, but local finalization failed",
		"error: WRITE_APPLIED_LOCAL_FAILURE: pages.update applied, but local finalization failed",
		"Match from the anchored `error: WRITE_APPLIED_LOCAL_FAILURE:` at line start",
		"This exact marker means known applied and no retry; the write must not be repeated.",
		"Otherwise, do not retry and do not claim that the write applied.",
		"Confluence reads to reconcile page ID, space, title, parent, version, and content",
	}
	assertOrderedPhrases(t, got[fallbackStart:], recovery)

	for _, obsolete := range []string{
		"object with boolean `ok` and integer `v` is the primary result",
		"with an `ok` boolean and integer `v`",
	} {
		if strings.Contains(got, obsolete) {
			t.Errorf("rendered contract retains obsolete envelope grammar %q", obsolete)
		}
	}
	if !strings.Contains(got, "stderr as a contract fallback in any other case.") {
		t.Fatal("rendered contract does not limit stderr fallback to confirmed writes")
	}
	if strings.Contains(got, "explicitly confirmed") {
		t.Fatal("rendered contract uses an undefined confirmation shorthand")
	}
}

func assertOrderedPhrases(t *testing.T, got string, phrases []string) {
	t.Helper()
	position := 0
	for _, phrase := range phrases {
		index := strings.Index(got[position:], phrase)
		if index < 0 {
			t.Fatalf("rendered contract lacks ordered phrase %q", phrase)
		}
		position += index + len(phrase)
	}
}

func TestRepositoryRootWorksBelowRoot(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Join(root, "tools", "gencontract")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
	if got, err := repositoryRoot(); err != nil || got != root {
		t.Errorf("repositoryRoot = %q, %v; want %q", got, err, root)
	}
}
