package main

import (
	"strings"
	"testing"
)

func TestCollectIncludesEveryPublicLeafAndHidesAliases(t *testing.T) {
	tree := collect(rootCommand())
	want := []string{
		"confluence-cli auth allow-spaces clear",
		"confluence-cli auth allow-spaces set",
		"confluence-cli auth allow-spaces show",
		"confluence-cli auth list",
		"confluence-cli auth login",
		"confluence-cli auth logout",
		"confluence-cli auth migrate-keychain",
		"confluence-cli auth status",
		"confluence-cli contract",
		"confluence-cli pages create",
		"confluence-cli pages get",
		"confluence-cli pages list",
		"confluence-cli pages update",
		"confluence-cli search",
		"confluence-cli skills install",
		"confluence-cli skills uninstall",
		"confluence-cli spaces get",
		"confluence-cli spaces list",
		"confluence-cli version",
	}
	if len(tree.Commands) != len(want) {
		t.Fatalf("commands=%d want=%d: %+v", len(tree.Commands), len(want), tree.Commands)
	}
	for index, command := range tree.Commands {
		if command.Path != want[index] {
			t.Fatalf("command[%d]=%q want=%q", index, command.Path, want[index])
		}
	}
	for _, flag := range tree.Globals {
		if flag.Name == "json" {
			t.Fatal("hidden --json alias entered generated reference")
		}
	}
}

func TestRenderMarkdownPinsWriteSafetyFlags(t *testing.T) {
	rendered := renderMarkdown(collect(rootCommand()))
	for _, required := range []string{
		"### `confluence-cli pages create`",
		"### `confluence-cli pages update`",
		"`--dry-run`",
		"`--yes`",
		"`--expected-version`",
		"`--body-file`",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered command reference lacks %q", required)
		}
	}
}

func TestRenderMarkdownContainsStrictV1GrammarBeforeWriteRecovery(t *testing.T) {
	rendered := strings.Join(strings.Fields(renderMarkdown(collect(rootCommand()))), " ")
	ordered := []string{
		"A v1 envelope is exactly one complete top-level JSON object followed only by whitespace.",
		"It requires boolean `ok` and a JSON integer `v` whose value is exactly `1`",
		"If `ok` is `true`, `data` must be present and non-null; `error` and `hint` must be absent, and their presence is invalid even when `null`.",
		"If `ok` is `false`, `data` must be absent; `error` must be a present non-null object with string `code` and `message`, and `hint` must be a present string.",
		"Forbidden-key presence is invalid even when `null`.",
		"`meta` is optional; unknown additive fields are tolerated. They never repair an invalid known member.",
		"Any malformed or unsupported envelope makes stdout invalid.",
		"Valid v1 examples:",
	}
	assertOrderedPhrases(t, rendered, ordered)

	validStart := strings.Index(rendered, "Valid v1 examples:")
	invalidStart := strings.Index(rendered, "Invalid examples:")
	if validStart < 0 || invalidStart <= validStart {
		t.Fatalf("rendered command reference fixture sections are missing or out of order: valid=%d invalid=%d", validStart, invalidStart)
	}
	validSection := rendered[validStart:invalidStart]
	for _, fixture := range []string{
		`{"ok":true,"v":1,"data":{},"meta":{"future":true},"future":true}`,
		`{"ok":false,"v":1,"error":{"code":"PROFILE_REQUIRED","message":"profile is required","future":true},"hint":"pass --profile","future":true}`,
	} {
		if !strings.Contains(validSection, fixture) {
			t.Errorf("rendered command reference valid section lacks fixture %s", fixture)
		}
	}

	fallbackStart := strings.Index(rendered, "Only for a `confluence-cli pages create` or `confluence-cli pages update`")
	if fallbackStart <= invalidStart {
		t.Fatalf("rendered command reference fallback does not follow invalid fixtures: invalid=%d fallback=%d", invalidStart, fallbackStart)
	}
	invalidSection := rendered[invalidStart:fallbackStart]
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
	} {
		if !strings.Contains(invalidSection, fixture) {
			t.Errorf("rendered command reference invalid section lacks fixture %s", fixture)
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
	assertOrderedPhrases(t, rendered[fallbackStart:], recovery)

	for _, obsolete := range []string{
		"object with boolean `ok` and integer `v` is the primary result",
		"with an `ok` boolean and integer `v`",
	} {
		if strings.Contains(rendered, obsolete) {
			t.Errorf("rendered command reference retains obsolete envelope grammar %q", obsolete)
		}
	}
	if !strings.Contains(rendered, "stderr as a contract fallback in any other case.") {
		t.Fatal("rendered command reference does not limit stderr fallback to confirmed writes")
	}
	if strings.Contains(rendered, "explicitly confirmed") {
		t.Fatal("rendered command reference uses an undefined confirmation shorthand")
	}
}

func assertOrderedPhrases(t *testing.T, got string, phrases []string) {
	t.Helper()
	position := 0
	for _, phrase := range phrases {
		index := strings.Index(got[position:], phrase)
		if index < 0 {
			t.Fatalf("rendered command reference lacks ordered phrase %q", phrase)
		}
		position += index + len(phrase)
	}
}
