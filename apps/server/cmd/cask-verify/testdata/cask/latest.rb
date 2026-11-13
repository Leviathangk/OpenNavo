cask "latest" do
  version :latest
  sha256 "4444444444444444444444444444444444444444444444444444444444444444"

  url "https://example.com/latest.dmg"
  name "Latest"
  desc "Fixture app that never pins a version"
  homepage "https://example.com/latest"

  app "Latest.app"
end
