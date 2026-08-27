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

func TestRenderMarkdownContainsOrderedInvalidStdoutRecoveryGrammar(t *testing.T) {
	rendered := renderMarkdown(collect(rootCommand()))
	ordered := []string{
		"1. Parse stdout as exactly one JSON value.",
		"2. Only for a `confluence-cli pages create` or `confluence-cli pages update`",
		"invoked with both `--confirm-intent` and `--yes`, without `--dry-run`",
		"stdout is empty, fails JSON parsing",
		"(including premature EOF), contains multiple JSON values, or lacks either",
		"inspect at most the first 4096 bytes of stderr.",
		"3. In that bounded diagnostic, only one of these exact complete,",
		"newline-terminated lines counts",
		"error: WRITE_APPLIED_LOCAL_FAILURE: pages.create applied, but local finalization failed",
		"error: WRITE_APPLIED_LOCAL_FAILURE: pages.update applied, but local finalization failed",
		"Match from the anchored `error: WRITE_APPLIED_LOCAL_FAILURE:` at line start",
		"This exact marker means known applied",
		"and no retry; the write must not be repeated.",
		"4. Otherwise, do not retry and do not claim that the write applied.",
		"Use bounded",
		"Confluence reads to reconcile page ID, space, title, parent, version, and",
	}
	position := 0
	for _, phrase := range ordered {
		index := strings.Index(rendered[position:], phrase)
		if index < 0 {
			t.Fatalf("rendered command reference lacks ordered recovery phrase %q", phrase)
		}
		position += index + len(phrase)
	}
	if !strings.Contains(rendered, "A single object with boolean `ok`\n   and integer `v` is the primary result; never replace it with stderr.") {
		t.Fatal("rendered command reference does not keep a valid ok/v envelope primary")
	}
	if !strings.Contains(rendered, "stderr as a contract fallback in any other case.") {
		t.Fatal("rendered command reference does not limit stderr fallback to confirmed writes")
	}
	if strings.Contains(rendered, "explicitly confirmed") {
		t.Fatal("rendered command reference uses an undefined confirmation shorthand")
	}
}
