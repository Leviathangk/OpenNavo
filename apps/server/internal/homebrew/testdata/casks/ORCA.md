Orca is an unchanged fixture from [stablyai/homebrew-orca@a98f4efbafd0a0959fde2de1680179d3e01bab88](https://github.com/stablyai/homebrew-orca/blob/a98f4efbafd0a0959fde2de1680179d3e01bab88/Casks/orca.rb), fetched on 2026-10-29.

Line 2 declares literal `arch arm: "arm64", intel: "x64"` values. Its per-CPU SHA-256 values and `#{arch}` URL exercise the existing Homebrew API variation model. The parser never executes Ruby. Architecture/OS blocks remain unsupported.
