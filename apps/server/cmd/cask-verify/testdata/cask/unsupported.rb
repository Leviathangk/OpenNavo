cask "unsupported" do
  version "1.0.0"
  sha256 "2222222222222222222222222222222222222222222222222222222222222222"

  url "https://example.com/unsupported-1.0.0.dmg"
  name "Unsupported"
  desc "Fixture app that uses a rejected language block"
  homepage "https://example.com/unsupported"

  language :en do
    name "Unsupported"
  end

  app "Unsupported.app"
end
