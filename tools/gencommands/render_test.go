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
