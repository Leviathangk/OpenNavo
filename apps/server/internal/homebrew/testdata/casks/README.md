These fixtures are parsed locally; no Ruby or install hooks are executed.

- `double-shot.rb` is the unchanged public cask at [noahelhadedy/homebrew-tap@68fb394](https://github.com/noahelhadedy/homebrew-tap/blob/68fb39413dbaa57e20ffa4c3b977f7ac76b9f53f/Casks/double-shot.rb). Line 21 selects `postflight_steps` or legacy `postflight` with `respond_to?`. Its expected JSON uses the modern hook marker, consistent with an unconditional `postflight_steps` block; hook bodies and helper locals are omitted.
- `rectangle.rb` is the unchanged public cask at [Homebrew/homebrew-cask@62514aa](https://github.com/Homebrew/homebrew-cask/blob/62514aa815052bc35a8fd17d2e7fadff8784c85a/Casks/r/rectangle.rb).
- `swiftvm.json` corresponds to the existing `swiftvmCask` source in `cask_source_test.go`.

The complete expected JSON for Rectangle and SwiftVM was captured with the parser before flight-guard support. Tests compare every output field and the normalized catalog metadata to prevent changes to previously supported casks.
