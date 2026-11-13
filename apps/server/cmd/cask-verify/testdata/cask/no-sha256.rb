cask "no-sha256" do
  version "1.0.0"

  url "https://example.com/no-sha256-1.0.0.dmg"
  name "No SHA"
  desc "Fixture app without a checksum"
  homepage "https://example.com/no-sha256"

  app "NoSHA.app"
end
