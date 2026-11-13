cask "no-artifacts" do
  version "1.0.0"
  sha256 "5555555555555555555555555555555555555555555555555555555555555555"

  url "https://example.com/no-artifacts-1.0.0.dmg"
  name "No Artifacts"
  desc "Fixture app that installs nothing"
  homepage "https://example.com/no-artifacts"

  uninstall delete: "/Library/NoArtifacts"
end
