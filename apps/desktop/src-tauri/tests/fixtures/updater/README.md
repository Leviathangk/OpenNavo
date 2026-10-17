# Development update fixtures

`OpenNavo_demo.app.tar.gz` is a nonexecutable synthetic app containing only Info.plist and a version marker; it is not distributable software. Its `.sig` and `development.pub` are public verification fixtures produced with a temporary development key, not a production release key.

Regenerate with `python3 -I apps/desktop/src-tauri/tests/fixtures/updater/generate.py`. The generator uses only local pnpm/Tauri signer and the Python standard library. The private key is stored in ignored `src-tauri/target/dev_updater/development.key`, with directory mode 700 and file mode 600. Tool output is never logged. After regenerating the private key, synchronize any development public key used by the default configuration with this `.pub`; the fixture generator must never overwrite a production release public key.

`cargo test --manifest-path apps/desktop/src-tauri/Cargo.toml --test updater` calls the real updater plugin in temporary directories to verify checking, downloading, minisign verification, and replacement from 0.0.1 to 0.0.2, rejecting tampered packages, signed-version mismatches, and downgrades. HTTP uses only random loopback ports. MockRuntime creates no native windows and never modifies installed apps. Tests do not need the private key.

`https.py` creates a fresh loopback TLS certificate for each test server using `/usr/bin/openssl`. The private key is removed immediately after loading it into the TLS context. The public certificate is written beside the temporary payload as `<payload>.cert.pem` and trusted only by that test client. No TLS private key is committed.
