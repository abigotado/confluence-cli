#!/usr/bin/env ruby

require "minitest/autorun"
require "open3"
require "tempfile"
require "yaml"

class VerifyPortableReleasePolicyTest < Minitest::Test
  ROOT = File.expand_path("../..", __dir__)
  VERIFIER = File.join(__dir__, "verify-portable-release-policy.rb")
  CONFIG = YAML.safe_load(File.read(File.join(ROOT, ".goreleaser.yaml")), permitted_classes: [], aliases: false)
  RELEASE_WORKFLOW = File.read(File.join(ROOT, ".github/workflows/release.yaml"))

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

  def test_release_requires_the_tagged_commit_on_the_default_branch
    assert_includes RELEASE_WORKFLOW, "refs/heads/${DEFAULT_BRANCH}:refs/remotes/origin/${DEFAULT_BRANCH}"
    assert_includes RELEASE_WORKFLOW, 'git merge-base --is-ancestor HEAD "refs/remotes/origin/${DEFAULT_BRANCH}"'
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

  def test_rejects_a_missing_version_fallback
    config = mutate do |value|
      value["builds"].first["ldflags"] = ["-s -w"]
    end

    success, stderr = verify(config)

    refute success
    assert_includes stderr, "must inject the releaseVersion fallback"
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
end
