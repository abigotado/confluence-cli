#!/usr/bin/env ruby

require "minitest/autorun"
require "open3"
require "tempfile"
require "yaml"

class VerifyPortableReleasePolicyTest < Minitest::Test
  ROOT = File.expand_path("../..", __dir__)
  VERIFIER = File.join(__dir__, "verify-portable-release-policy.rb")
  CONFIG = YAML.safe_load(File.read(File.join(ROOT, ".goreleaser.yaml")), permitted_classes: [], aliases: false)
  GO_WORKFLOW = File.read(File.join(ROOT, ".github/workflows/go.yaml"))
  GO_WORKFLOW_CONFIG = YAML.safe_load(GO_WORKFLOW, permitted_classes: [], aliases: false)
  RELEASE_WORKFLOW = File.read(File.join(ROOT, ".github/workflows/release.yaml"))
  RELEASE_WORKFLOW_CONFIG = YAML.safe_load(RELEASE_WORKFLOW, permitted_classes: [], aliases: false)
  RELEASE_PREFIX = "github.com/abigotado/confluence-cli/internal/cli.release"
  EXPECTED_RELEASE_LDFLAGS = [
    "-X #{RELEASE_PREFIX}Version=v{{ .Version }}",
    "-X #{RELEASE_PREFIX}Commit={{ .FullCommit }}",
    "-X #{RELEASE_PREFIX}CommitTime={{ .CommitDate }}",
  ].freeze

  def verify(config)
    Tempfile.create(["goreleaser", ".yaml"]) do |file|
      file.write(YAML.dump(config))
      file.flush
      _stdout, stderr, status = Open3.capture3("ruby", VERIFIER, file.path)
      return [status.success?, stderr]
    end
  end

  def mutate
    config = Marshal.load(Marshal.dump(CONFIG))
    yield config
    config
  end

  def test_current_configuration_passes
    success, stderr = verify(CONFIG)

    assert success, stderr
  end

  def test_release_provenance_uses_the_machine_contract_field
    assert_includes RELEASE_WORKFLOW, ".data.commit_time"
    refute_includes RELEASE_WORKFLOW, ".data.commitTime"
  end

  def test_direct_release_injects_and_asserts_exact_canonical_provenance
    expected = {
      "#{RELEASE_PREFIX}Version" => "${RELEASE_TAG}",
      "#{RELEASE_PREFIX}Commit" => "${expected_commit}",
      "#{RELEASE_PREFIX}CommitTime" => "${expected_commit_time}",
    }
    expected.each do |assignment, value|
      assert_equal 1, RELEASE_WORKFLOW.scan("-X #{assignment}=#{value}").length
    end
    assert_equal expected.keys.sort, RELEASE_WORKFLOW.scan(/#{Regexp.escape(RELEASE_PREFIX)}[A-Za-z0-9_]*/).uniq.sort
    assert_includes RELEASE_WORKFLOW, "commit_epoch=$(git show -s --format=%ct HEAD)"
    assert_includes RELEASE_WORKFLOW, %(date --utc --date="@${commit_epoch}" '+%Y-%m-%dT%H:%M:%SZ')
    assert_includes RELEASE_WORKFLOW, "--fields version,commit,commit_time"
    assert_includes RELEASE_WORKFLOW, '[ "${commit}" != "${expected_commit}" ]'
    assert_includes RELEASE_WORKFLOW, '[ "${commit_time}" != "${expected_commit_time}" ]'
  end

  def test_linux_snapshot_asserts_exact_canonical_provenance
    assert_includes GO_WORKFLOW, "commit_epoch=$(git show -s --format=%ct HEAD)"
    assert_includes GO_WORKFLOW, %(date --utc --date="@${commit_epoch}" '+%Y-%m-%dT%H:%M:%SZ')
    assert_includes GO_WORKFLOW, "--fields version,commit,commit_time"
    assert_includes GO_WORKFLOW, '[ "${commit}" != "${expected_commit}" ]'
    assert_includes GO_WORKFLOW, '[ "${commit_time}" != "${expected_commit_time}" ]'
  end

  def test_go_matrix_covers_linux_macos_and_windows
    matrix = GO_WORKFLOW_CONFIG.fetch("jobs").fetch("test").fetch("strategy").fetch("matrix")

    assert_equal %w[macos-latest ubuntu-latest windows-latest], matrix.fetch("os").sort
  end

  def test_go_matrix_uses_platform_appropriate_test_commands
    steps = GO_WORKFLOW_CONFIG.fetch("jobs").fetch("test").fetch("steps")
    race_test = steps.find { |step| step["name"] == "go test" }
    windows_test = steps.find { |step| step["name"] == "go test (Windows)" }

    refute_nil race_test
    assert_equal "runner.os != 'Windows'", race_test.fetch("if")
    assert_equal "go test -race ./...", race_test.fetch("run")
    refute_nil windows_test
    assert_equal "runner.os == 'Windows'", windows_test.fetch("if")
    assert_equal "go test -count=1 ./...", windows_test.fetch("run")
  end

  def test_posix_blocks_that_reach_windows_use_bash
    steps = GO_WORKFLOW_CONFIG.fetch("jobs").fetch("test").fetch("steps")

    ["gofmt", "Generated contract is current"].each do |name|
      step = steps.find { |candidate| candidate["name"] == name }
      refute_nil step
      assert_equal "bash", step.fetch("shell")
    end
  end

  def test_windows_safety_keeps_fail_closed_checks
    job = GO_WORKFLOW_CONFIG.fetch("jobs").fetch("windows-safety")
    steps = job.fetch("steps")
    mutation_test = steps.find { |step| step["name"] == "Skill mutation fails closed" }
    credential_build = steps.find { |step| step["name"] == "Build unsupported credential backend" }

    assert_equal "windows-latest", job.fetch("runs-on")
    refute_nil mutation_test
    assert_equal "go test -count=1 -run '^TestWindowsSkillMutationFailsClosed$' ./internal/skills", mutation_test.fetch("run")
    refute_nil credential_build
    assert_equal "go build ./cmd/confluence-cli", credential_build.fetch("run")
  end

  def test_required_check_aggregates_matrix_and_windows_safety
    job = GO_WORKFLOW_CONFIG.fetch("jobs").fetch("required")

    assert_equal "Build and test", job.fetch("name")
    assert_equal "${{ always() }}", job.fetch("if")
    assert_equal %w[test windows-safety], job.fetch("needs")
  end

  def test_release_requires_the_tagged_commit_on_the_default_branch
    assert_includes RELEASE_WORKFLOW, "refs/heads/${DEFAULT_BRANCH}:refs/remotes/origin/${DEFAULT_BRANCH}"
    assert_includes RELEASE_WORKFLOW, 'git merge-base --is-ancestor HEAD "refs/remotes/origin/${DEFAULT_BRANCH}"'
  end

  def test_release_checkout_does_not_persist_credentials
    checkout = release_steps.find { |step| step["uses"]&.start_with?("actions/checkout@") }

    refute_nil checkout
    assert_equal false, checkout.fetch("with").fetch("persist-credentials")
  end

  def test_release_workflow_defaults_to_read_only_contents
    assert_equal({ "contents" => "read" }, RELEASE_WORKFLOW_CONFIG.fetch("permissions"))
  end

  def test_github_token_is_scoped_to_the_final_publish_step
    release_job = RELEASE_WORKFLOW_CONFIG.fetch("jobs").fetch("release")
    refute_includes RELEASE_WORKFLOW_CONFIG.fetch("env", {}), "GITHUB_TOKEN"
    refute_includes release_job.fetch("env", {}), "GITHUB_TOKEN"
    assert_equal 1, RELEASE_WORKFLOW.scan(/\$\{\{\s*(?:secrets\.GITHUB_TOKEN|github\.token)\s*\}\}/i).length

    token_steps = release_steps.select { |step| step.fetch("env", {}).key?("GITHUB_TOKEN") }
    assert_equal 1, token_steps.length
    assert_same release_steps.last, token_steps.first
    assert_equal "goreleaser release", token_steps.first.fetch("name")
    assert_equal "${{ secrets.GITHUB_TOKEN }}", token_steps.first.fetch("env").fetch("GITHUB_TOKEN")
  end

  def test_github_actions_are_pinned_to_commit_shas
    workflows = Dir.glob(File.join(ROOT, ".github/workflows/*.{yaml,yml}"))
    uses = workflows.flat_map { |workflow| File.read(workflow).scan(/^\s*uses:\s+([^\s#]+)/).flatten }

    refute_empty uses
    uses.each { |action| assert_match(/@[0-9a-f]{40}\z/, action) }
  end

  def test_rejects_an_additional_darwin_build
    config = mutate do |value|
      value["builds"] << {
        "id" => "confluence-cli-darwin",
        "env" => ["CGO_ENABLED=1"],
        "goos" => ["darwin"],
        "goarch" => ["arm64"],
      }
    end

    success, stderr = verify(config)

    refute success
    assert_includes stderr, "exactly one build is required"
  end

  def test_rejects_a_darwin_target_override
    config = mutate do |value|
      value["builds"].first["targets"] = ["darwin_arm64"]
    end

    success, stderr = verify(config)

    refute success
    assert_includes stderr, "must not override the platform matrix with targets"
  end

  def test_rejects_a_homebrew_cask
    config = mutate do |value|
      value["homebrew_casks"] = [{ "name" => "confluence-cli" }]
    end

    success, stderr = verify(config)

    refute success
    assert_includes stderr, "unsupported top-level section homebrew_casks is not permitted"
  end

  def test_rejects_a_missing_release_provenance_assignment
    config = mutate do |value|
      value["builds"].first["ldflags"].delete(EXPECTED_RELEASE_LDFLAGS[1])
    end

    success, stderr = verify(config)

    refute success
    assert_includes stderr, "must inject exactly one releaseCommit assignment"
  end

  def test_rejects_a_duplicate_release_provenance_assignment
    config = mutate do |value|
      value["builds"].first["ldflags"] << EXPECTED_RELEASE_LDFLAGS[0]
    end

    success, stderr = verify(config)

    refute success
    assert_includes stderr, "must inject exactly one releaseVersion assignment"
  end

  def test_rejects_a_conflicting_release_provenance_assignment
    config = mutate do |value|
      flags = value["builds"].first["ldflags"]
      flags[flags.index(EXPECTED_RELEASE_LDFLAGS[2])] = "-X #{RELEASE_PREFIX}CommitTime={{ .Date }}"
    end

    success, stderr = verify(config)

    refute success
    assert_includes stderr, "releaseCommitTime assignment must be exactly"
  end

  def test_rejects_an_unexpected_internal_cli_release_assignment
    config = mutate do |value|
      value["builds"].first["ldflags"] << "-s -w -X #{RELEASE_PREFIX}Branch={{ .Branch }}"
    end

    success, stderr = verify(config)

    refute success
    assert_includes stderr, "unexpected internal/cli release assignment #{RELEASE_PREFIX}Branch"
  end

  def test_rejects_universal_binaries
    config = mutate do |value|
      value["universal_binaries"] = [{ "id" => "confluence-cli-universal" }]
    end

    success, stderr = verify(config)

    refute success
    assert_includes stderr, "unsupported top-level section universal_binaries is not permitted"
  end

  def test_rejects_release_extra_files
    config = mutate do |value|
      value["release"]["extra_files"] = [{ "glob" => "confluence-cli_darwin_arm64.tar.gz" }]
    end

    success, stderr = verify(config)

    refute success
    assert_includes stderr, "unsupported release key extra_files is not permitted"
  end

  def test_rejects_an_alternate_upload_channel
    config = mutate do |value|
      value["uploads"] = [{ "name" => "darwin-upload" }]
    end

    success, stderr = verify(config)

    refute success
    assert_includes stderr, "unsupported top-level section uploads is not permitted"
  end

  def test_rejects_an_additional_before_hook
    config = mutate do |value|
      value["before"]["hooks"] << "make darwin-archive"
    end

    success, stderr = verify(config)

    refute success
    assert_includes stderr, "before hooks must contain only go mod download"
  end

  def test_rejects_a_quarantine_bypass
    config = mutate do |value|
      value["hooks"] = { "post" => "/usr/bin/xattr -dr com.apple.quarantine confluence-cli" }
    end

    success, stderr = verify(config)

    refute success
    assert_includes stderr, "configuration contains a Gatekeeper quarantine bypass"
  end

  private

  def release_steps
    RELEASE_WORKFLOW_CONFIG.fetch("jobs").fetch("release").fetch("steps")
  end
end
