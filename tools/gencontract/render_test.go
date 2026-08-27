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

func TestRenderContainsOrderedInvalidStdoutRecoveryGrammar(t *testing.T) {
	got := renderMarkdown(errx.Describe())
	ordered := []string{
		"1. Parse stdout as exactly one JSON value.",
		"2. Only for an explicitly confirmed `confluence-cli pages create` or",
		"stdout is empty, fails JSON parsing",
		"(including premature EOF), contains multiple JSON values, or lacks either",
		"inspect at most the first 4096 bytes of stderr.",
		"3. In that bounded diagnostic, only one of these exact complete lines counts",
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
		index := strings.Index(got[position:], phrase)
		if index < 0 {
			t.Fatalf("rendered contract lacks ordered recovery phrase %q", phrase)
		}
		position += index + len(phrase)
	}
	if !strings.Contains(got, "A single object with boolean `ok`\n   and integer `v` is the primary result; never replace it with stderr.") {
		t.Fatal("rendered contract does not keep a valid ok/v envelope primary")
	}
	if !strings.Contains(got, "stderr as a contract fallback in any other case.") {
		t.Fatal("rendered contract does not limit stderr fallback to confirmed writes")
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
