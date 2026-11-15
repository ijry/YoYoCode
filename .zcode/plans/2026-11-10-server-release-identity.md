# Server release identity implementation plan

Goal: publish YoYoCode server bundles and keep the installer, updater, version and signing identity consistent.

Approved scope: user accepted the recommendation to use yoyocode-server-* assets, fix installation/update/signature configuration, and retain legacy codeg-server-* assets.

Design: keep codeg-server[.exe], CODEG_* and installed paths unchanged. Publish independently packed/signed new and legacy archives, each with its own matching root directory. Docker consumes new artifact names. Installers use ijry/YoYoCode and prefer new names, falling back to old names only for missing assets in that same repository. Server updater uses new asset names and the existing PLUS public key.

Ruling: use a dedicated server-latest.json manifest with the full x.y.z-plus.N tag, and a --server identity-script mode that also sets Cargo's package version. Desktop safe-mode latest.json stays untouched because MSI and desktop updater use the base version. Without this, plus.N iterations cannot update and the signature version can disagree with the manifest. Existing upstream-configured servers need one manual reinstall.

Tasks:
- [ ] Add regression checks for server identity injection, new/legacy archive layouts and installer fallback; observe failures.
- [ ] Update CI matrix, packaging/signing/upload loops and Docker consumers. Publish server-latest.json only once from the Linux x64 server build.
- [ ] Add --server injection, update installers and Rust update URLs/public key/asset selection; retain version-bound signature verification. Add PLUS ordering and key consistency coverage.
- [ ] Run Node regression checks, shell/PowerShell parsing and targeted Rust checks; review diff for compatibility and scope.

Review focus: both archive root names; signatures and checksum filenames cover both bundles; server running/manifest/signature versions agree; fallback only on 404 and never switches repository; desktop version remains unchanged.

No commit, push, tag or publication is authorized. Local changes and local test execution only.

Completion ledger:
- Implemented CI new/legacy archives with separate roots/signatures/checksums, Docker artifact selection and server version smoke checks.
- Implemented --server Cargo version injection, installer 404-only fallback, PLUS public key and release repository migration.
- Review finding fixed: tauri-runtime Web handler keeps desktop latest.json; standalone server uses server-latest.json.
- Node targeted tests: 15/15 pass, including Windows ZIP extraction; Rust update module: 68/68 pass. ESLint of changed scripts, Prettier of changed scripts/release.yml and diff whitespace checks passed.
- Full pnpm test: 780 files passed, 7 failed; 11686 cases passed, 6 failed. Failures outside edited files: tasks-page.focus/search (mock getState), html-code-preview (timeout), json-render-pre (chart count), aux-panel-git-changes-tab (two expansion cases), use-transcript-known-invocations (source assertion), activity-rows (streaming fold). No full CI release or signing with production secrets was run.
- Existing unrelated Rust warnings in comic/task.rs and comic/video.rs remain.

